package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFavouriteStoreRoundTripAndConflicts(t *testing.T) {
	t.Parallel()
	store := favouriteStore{path: filepath.Join(t.TempDir(), "favourites.json")}
	home := newFavourite(" Home ", placeLocation{Lon: -0.1, Lat: 51.5, Source: placeSourceCoordinates})
	park := newFavourite("Park", placeLocation{Lon: -0.2, Lat: 51.6, Source: placeSourceMapPoint})
	for _, f := range []favourite{home, park} {
		if err := store.change(nil, &f); err != nil {
			t.Fatal(err)
		}
	}
	otherViewer := favouriteStore{path: store.path}
	entries, err := otherViewer.read()
	if err != nil || len(entries) != 2 || entries[0] != home || entries[1] != park {
		t.Fatalf("restart lost favourites: %v, %v", entries, err)
	}
	renamed := home
	renamed.Name = "Maison"
	if err := otherViewer.change(&home, &renamed); err != nil {
		t.Fatal(err)
	}
	if err := store.change(&home, nil); err == nil {
		t.Fatal("stale delete removed a renamed favourite")
	}
	if err := store.change(&home, &home); err == nil {
		t.Fatal("stale rename overwrote another edit")
	}
	if err := store.change(&park, nil); err != nil {
		t.Fatal(err)
	}
	entries, err = store.read()
	if err != nil || len(entries) != 1 || entries[0] != renamed {
		t.Fatalf("delete changed unrelated entry: %v, %v", entries, err)
	}
	if err := store.change(&renamed, nil); err != nil {
		t.Fatal(err)
	}
	entries, err = store.read()
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty library could not be reloaded: %v, %v", entries, err)
	}
	info, err := os.Stat(store.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("favourites file should be private", err)
	}
}

func TestFavouriteStoreRejectsInvalidEdits(t *testing.T) {
	t.Parallel()
	base := newFavourite("Home", placeLocation{Lon: -0.1, Lat: 51.5, Source: placeSourceCoordinates})
	for _, name := range []string{"blank", "duplicate", "control", "latitude", "NaN", "provider", "unknown source", "attribution"} {
		t.Run(name, func(t *testing.T) {
			store := favouriteStore{path: filepath.Join(t.TempDir(), "favourites.json")}
			if err := store.change(nil, &base); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			invalid := newFavourite("Other", base.location())
			switch name {
			case "blank":
				invalid.Name = " "
			case "duplicate":
				invalid.Name = "home"
			case "control":
				invalid.Name = "Home\x1b[31m"
			case "latitude":
				invalid.Lat = 91
			case "NaN":
				invalid.Lon = math.NaN()
			case "provider":
				invalid.Source = placeSourceMapboxSearch
			case "unknown source":
				invalid.Source = "coordinates "
			case "attribution":
				invalid.Attribution = "Provider credit"
			}
			if err := store.change(nil, &invalid); err == nil {
				t.Fatal("invalid favourite accepted")
			}
			after, err := os.ReadFile(store.path)
			if err != nil || string(after) != string(before) {
				t.Fatal("rejected edit changed existing data", err)
			}
		})
	}
}

func TestFavouriteStoreRejectsUnicodeNameCollisions(t *testing.T) {
	t.Parallel()
	for _, names := range [][2]string{
		{"İstanbul", "istanbul"}, {"istanbul", "İstanbul"},
		{"Σ", "ς"}, {"ς", "Σ"}, {"S", "ſ"}, {"ſ", "S"},
		{"Straße", "STRASSE"}, {"STRASSE", "Straße"},
		{"ﬃ", "FFI"}, {"FFI", "ﬃ"},
	} {
		for _, operation := range []string{"add", "rename"} {
			t.Run(names[0]+"/"+operation, func(t *testing.T) {
				store := favouriteStore{path: filepath.Join(t.TempDir(), "favourites.json")}
				location := placeLocation{Lon: -0.1, Lat: 51.5, Source: placeSourceCoordinates}
				first := newFavourite(names[0], location)
				if err := store.change(nil, &first); err != nil {
					t.Fatal(err)
				}
				candidate := newFavourite(names[1], location)
				var expected *favourite
				if operation == "rename" {
					original := candidate
					original.Name = "Other"
					if err := store.change(nil, &original); err != nil {
						t.Fatal(err)
					}
					expected = &original
				}
				before, err := os.ReadFile(store.path)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.change(expected, &candidate); err == nil {
					t.Fatal("accepted case-equivalent favourite names")
				}
				after, err := os.ReadFile(store.path)
				if err != nil || string(after) != string(before) {
					t.Fatal("rejected name collision changed the library", err)
				}
				if _, err := store.read(); err != nil {
					t.Fatal("name collision made the library unreadable", err)
				}
			})
		}
	}
}

