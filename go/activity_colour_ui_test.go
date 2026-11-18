package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestActivityColourPreviewAndSave(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	m.workspace.clear()
	active := openWorkspaceFile(t, &m.workspace, "../fixtures/country-ride.gpx")
	other := openWorkspaceFile(t, &m.workspace, "../fixtures/waypoints.gpx")
	if err := m.workspace.selectItem(active); err != nil {
		t.Fatal(err)
	}
	if err := m.workspace.seek(len(m.activeTrack().Points) / 2); err != nil {
		t.Fatal(err)
	}
	m.layout()
	selection := m.workspace.selection
	playback := selection.playback
	elapsed, playing := playback.Elapsed, playback.Playing
	camera := m.camera
	revision := m.workspace.liveRevision
	original := m.workspace.items()
	m.openDialog(dialogWorkspace)

	open := func() { m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"}) }
	open()
	if m.ui.dialog != dialogActivityColour || m.ui.selected != 0 {
		t.Fatal("picker did not open on the current slot")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.rebuild()
	if m.activeSwatch() != 6 || m.visibleProfile().Items[0].Color != 6 {
		t.Fatal("map cursor or profile kept the original slot")
	}
	if m.progress.FullColor != lipgloss.Color(m.theme().Activities[5]) || !strings.Contains(ansi.Strip(m.content), "█") {
		t.Fatal("preview did not reach the displayed playback progress")
	}
	if m.workspace.items()[0].Color != original[0].Color || m.workspace.liveRevision != revision {
		t.Fatal("preview changed the working copy")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogWorkspace || m.activeSwatch() != 1 || m.visibleProfile().Items[0].Color != 1 {
		t.Fatal("Escape did not restore the Files row and its original colour")
	}

	open()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.ui.dialog != dialogWorkspace || m.activeSwatch() != 3 || m.workspace.liveRevision != revision+1 {
		t.Fatal("Enter did not confirm the slot once")
	}
	items := m.workspace.items()
	if items[1].Ref != other || items[1].Color != original[1].Color || items[0].Visible != original[0].Visible {
		t.Fatal("assignment changed another item or visibility")
	}
	if m.workspace.selection != selection || selection.playback != playback || m.camera != camera {
		t.Fatal("assignment replaced the selection, playback or camera")
	}
	if playback.Elapsed != elapsed || playback.Playing != playing {
		t.Fatal("assignment changed the selection, playback or camera")
	}
	open()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.workspace.liveRevision != revision+1 {
		t.Fatal("confirming the current slot advanced the revision")
	}
	m.applyTheme((m.settings.Theme + 1) % len(m.themes))
	if m.activeColor() != m.theme().Activities[2] {
		t.Fatal("confirmed slot did not follow the new theme")
	}
	checkpoint := saveTestWorkspace(t, &m.workspace, workspaceStore{directory: t.TempDir()}, false)
	reopened, _, err := prepareWorkspace(checkpoint.path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.items()[0].Color != 3 || reopened.items()[1].Color != original[1].Color {
		t.Fatal("reopening the workspace lost the chosen slot")
	}
}

func TestActivityColourDismissalAndHiddenTarget(t *testing.T) {
	m := themeViewer(t, 30, 12, true)
	m.workspace.clear()
	active := openWorkspaceFile(t, &m.workspace, "../fixtures/country-ride.gpx")
	hidden := openWorkspaceFile(t, &m.workspace, "../fixtures/waypoints.gpx")
	if err := m.workspace.setVisible(hidden, false); err != nil {
		t.Fatal(err)
	}
	if err := m.workspace.selectItem(active); err != nil {
		t.Fatal(err)
	}
	m.layout()
	m.openDialog(dialogWorkspace)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.rebuild()
	screen := ansi.Strip(m.content)
	if !strings.Contains(screen, ">3#") || !strings.Contains(screen, "6#") || !strings.Contains(screen, "enter keep") {
		t.Fatal("picker clipped its choices or controls at the minimum terminal size")
	}
	if m.activeSwatch() != 1 || len(m.workspace.sceneItems()) != 1 {
		t.Fatal("preview selected or revealed the hidden activity")
	}
	m.pushDialog(dialogCommands)
	m.backDialog()
	if m.ui.dialog != dialogWorkspace || m.ui.selected != 1 || m.workspace.items()[1].Color != 2 {
		t.Fatal("replacing the picker did not discard the preview and return to Files")
	}

	m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if err := m.workspace.removeItem(hidden); err != nil {
		t.Fatal(err)
	}
	revision := m.workspace.liveRevision
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, body, _ := m.activityColourContent(22, 2)
	if !strings.Contains(body, "no longer loaded") || m.workspace.liveRevision != revision {
		t.Fatal("confirming a removed target changed the workspace")
	}
}
