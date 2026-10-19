package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const workspaceVersion = 1
const workspaceFileLimit = 4 << 20

var errWorkspaceConflict = errors.New("saved workspace changed")

// This is an explicit persistence model: runtime geometry, clients and requests
// never cross this boundary. Import item keys apply only to identical source bytes.
type savedWorkspace struct {
	Planner   string          `json:"planner,omitempty"`
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Revision  uint64          `json:"revision"`
	Created   time.Time       `json:"created"`
	Modified  time.Time       `json:"modified"`
	Order     []string        `json:"order"`
	Imports   []savedImport   `json:"imports"`
	Journeys  []savedJourney  `json:"journeys"`
	Selection *savedSelection `json:"selection,omitempty"`
	Camera    workspaceCamera `json:"camera"`
}

type savedImport struct {
	ID          string      `json:"id"`
	Path        string      `json:"path"`
	Fingerprint string      `json:"sha256"`
	Items       []savedItem `json:"items"`
	problem     string
}

type savedItem struct {
	ID      string        `json:"id"`
	Key     int           `json:"key"`
	Visible bool          `json:"visible"`
	Color   activityColor `json:"colour_slot"`
}

type savedJourney struct {
	ID    string           `json:"id"`
	Item  savedItem        `json:"item"`
	Name  string           `json:"name"`
	Mode  journeyMode      `json:"mode"`
	Stops []*savedLocation `json:"stops"`
}

type savedLocation struct {
	Name        string      `json:"name"`
	Lon         *float64    `json:"longitude"`
	Lat         *float64    `json:"latitude"`
	Source      placeSource `json:"source"`
	ProviderID  string      `json:"provider_id,omitempty"`
	Attribution string      `json:"attribution,omitempty"`
}

type savedSelection struct {
	Item  string `json:"item"`
	Point int    `json:"point"`
}

type workspaceCamera struct {
	Lon     *float64 `json:"longitude"`
	Lat     *float64 `json:"latitude"`
	Zoom    *float64 `json:"zoom"`
	Bearing *float64 `json:"bearing"`
}

type workspaceCheckpoint struct {
	path     string
	digest   [32]byte
	manifest savedWorkspace
}

type workspaceStore struct{ directory string }

func localWorkspaceStore() (workspaceStore, error) {
	path, err := appSettingsPath()
	if err != nil {
		return workspaceStore{}, err
	}
	return workspaceStore{directory: filepath.Join(filepath.Dir(path), "workspaces")}, nil
}

func (s workspaceStore) path(id string) string { return filepath.Join(s.directory, id+".json") }