func TestFavouriteStoreRejectsDuplicateGeocodingOrigins(t *testing.T) {
	t.Parallel()
	for _, source := range []placeSource{placeSourceMapboxPermanent, placeSourceMapboxTemporary} {
		t.Run(string(source), func(t *testing.T) {
			store := favouriteStore{path: filepath.Join(t.TempDir(), "favourites.json")}
			location := placeLocation{Lon: -0.1, Lat: 51.5, Source: source, ProviderID: "place-id"}
			first := newFavourite("Home", location)
			if err := store.change(nil, &first); err != nil {
				t.Fatal(err)
			}
			for _, candidateSource := range []placeSource{placeSourceMapboxPermanent, placeSourceMapboxTemporary} {
				location.Source = candidateSource
				candidate := newFavourite("Other name", location)
				if err := store.change(nil, &candidate); err == nil {
					t.Fatalf("accepted duplicate place from %q", candidateSource)
				}
			}
			entries, err := store.read()
			if err != nil || len(entries) != 1 || entries[0] != first {
				t.Fatal("rejected duplicate changed the library", entries, err)
			}
		})
	}
}

func TestFavouriteStorePreservesBrokenFiles(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, data string }{
		{name: "truncated", data: `{"version":1,"favourites":[`},
		{name: "new version", data: `{"version":3,"favourites":[]}`},
		{name: "null", data: `{"version":1,"favourites":null}`},
		{name: "case-folded duplicates", data: `{"version":2,"favourites":[{"id":"x","name":"Σ","latitude":51,"longitude":0,"source":"coordinates"},{"id":"y","name":"ς","latitude":51,"longitude":0,"source":"coordinates"}]}`},
		{name: "multi-rune case-folded duplicates", data: `{"version":2,"favourites":[{"id":"x","name":"Straße","latitude":51,"longitude":0,"source":"coordinates"},{"id":"y","name":"STRASSE","latitude":51,"longitude":0,"source":"coordinates"}]}`},
		{name: "missing longitude", data: `{"version":1,"favourites":[{"id":"x","name":"Home","latitude":51,"source":"coordinates"}]}`},
		{name: "invalid location", data: `{"version":1,"favourites":[{"id":"x","name":"Home","latitude":100,"source":"coordinates"}]}`},
		{name: "unknown source", data: `{"version":2,"favourites":[{"id":"x","name":"Home","latitude":51,"longitude":0,"source":"mapbox"}]}`},
		{name: "oversized", data: strings.Repeat(" ", (1<<20)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := favouriteStore{path: filepath.Join(t.TempDir(), "favourites.json")}
			if err := os.WriteFile(store.path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			f := newFavourite("Home", placeLocation{Source: placeSourceCoordinates})
			if err := store.change(nil, &f); err == nil {
				t.Fatal("broken file overwritten")
			}
			data, err := os.ReadFile(store.path)
			if err != nil || string(data) != test.data {
				t.Fatal("broken file was not preserved", err)
			}
		})
	}
}

