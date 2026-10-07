package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func runLoadCommand(m *viewer, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, next := range msg {
			runLoadCommand(m, next)
		}
	case loaded:
		_, next := m.Update(msg)
		runLoadCommand(m, next)
	case sceneMsg:
		_, next := m.Update(msg)
		runLoadCommand(m, next)
	}
}

func TestViewerLoadsMultipleFilesAndFitsOnlyVisibleItems(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	m := &viewer{provider: newProvider("none"), ctx: context.Background(), width: 70, height: 24, camera: fit(nil, 68, 15, 2), args: Args{NoAnimation: true}, showProfile: true}
	m.initUI()
	first := importFile(t, "first.gpx", []byte(`<gpx><rte><name>First</name><rtept lat="51" lon="0"/><rtept lat="51" lon="0.01"/></rte></gpx>`))
	second := importFile(t, "second.gpx", []byte(`<gpx><rte><name>Second</name><rtept lat="51" lon="10"/><rtept lat="51" lon="10.01"/></rte></gpx>`))
	runLoadCommand(m, m.load(first))
	firstRef := m.workspace.selection.ref
	runLoadCommand(m, m.load(second))
	if m.fatal != nil || len(m.workspace.documents) != 2 || m.activeTrack().Name != "Second" || m.following {
		t.Fatal("adding a second file failed or replaced the first", m.fatal)
	}
	if m.camera != fit(m.activeTrack(), m.camera.Width, m.camera.Height, m.camera.Aspect) {
		t.Fatal("newly loaded item was not fitted")
	}
	m.Update(tea.KeyPressMsg{Code: 'f'})
	if m.camera != m.workspace.fitVisible(m.camera.Width, m.camera.Height, m.camera.Aspect) {
		t.Fatal("f did not fit all visible items")
	}
	if err := m.workspace.setVisible(firstRef, false); err != nil {
		t.Fatal(err)
	}
	m.Update(tea.KeyPressMsg{Code: 'f'})
	if m.camera != fit(m.activeTrack(), m.camera.Width, m.camera.Height, m.camera.Aspect) {
		t.Fatal("f still included hidden items")
	}
	if err := os.WriteFile(second, []byte("invalid now"), 0600); err != nil {
		t.Fatal(err)
	}
	runLoadCommand(m, m.load(second))
	if len(m.workspace.documents) != 2 || m.ui.dialog != dialogNone || m.activeTrack().Name != "Second" {
		t.Fatal("reopening a loaded file reread or duplicated it")
	}
}

func TestLoadedFilesDialogShowsOwnershipAndClearsRemovedActiveState(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	m := &viewer{provider: newProvider("none"), ctx: context.Background(), width: 120, height: 40, camera: fit(nil, 88, 34, 2), showProfile: true}
	m.initUI()
	path := importFile(t, "my-courses.tcx", []byte(coursesWithGapTCX))
	runLoadCommand(m, m.load(path))
	m.activePlayback().toggle()
	m.openDialog(dialogWorkspace)
	m.rebuild()
	text := ansi.Strip(m.content)
	for _, label := range []string{"Loaded files", "my-courses.tcx", "Outward", "Return", "remove file"} {
		if !strings.Contains(text, label) {
			t.Fatal("loaded-files dialog omitted ownership or controls", label)
		}
	}
	before := m.camera
	m.Update(tea.KeyPressMsg{Code: ' '})
	if m.activeTrack() != nil || m.activePlayback() != nil || m.activeProfile() != nil || m.camera.Center != before.Center || len(m.workspace.sceneItems()) != 1 {
		t.Fatal("hiding the active item kept stale state or moved the camera")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.activeTrack() == nil || m.activePlayback().Playing || len(m.workspace.sceneItems()) != 2 {
		t.Fatal("selecting the hidden item did not reveal it paused")
	}
	m.openDialog(dialogWorkspace)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
	if m.activeTrack() != nil || len(m.workspace.items()) != 1 {
		t.Fatal("removing one item removed its siblings or selected one automatically")
	}
	m.Update(tea.KeyPressMsg{Code: 'D', Text: "D"})
	if len(m.workspace.documents) != 0 || m.activeProfile() != nil {
		t.Fatal("removing the file retained its items or profile")
	}
}

func TestViewerFreeRoamKeepsFilesAndClearWorkspaceRejectsStaleLoads(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	m := &viewer{provider: newProvider("none"), ctx: context.Background(), width: 120, height: 40, camera: fit(nil, 88, 34, 2)}
	m.initUI()
	runLoadCommand(m, m.load("../fixtures/example.gpx"))
	before := m.camera
	m.runCommand("roam")
	if m.activeTrack() != nil || len(m.workspace.items()) != 4 || m.camera.Center != before.Center {
		t.Fatal("free roaming discarded loaded geometry")
	}
	staleID := m.loadID
	d, err := loadDocument("../fixtures/waypoints.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m.runCommand("clear")
	m.Update(loaded{id: staleID, document: d})
	if len(m.workspace.documents) != 0 || m.activeTrack() != nil {
		t.Fatal("a stale load restored a cleared workspace")
	}
}
