package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWorkspaceStoreRenameDuplicateAndDelete(t *testing.T) {
	var w Workspace
	source := importFile(t, "ride.gpx", []byte(`<gpx><wpt lat="51" lon="0"/></gpx>`))
	openWorkspaceFile(t, &w, source)
	store := workspaceStore{directory: t.TempDir()}
	cp := saveTestWorkspace(t, &w, store, false)
	renamed, err := store.rename(cp, "Café / 東京")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.path != cp.path || renamed.manifest.ID != cp.manifest.ID || renamed.manifest.Revision != 2 {
		t.Fatal("rename changed identity or failed to advance revision")
	}
	before, err := workspaceContentDigest(cp.manifest)
	if err != nil {
		t.Fatal(err)
	}
	after, err := workspaceContentDigest(renamed.manifest)
	if err != nil || before != after {
		t.Fatal("rename changed saved content", err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	copy, err := store.duplicate(renamed, renamed.manifest.Name)
	if err != nil {
		t.Fatal("duplicate should preserve missing imports without loading them", err)
	}
	if copy.manifest.ID == renamed.manifest.ID || copy.manifest.Revision != 1 {
		t.Fatal("duplicate reused checkpoint identity")
	}
	if !reflect.DeepEqual(copy.manifest.Imports, renamed.manifest.Imports) {
		t.Fatal("duplicate changed missing import state")
	}
	if err := store.delete(renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renamed.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("delete left checkpoint behind", err)
	}
	if _, err := readWorkspace(copy.path); err != nil {
		t.Fatal("delete removed unrelated save", err)
	}
	if _, err := store.rename(renamed, "Resurrected"); !errors.Is(err, errWorkspaceConflict) {
		t.Fatal("rename resurrected deleted checkpoint", err)
	}
}

func TestWorkspaceStoreDuplicateResolvesRelativeReferences(t *testing.T) {
	var w Workspace
	openWorkspaceFile(t, &w, "../fixtures/example.gpx")
	sourceStore := workspaceStore{directory: t.TempDir()}
	cp := saveTestWorkspace(t, &w, sourceStore, false)
	v := cp.manifest
	v.Imports[0].Path = "missing.gpx"
	v.Revision++
	cp, err := sourceStore.save(v, cp)
	if err != nil {
		t.Fatal(err)
	}
	destination := workspaceStore{directory: t.TempDir()}
	copy, err := destination.duplicate(cp, "Copy")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(sourceStore.directory, "missing.gpx")
	if copy.manifest.Imports[0].Path != want || cp.manifest.Imports[0].Path != "missing.gpx" {
		t.Fatal("copy redirected a relative import or mutated the source")
	}
}

func TestWorkspaceStoreManagementRejectsInvalidNamesAndExternalEdits(t *testing.T) {
	for _, test := range []struct{ name, value string }{
		{name: "blank", value: "  "},
		{name: "long", value: strings.Repeat("界", 257)},
		{name: "control", value: "bad\x1bname"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var w Workspace
			store := workspaceStore{directory: t.TempDir()}
			cp := saveTestWorkspace(t, &w, store, false)
			if _, err := store.rename(cp, test.value); err == nil {
				t.Fatal("accepted invalid rename")
			}
			if _, err := store.duplicate(cp, test.value); err == nil {
				t.Fatal("accepted invalid duplicate name")
			}
			if err := checkWorkspace(cp); err != nil {
				t.Fatal("invalid name changed original", err)
			}
		})
	}
	for _, action := range []string{"rename", "duplicate", "delete"} {
		t.Run(action, func(t *testing.T) {
			var w Workspace
			store := workspaceStore{directory: t.TempDir()}
			cp := saveTestWorkspace(t, &w, store, false)
			data, err := os.ReadFile(cp.path)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, ' ')
			if err := os.WriteFile(cp.path, data, 0600); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "rename":
				_, err = store.rename(cp, "Renamed")
			case "duplicate":
				_, err = store.duplicate(cp, "Copied")
			case "delete":
				err = store.delete(cp)
			}
			if !errors.Is(err, errWorkspaceConflict) {
				t.Fatal("external edit was not detected", err)
			}
			after, err := os.ReadFile(cp.path)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatal("external edit was lost", err)
			}
		})
	}
}

func TestWorkspaceStoreConcurrentSaveRenameDelete(t *testing.T) {
	for _, pair := range []struct{ first, second string }{
		{first: "save", second: "rename"},
		{first: "save", second: "delete"},
		{first: "rename", second: "delete"},
	} {
		t.Run(pair.first+"-"+pair.second, func(t *testing.T) {
			var w Workspace
			store := workspaceStore{directory: t.TempDir()}
			cp := saveTestWorkspace(t, &w, store, false)
			v := cp.manifest
			v.Revision++
			start := make(chan struct{})
			results := make(chan error, 2)
			for _, action := range []string{pair.first, pair.second} {
				go func() {
					<-start
					var err error
					switch action {
					case "save":
						_, err = store.save(v, cp)
					case "rename":
						_, err = store.rename(cp, "Renamed")
					case "delete":
						err = store.delete(cp)
					}
					results <- err
				}()
			}
			close(start)
			successes := 0
			for range 2 {
				if <-results == nil {
					successes++
				}
			}
			if successes != 1 {
				t.Fatalf("expected one winning mutation, got %d", successes)
			}
		})
	}
}