func TestFavouriteStoreBusyAndFailedWrite(t *testing.T) {
	t.Parallel()
	store := favouriteStore{path: filepath.Join(t.TempDir(), "favourites.json")}
	f := newFavourite("Home", placeLocation{Source: placeSourceCoordinates})
	if err := store.change(nil, &f); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(store.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockLocalFile(lock); err != nil {
		t.Fatal(err)
	}
	if err := store.change(&f, nil); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("concurrent writer was not blocked", err)
	}
	if err := unlockLocalFile(lock); err != nil {
		t.Fatal(err)
	}
	// A read-only directory permits the lock/read, but not the temporary write.
	directory := filepath.Dir(store.path)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(directory, 0700)
	if os.Geteuid() != 0 {
		if err := store.change(&f, nil); err == nil {
			t.Fatal("write unexpectedly succeeded in a read-only directory")
		}
	}
	entries, err := store.read()
	if err != nil || len(entries) != 1 || entries[0] != f {
		t.Fatal("failed writes lost saved data", entries, err)
	}
}

func TestFavouriteStoreMigratesAttributionFromVersionOne(t *testing.T) {
	t.Parallel()
	store := favouriteStore{path: filepath.Join(t.TempDir(), "favourites.json")}
	legacy := `{"version":1,"favourites":[{"id":"local-id","name":"Village","longitude":-2.54,"latitude":51.31,"source":"Mapbox Geocoding (permanent)","provider_id":"mapbox-place-id","attribution":"NOTICE: © 2026 Mapbox and its suppliers. All rights reserved."}]}`
	if err := os.WriteFile(store.path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := store.read()
	if err != nil || len(entries) != 1 {
		t.Fatal("version 1 favourites no longer load", entries, err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil || string(before) != legacy {
		t.Fatal("reading favourites rewrote the file", err)
	}
	renamed := entries[0]
	renamed.Name = "Home"
	if err := store.change(&entries[0], &renamed); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Version    int              `json:"version"`
		Favourites []map[string]any `json:"favourites"`
	}
	if err := json.Unmarshal(data, &saved); err != nil || saved.Version != 2 || len(saved.Favourites) != 1 {
		t.Fatal("migration did not write version 2", string(data), err)
	}
	entry := saved.Favourites[0]
	if _, exists := entry["attribution"]; exists {
		t.Fatal("attribution is still persisted per favourite")
	}
	if entry["id"] != "local-id" || entry["provider_id"] != "mapbox-place-id" || entry["name"] != "Home" {
		t.Fatal("migration lost the favourite's identity", entry)
	}
	reloaded, err := store.read()
	if err != nil || len(reloaded) != 1 || reloaded[0] != renamed {
		t.Fatal("version 2 did not preserve the renamed favourite", reloaded, err)
	}
	if reloaded[0].location().Attribution != "© Mapbox" {
		t.Fatal("offline favourite lost its display credit")
	}
	if err := store.change(&renamed, nil); err != nil {
		t.Fatal("migration broke subsequent deletion", err)
	}
}

func TestFavouriteSourceCompatibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		source   placeSource
		wire     string
		storable bool
	}{
		{source: placeSourceCoordinates, wire: "coordinates", storable: true},
		{source: placeSourceMapCentre, wire: "map centre", storable: true},
		{source: placeSourceMapPoint, wire: "map point", storable: true},
		{source: placeSourceMapboxSearch, wire: "Mapbox Search Box", storable: false},
		{source: placeSourceMapboxPermanent, wire: "Mapbox Geocoding (permanent)", storable: true},
		{source: placeSourceMapboxTemporary, wire: "Mapbox Geocoding (temporary)", storable: true},
	} {
		t.Run(test.wire, func(t *testing.T) {
			store := favouriteStore{path: filepath.Join(t.TempDir(), "favourites.json")}
			location := placeLocation{Source: test.source, Lat: 51, Lon: -2}
			if !test.source.userEntered() {
				location.ProviderID = "place-id"
				location.Attribution = "© Mapbox"
			}
			entry := newFavourite("Home", location)
			data, err := json.Marshal(entry)
			if err != nil || !strings.Contains(string(data), `"source":"`+test.wire+`"`) {
				t.Fatal("source JSON changed without a migration", string(data), err)
			}
			err = store.change(nil, &entry)
			if !test.storable {
				if err == nil {
					t.Fatal("unresolved search source accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			entries, err := store.read()
			if err != nil || len(entries) != 1 || entries[0] != entry {
				t.Fatal("accepted source no longer round-trips", entries, err)
			}
		})
	}
}
