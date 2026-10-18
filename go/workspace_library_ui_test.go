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
	if preview != nil {
		t.Fatal("listing started geometry loading")
	}
	if m.savedWork.entries[0].path != second.path {
		t.Fatal("newest save not selected")
	}
	for range 20 {
		if cmd := m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyDown}); cmd != nil {
			t.Fatal("picker navigation started background work")
		}
	}
	m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.savedWork.candidate != nil {
		t.Fatal("picker navigation loaded geometry")
	}
	if m.savedWork.mode != "library" || m.savedWork.entries[m.savedWork.selected].path != first.path {
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
	m.Update(list())
	open := m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if open == nil {
		t.Fatal("Enter did not prepare saved content")
	}
	if m.workspace.selection.ref != original {
		t.Fatal("open replaced content before candidate validation")
	}
	if duplicate := m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEnter}); duplicate != nil {
		t.Fatal("repeated Enter started duplicate loading")
	}
	m.Update(open())
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

func TestWorkspaceLibraryDefersImportChecksAndIgnoresCancelledOpen(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	source := importFile(t, "original.gpx", []byte(`<gpx><wpt lat="51" lon="0"/></gpx>`))
	openWorkspaceFile(t, &m.workspace, source)
	store, err := localWorkspaceStore()
	if err != nil {
		t.Fatal(err)
	}
	cp := saveTestWorkspace(t, &m.workspace, store, true)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	list := m.openSavedWorkspaceDialog("open")
	_, cmd := m.Update(list())
	if cmd != nil || m.savedWork.candidate != nil {
		t.Fatal("metadata browsing loaded imports")
	}
	preview := m.savedWork.entries[0].preview.pickerDetails(60, false)
	if !strings.Contains(preview, "original.gpx") || !strings.Contains(preview, "checked when opening") || strings.Contains(preview, "missing/changed") {
		t.Fatal("preview claimed to validate import contents", preview)
	}
	original := m.workspace.selection.ref
	open := m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	ready := open()
	m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(ready)
	if m.workspace.selection.ref != original {
		t.Fatal("cancelled open replaced current workspace")
	}
	list = m.openSavedWorkspaceDialog("open")
	m.Update(list())
	open = m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(open())
	if m.savedWork.mode != "repair" || len(m.workspace.unresolved) != 1 || m.workspace.checkpoint.path != cp.path {
		t.Fatal("opening did not validate missing import")
	}
}
