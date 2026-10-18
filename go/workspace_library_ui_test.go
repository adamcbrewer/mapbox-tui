package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestWorkspaceLibraryPreviewSwitchCancelAndOpen(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	store, err := localWorkspaceStore()
	if err != nil {
		t.Fatal(err)
	}
	first := saveTestWorkspace(t, &m.workspace, store, true)
	second := saveTestWorkspace(t, &m.workspace, store, true)
	original := m.workspace.selection.ref
	list := m.openSavedWorkspaceDialog("open")
	_, preview := m.Update(list())
	late := preview()
	if m.savedWork.entries[0].path != second.path {
		t.Fatal("newest save not selected")
	}
	next := m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(late)
	if m.savedWork.candidate != nil {
		t.Fatal("stale preview replaced selected save")
	}
	m.Update(next())
	if m.savedWork.mode != "library" || m.savedWork.candidate.checkpoint.path != first.path {
		t.Fatal("preview navigated away from combined picker")
	}
	_, body, _ := m.savedWorkspaceContent(48, 15)
	for _, want := range []string{"Saved workspaces (2/2)", "Created:", "Modified:", "Imports:", "example.gpx"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
	if m.workspace.selection.ref != original {
		t.Fatal("preview replaced active workspace")
	}
	m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.workspace.selection.ref != original {
		t.Fatal("cancel replaced active workspace")
	}
	list = m.openSavedWorkspaceDialog("open")
	_, preview = m.Update(list())
	m.Update(preview())
	m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.workspace.checkpoint.path != second.path || m.workspace.selection.ref == original {
		t.Fatal("Enter did not open the previewed workspace directly")
	}
}

func TestWorkspaceLibraryEmptyInvalidAndAlternatePath(t *testing.T) {
	m := themeViewer(t, 70, 24, true)
	list := m.openSavedWorkspaceDialog("open")
	m.Update(list())
	_, body, _ := m.savedWorkspaceContent(48, 15)
	if !strings.Contains(body, "No saved workspaces") {
		t.Fatal(body)
	}
	store, err := localWorkspaceStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.directory, "bad.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	refresh := m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: 'r'})
	m.Update(refresh())
	_, body, _ = m.savedWorkspaceContent(48, 15)
	if !strings.Contains(body, "bad.json (unavailable)") || !strings.Contains(body, "Cannot open") {
		t.Fatal(body)
	}
	m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.ui.dialog != dialogSavedWorkspace {
		t.Fatal("invalid save opened")
	}
	pending := m.loadWorkspaceLibrary()
	m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: 'p'})
	m.Update(pending())
	if m.savedWork.mode != "open" {
		t.Fatal("late listing replaced path input")
	}
}