func validSavedID(id string) bool {
	if len(id) < 8 || len(id) > 80 {
		return false
	}
	for _, c := range id {
		if c != '-' && c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (v savedWorkspace) validate() error {
	if v.Version != workspaceVersion {
		return fmt.Errorf("unsupported workspace version %d; original left unchanged", v.Version)
	}
	validName := strings.TrimSpace(v.Name) != "" && len([]rune(v.Name)) <= 256 && cleanPlaceText(v.Name) == v.Name
	if !validSavedID(v.ID) || !validName || v.Revision == 0 {
		return fmt.Errorf("invalid workspace identity, name or revision")
	}
	if v.Created.IsZero() || v.Modified.Before(v.Created) {
		return fmt.Errorf("invalid workspace timestamps")
	}
	if v.Imports == nil || v.Journeys == nil || len(v.Imports)+len(v.Journeys) > 1024 {
		return fmt.Errorf("invalid workspace contents or too many documents")
	}
	if _, err := v.Camera.camera(); err != nil {
		return err
	}
	ids := map[string]bool{v.ID: true}
	items := map[string]bool{}
	documents := map[string]bool{}
	journeys := map[string]bool{}
	addID := func(id string) error {
		if !validSavedID(id) || ids[id] {
			return fmt.Errorf("invalid or repeated workspace ID")
		}
		ids[id] = true
		return nil
	}
	addItem := func(item savedItem) error {
		if err := addID(item.ID); err != nil {
			return err
		}
		if item.Key < 1 || item.Color < 1 || item.Color > activityColorCount {
			return fmt.Errorf("invalid saved item state")
		}
		items[item.ID] = item.Visible
		return nil
	}
	for _, imported := range v.Imports {
		documents[imported.ID] = true
		if err := addID(imported.ID); err != nil {
			return err
		}
		validPath := imported.Path != "" && len(imported.Path) <= 4096 && !strings.ContainsRune(imported.Path, 0)
		validHash := len(imported.Fingerprint) == 64 && strings.Trim(imported.Fingerprint, "0123456789abcdef") == ""
		if !validPath || !validHash || len(imported.Items) == 0 || len(imported.Items) > 10000 {
			return fmt.Errorf("invalid saved import")
		}
		keys := map[int]bool{}
		for _, item := range imported.Items {
			if err := addItem(item); err != nil {
				return err
			}
			if keys[item.Key] {
				return fmt.Errorf("repeated import item key")
			}
			keys[item.Key] = true
		}
	}
	for _, journey := range v.Journeys {
		journeys[journey.Item.ID] = true
		documents[journey.ID] = true
		if err := addID(journey.ID); err != nil {
			return err
		}
		if err := addItem(journey.Item); err != nil {
			return err
		}
		if journey.Mode != journeyManual && journey.Mode.limitKM() == 0 {
			return fmt.Errorf("invalid journey mode")
		}
		if err := validateJourneyStopCount(len(journey.Stops)); err != nil {
			return fmt.Errorf("invalid journey stop count: %w", err)
		}
		if len(journey.Name) > 1024 || cleanPlaceText(journey.Name) != journey.Name {
			return fmt.Errorf("invalid journey name")
		}
		for _, stop := range journey.Stops {
			if stop == nil {
				continue
			}
			if stop.Lon == nil || stop.Lat == nil || !validLocation(*stop.Lon, *stop.Lat) {
				return fmt.Errorf("invalid saved stop coordinates")
			}
			validText := cleanPlaceText(stop.Name) == stop.Name && cleanPlaceText(stop.Attribution) == stop.Attribution
			validSize := len(stop.Name) <= 4096 && len(stop.Attribution) <= 16384 && len(stop.ProviderID) <= 4096
			if !stop.Source.valid() || !validText || !validSize {
				return fmt.Errorf("invalid stop source or text")
			}
		}
	}
	if len(v.Order) != len(documents) {
		return fmt.Errorf("invalid workspace document order")
	}
	for _, id := range v.Order {
		if !documents[id] {
			return fmt.Errorf("unknown or repeated document in workspace order")
		}
		delete(documents, id)
	}
	if v.Planner != "" && !journeys[v.Planner] {
		return fmt.Errorf("invalid saved planner journey")
	}
	if v.Selection != nil {
		if !items[v.Selection.Item] || v.Selection.Point < -1 {
			return fmt.Errorf("invalid saved selection")
		}
	}
	return nil
}

func (v workspaceCamera) camera() (Camera, error) {
	if v.Lon == nil || v.Lat == nil || v.Zoom == nil || v.Bearing == nil {
		return Camera{}, fmt.Errorf("workspace camera is incomplete")
	}
	c, err := viewport(nil, Args{Lon: v.Lon, Lat: v.Lat, Zoom: v.Zoom, Bearing: v.Bearing})
	if err != nil {
		return Camera{}, fmt.Errorf("invalid workspace camera: %w", err)
	}
	return c, nil
}

func readWorkspace(path string) (*workspaceCheckpoint, error) {
	return readWorkspaceContext(context.Background(), path)
}

func readWorkspaceContext(ctx context.Context, path string) (*workspaceCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	file, err := openRegularFile(path)
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(contextTrackFile{File: file, ctx: ctx}, workspaceFileLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read workspace: %w", err)
	}
	if len(data) > workspaceFileLimit {
		return nil, fmt.Errorf("workspace exceeds 4 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var v savedWorkspace
	if err := decoder.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode workspace: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("unexpected data after workspace")
	}
	if err := v.validate(); err != nil {
		return nil, err
	}
	return &workspaceCheckpoint{path: path, digest: sha256.Sum256(data), manifest: v}, nil
}

// save checks the complete previous bytes as well as identity/revision, so external
// edits that do not increment the revision are conflicts too. Saves are explicit.
func (s workspaceStore) save(v savedWorkspace, expected *workspaceCheckpoint) (*workspaceCheckpoint, error) {
	path := s.path(v.ID)
	if expected != nil {
		path = expected.path
	}
	if err := v.validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode workspace: %w", err)
	}
	data = append(data, '\n')
	if len(data) > workspaceFileLimit {
		return nil, fmt.Errorf("workspace exceeds 4 MiB")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create workspace directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open workspace lock: %w", err)
	}
	defer lock.Close()
	if err := lockLocalFile(lock); err != nil {
		return nil, fmt.Errorf("workspace is busy; try again: %w", err)
	}
	defer unlockLocalFile(lock)
	current, readErr := readWorkspace(path)
	if expected == nil {
		if !errors.Is(readErr, os.ErrNotExist) {
			return nil, fmt.Errorf("workspace already exists or cannot be read; use Save As")
		}
	} else {
		if readErr != nil {
			return nil, fmt.Errorf("%w or is unavailable; reload or Save As: %w", errWorkspaceConflict, readErr)
		}
		sameFile := current.digest == expected.digest && current.manifest.ID == v.ID
		matches := sameFile && current.manifest.Revision+1 == v.Revision
		if !matches {
			return nil, fmt.Errorf("%w in another viewer or editor; reload or Save As", errWorkspaceConflict)
		}
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".workspace-*.json")
	if err != nil {
		return nil, fmt.Errorf("create workspace file: %w", err)
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return nil, fmt.Errorf("write workspace: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return nil, fmt.Errorf("replace workspace: %w", err)
	}
	return &workspaceCheckpoint{path: path, digest: sha256.Sum256(data), manifest: v}, nil
}

func newSavedWorkspace(name string) savedWorkspace {
	now := time.Now().UTC()
	return savedWorkspace{Version: workspaceVersion, ID: rand.Text(), Name: name, Revision: 1, Created: now, Modified: now,
		Imports: []savedImport{}, Journeys: []savedJourney{}, Order: []string{}}
}
