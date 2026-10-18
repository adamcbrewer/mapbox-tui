package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceLibraryListsFilesAndKeepsUnreadableEntries(t *testing.T) {
	store := workspaceStore{directory: filepath.Join(t.TempDir(), "workspaces")}
	entries, err := store.list()
	if err != nil || len(entries) != 0 {
		t.Fatal("missing directory should be an empty library", err)
	}
	var w Workspace
	first := saveTestWorkspace(t, &w, store, true)
	second := saveTestWorkspace(t, &w, store, true)
	broken := filepath.Join(store.directory, "broken.json")
	if err := os.WriteFile(broken, []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.directory, "ignored.tmp"), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err = store.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].path != second.path || entries[1].path != first.path {
		t.Fatal("saves not ordered by modification time", entries)
	}
	if entries[0].name != first.manifest.Name || entries[2].path != broken || entries[2].problem == "" {
		t.Fatal("lost display names or unreadable save", entries)
	}
	if err := os.Remove(first.path); err != nil {
		t.Fatal(err)
	}
	entries, err = store.list()
	if err != nil || len(entries) != 2 {
		t.Fatal("listing did not reflect deletion", entries, err)
	}
}
