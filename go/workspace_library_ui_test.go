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
	selectedPath := m.savedWork.entries[m.savedWork.selected].path
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
	savedKey(m, 'd')
	if m.workspace.checkpoint.path != selectedPath || m.workspace.selection.ref == original {
		t.Fatal("Enter did not open the previewed workspace directly")
	}
}

func TestWorkspaceLibraryRefreshPreservesSelectionAndDetailScroll(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	store, err := localWorkspaceStore()
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		saveTestWorkspace(t, &m.workspace, store, true)
	}
	list := m.openSavedWorkspaceDialog("open")
	m.Update(list())
	m.selectLibraryWorkspace(2)
	m.workspaceLibraryContent(48, 8)
	m.ui.page.GotoBottom()
	offset, anchor := m.ui.page.YOffset(), m.savedWork.anchor
	if offset == 0 {
		t.Fatal("fixture has no scrollable details")
	}
	refresh := m.loadWorkspaceLibrary()
	_, body, _ := m.workspaceLibraryContent(48, 8)
	if m.savedWork.anchor != anchor || len(m.savedWork.entries) != 4 ||
		!strings.Contains(body, "Reading saved workspaces") || !strings.Contains(body, "Saved workspaces (3/4)") {
		t.Fatal("refresh replaced the selected list with an empty loading screen", body)
	}
	saveTestWorkspace(t, &m.workspace, store, true)
	m.Update(refresh())
	m.workspaceLibraryContent(48, 8)
	if m.savedWork.anchor != anchor || m.ui.page.YOffset() != offset {
		t.Fatal("refresh lost selected identity or detail scroll", m.savedWork.anchor, m.ui.page.YOffset())
	}
	_, body, _ = m.workspaceLibraryContent(48, 2)
	if !strings.Contains(body, "> ") {
		t.Fatal("compact picker hid the selected entry", body)
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

func TestWorkspaceLibrarySearchDoesNotCancelPendingListing(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	if _, err := m.saveWorkspaceCheckpoint("Saved workspace", false); err != nil {
		t.Fatal(err)
	}
	list := m.openSavedWorkspaceDialog("open")
	m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: '/'})
	m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: 'o'})
	m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: 'm'})
	m.Update(list())
	if m.savedWork.busy || len(m.savedWork.entries) != 1 || m.savedWork.mode != "library" {
		t.Fatal("early search or sort stranded a pending listing")
	}
	m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: '/'})
	if m.savedWork.mode != "library-search" {
		t.Fatal("search unavailable after loading")
	}
}

func TestWorkspaceLibraryPathAfterSearch(t *testing.T) {
	for _, key := range []string{"p", "tab"} {
		t.Run(key, func(t *testing.T) {
			m := themeViewer(t, 70, 24, false)
			openTestWorkspaceLibrary(t, m)
			m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: '/'})
			m.Update(tea.PasteMsg{Content: "Search query"})
			savedKey(m, tea.KeyEnter)
			code := rune('p')
			if key == "tab" {
				code = tea.KeyTab
			}
			m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: code})
			if key == "tab" {
				savedKey(m, tea.KeyTab)
			}
			if m.savedWork.mode != "open" || m.ui.search.Value() != "" || !m.ui.search.Focused() {
				t.Fatal("search text leaked into workspace path input")
			}
			path := "/" + strings.Repeat("folder/", 50) + "workspace.json"
			m.Update(tea.PasteMsg{Content: path})
			if m.ui.search.Value() != path {
				t.Fatal("workspace path retained the short search limit")
			}
		})
	}
}

func TestWorkspaceLibraryMarksActiveCheckpointAndOffersActions(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	cp, err := m.saveWorkspaceCheckpoint("Active workspace", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := localWorkspaceStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.duplicate(cp, "Other workspace"); err != nil {
		t.Fatal(err)
	}
	openTestWorkspaceLibrary(t, m)
	_, body, hints := m.savedWorkspaceContent(48, 15)
	if !strings.Contains(body, "Active workspace (active)") || strings.Contains(body, cp.manifest.ID[:8]) {
		t.Fatal("active marker missing or unique save still shows an opaque ID", body)
	}
	if !strings.Contains(hints, "m actions") || strings.Contains(hints, "del delete") {
		t.Fatal("workspace actions still crowd the picker footer", hints)
	}
	m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: 'm'})
	_, body, _ = m.savedWorkspaceContent(48, 15)
	if !strings.Contains(body, "Rename saved workspace") || !strings.Contains(body, "Save current workspace as") {
		t.Fatal("missing readable workspace actions", body)
	}
	savedKey(m, tea.KeyEnter)
	if m.savedWork.mode != "rename" {
		t.Fatal("action menu did not open Rename")
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
	savedKey(m, 'd')
	if m.savedWork.mode != "repair" || len(m.workspace.unresolved) != 1 || m.workspace.checkpoint.path != cp.path {
		t.Fatal("opening did not validate missing import")
	}
}
