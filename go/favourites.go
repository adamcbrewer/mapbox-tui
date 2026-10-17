package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/text/cases"
)

// Favourites belong to the application, independently of any workspace.
type favourite struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Lon         float64     `json:"longitude"`
	Lat         float64     `json:"latitude"`
	Source      placeSource `json:"source"`
	Attribution string      `json:"-"`
	ProviderID  string      `json:"provider_id,omitempty"`
}

// Missing coordinates must not silently turn a damaged record into (0, 0).
func (f *favourite) UnmarshalJSON(data []byte) error {
	type fields favourite
	var decoded struct {
		fields
		Lon         *float64 `json:"longitude"`
		Lat         *float64 `json:"latitude"`
		Attribution string   `json:"attribution"` // Version 1 only.
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Lon == nil || decoded.Lat == nil {
		return fmt.Errorf("favourite coordinates are missing")
	}
	*f = favourite(decoded.fields)
	f.Lon, f.Lat = *decoded.Lon, *decoded.Lat
	f.Attribution = decoded.Attribution
	if f.Source.mapboxGeocoded() {
		f.Attribution = geocodingCredit
	}
	return nil
}

func (f favourite) location() placeLocation {
	return placeLocation{
		Name: f.Name, Lon: f.Lon, Lat: f.Lat, Source: f.Source,
		Attribution: f.Attribution, ProviderID: f.ProviderID,
	}
}

const favouriteFileVersion = 2

type favouriteFile struct {
	Version    int         `json:"version"`
	Favourites []favourite `json:"favourites"`
}

type favouriteStore struct{ path string }

func localFavouriteStore() (favouriteStore, error) {
	path, err := appSettingsPath()
	if err != nil {
		return favouriteStore{}, err
	}
	return favouriteStore{path: filepath.Join(filepath.Dir(path), "favourites.json")}, nil
}

func validateFavourite(f favourite) error {
	if !f.Source.valid() {
		return fmt.Errorf("unknown location source %q", f.Source)
	}
	if f.ID == "" || strings.TrimSpace(f.Name) == "" || f.Name != cleanPlaceText(f.Name) {
		return fmt.Errorf("favourites need an ID and a printable, non-blank name")
	}
	if !validLocation(f.Lon, f.Lat) {
		return fmt.Errorf("coordinates must be latitude -90..90, longitude -180..180")
	}
	geocoded := f.Source.mapboxGeocoded() && f.ProviderID != "" && f.Attribution != ""
	if !f.location().userEntered() && !geocoded {
		return fmt.Errorf("this location cannot be saved; save an address or town from the search results")
	}
	return nil
}

func favouriteNameKey(name string) string {
	// Preserve the existing dotted-I equivalence before full Unicode folding.
	name = strings.ToLower(strings.TrimSpace(name))
	return cases.Fold().String(name)
}

func (s favouriteStore) read() ([]favourite, error) {
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return []favourite{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read favourites: %w", err)
	}
	defer file.Close()
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read favourites: %w", err)
	}
	var saved favouriteFile
	if len(data) > limit || json.Unmarshal(data, &saved) != nil || saved.Favourites == nil {
		return nil, fmt.Errorf("invalid favourites file at %q; existing file left unchanged", s.path)
	}
	if saved.Version != 1 && saved.Version != favouriteFileVersion {
		return nil, fmt.Errorf("invalid favourites file at %q; existing file left unchanged", s.path)
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, f := range saved.Favourites {
		if err := validateFavourite(f); err != nil {
			return nil, fmt.Errorf("invalid favourites file: %w", err)
		}
		name := favouriteNameKey(f.Name)
		if ids[f.ID] || names[name] {
			return nil, fmt.Errorf("duplicate favourite ID or name; existing file left unchanged")
		}
		ids[f.ID], names[name] = true, true
	}
	return saved.Favourites, nil
}

// Re-read under a process lock and edit by ID, preserving unrelated edits made
// by another viewer. The expected value also catches stale rename/delete forms.
func (s favouriteStore) change(expected *favourite, replacement *favourite) error {
	if replacement != nil {
		if err := validateFavourite(*replacement); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create favourites directory: %w", err)
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open favourites lock: %w", err)
	}
	defer lock.Close()
	if err := lockLocalFile(lock); err != nil {
		return fmt.Errorf("favourites are busy; try again")
	}
	defer unlockLocalFile(lock)
	entries, err := s.read()
	if err != nil {
		return err
	}
	index := -1
	if expected != nil {
		index = slices.IndexFunc(entries, func(f favourite) bool { return f.ID == expected.ID })
		if index < 0 || entries[index] != *expected {
			return fmt.Errorf("favourite changed in another viewer; reopen Favourites and try again")
		}
	}
	if replacement != nil {
		name := favouriteNameKey(replacement.Name)
		for i, f := range entries {
			if i == index {
				continue
			}
			sameProvider := f.Source == replacement.Source ||
				(f.Source.mapboxGeocoded() && replacement.Source.mapboxGeocoded())
			samePlace := replacement.ProviderID != "" &&
				f.ProviderID == replacement.ProviderID && sameProvider
			if samePlace {
				return fmt.Errorf("this place is already a favourite")
			}
			if favouriteNameKey(f.Name) == name || f.ID == replacement.ID {
				return fmt.Errorf("a favourite with that name already exists")
			}
		}
	}
	switch {
	case replacement == nil && index >= 0:
		entries = slices.Delete(entries, index, index+1)
	case index >= 0:
		entries[index] = *replacement
	case replacement != nil:
		entries = append(entries, *replacement)
	default:
		return fmt.Errorf("no favourite selected")
	}
	data, err := json.MarshalIndent(favouriteFile{Version: favouriteFileVersion, Favourites: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode favourites: %w", err)
	}
	if len(data)+1 > 1<<20 {
		return fmt.Errorf("favourites file is full")
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".favourites-*.json")
	if err != nil {
		return fmt.Errorf("create favourites file: %w", err)
	}
	defer os.Remove(file.Name())
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("write favourites: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close favourites: %w", closeErr)
	}
	if err := os.Rename(file.Name(), s.path); err != nil {
		return fmt.Errorf("replace favourites: %w", err)
	}
	return nil
}

func newFavourite(name string, location placeLocation) favourite {
	if location.Source.mapboxGeocoded() {
		location.Attribution = geocodingCredit
	}
	return favourite{
		ID: rand.Text(), Name: strings.TrimSpace(name), Lon: location.Lon, Lat: location.Lat,
		Source: location.Source, Attribution: location.Attribution, ProviderID: location.ProviderID,
	}
}
