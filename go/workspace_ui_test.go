package main

import (
	"context"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	case sourceResult:
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
	if m.activeTrack() != nil || m.activePlayback() != nil || m.visibleProfile() == nil || m.camera.Center != before.Center || len(m.workspace.sceneItems()) != 1 {
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
	if len(m.workspace.documents) != 0 || m.visibleProfile() != nil {
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

func TestViewerMultipleElevationsShareOnlyTheSelectedPlaybackCursor(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	first := importFile(t, "short.gpx", []byte(`<gpx><trk><name>Short</name><trkseg><trkpt lat="51" lon="0"><ele>100</ele><time>2026-01-01T09:00:00Z</time></trkpt><trkpt lat="51" lon="0.01"><ele>120</ele><time>2026-01-01T09:10:00Z</time></trkpt></trkseg></trk></gpx>`))
	second := importFile(t, "long.gpx", []byte(`<gpx><trk><name>Long</name><trkseg><trkpt lat="51.001" lon="0"><ele>10</ele><time>2026-01-01T09:00:00Z</time></trkpt><trkpt lat="51.001" lon="0.02"><ele>20</ele><time>2026-01-01T09:10:00Z</time></trkpt></trkseg></trk></gpx>`))
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}} {
		m := &viewer{provider: newProvider("none"), ctx: context.Background(), width: size[0], height: size[1], camera: fit(nil, size[0], size[1], 2), args: Args{NoAnimation: true}, showProfile: true}
		m.initUI()
		runLoadCommand(m, m.load(first))
		firstRef := m.workspace.selection.ref
		runLoadCommand(m, m.load(second))
		m.Update(tea.KeyPressMsg{Code: 'f'})
		profile := m.visibleProfile()
		if profile == nil || len(profile.Items) != 2 || profile.Min != 10 || profile.Max != 120 {
			t.Fatal("viewer omitted one activity's elevation", size)
		}
		if profile.Distance != profile.Items[0].Track.Distance+profile.Items[1].Track.Distance {
			t.Fatal("viewer did not sum the ordered activities' distances")
		}
		colours := make(map[activityColor]bool)
		for _, cell := range profile.Grid.Cells {
			if cell.Bits != 0 {
				colours[cell.Color] = true
			}
		}
		if len(colours) != 2 {
			t.Fatal("compressed chart hid an entire activity's elevation", size)
		}
		base := slices.Clone(profile.Grid.Cells)
		before := m.camera
		m.Update(tea.KeyPressMsg{Code: ' '})
		m.advancePlayback(m.lastTick.Add(time.Minute))
		pos := m.position()
		if pos.Blend == 0 || pos.Elevation == nil || *pos.Elevation < 10 || *pos.Elevation > 20 || m.camera != before {
			t.Fatal("playback used another activity or moved a free camera", size)
		}
		if m.visibleProfile() != profile || !slices.Equal(profile.Grid.Cells, base) {
			t.Fatal("playback rebuilt or mutated the static elevation profiles", size)
		}
		_, chart, _ := profile.frame(pos)
		g := renderScene(m.camera, nil, m.workspace.sceneItems(), &pos.World)
		chartColumn := int(math.Round((profile.Items[0].Track.Distance+pos.Distance)/profile.Distance*float64(chart.Width*2-1))) / 2
		for gridIndex, grid := range []Grid{g, chart} {
			markers := 0
			for i, cell := range grid.Cells {
				if cell.Text == '@' {
					markers++
					if cell.Color != m.activeSwatch() || cell.Role != roleCursor {
						t.Fatal("playback marker lost activity identity or priority")
					}
					if gridIndex == 1 && i%grid.Width != chartColumn {
						t.Fatal("chart cursor did not include the earlier activity's distance")
					}
				}
			}
			if markers != 1 {
				t.Fatal("map and chart should each have one playback marker", size, markers)
			}
		}
		m.rebuild()
		text := ansi.Strip(m.content)
		legend := ansi.Strip(m.workspaceLegend(size[0], profile))
		if strings.Index(legend, "Short") > strings.Index(legend, "Long") {
			t.Fatal("legend moved the selected activity ahead of load order")
		}
		if !strings.Contains(text, "Short") || !strings.Contains(text, "Long") || !strings.Contains(text, "2:1 Long | Playing") || lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) != size[1] || m.progress.FullColor != lipgloss.Color(m.activeColor()) {
			t.Fatal("multi-activity labels, playback colour or layout failed", size, text)
		}
		oldPlayback := m.activePlayback()
		elapsed := oldPlayback.Elapsed
		m.openDialog(dialogWorkspace)
		m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m.Update(tea.KeyPressMsg{Code: ' '})
		if m.activePlayback() != oldPlayback || !oldPlayback.Playing || oldPlayback.Elapsed != elapsed || len(m.visibleProfile().Items) != 1 || m.visibleProfile().Max != 20 {
			t.Fatal("hiding the other activity interrupted playback or left its elevation visible")
		}
		m.Update(tea.KeyPressMsg{Code: ' '})
		m.closeDialog()
		m.openDialog(dialogWorkspace)
		m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if oldPlayback.Playing || m.workspace.selection.ref != firstRef || m.activePlayback().Playing || m.position().Index != 0 || m.position().Blend != 0 {
			t.Fatal("switching activity did not stop old playback and start paused")
		}
		updated := m.visibleProfile()
		if len(updated.Items) != 2 || updated.Min != profile.Min || updated.Max != profile.Max || updated.Distance != profile.Distance {
			t.Fatal("selecting another activity changed the shared axes")
		}
		updatedLegend := ansi.Strip(m.workspaceLegend(size[0], updated))
		if strings.ReplaceAll(updatedLegend, "*", " ") != strings.ReplaceAll(legend, "*", " ") {
			t.Fatal("selecting another activity reordered the legend")
		}
		m.activePlayback().toggle()
		m.openDialog(dialogWorkspace)
		m.Update(tea.KeyPressMsg{Code: ' '})
		if m.activePlayback() != nil || len(m.visibleProfile().Items) != 1 || m.visibleProfile().Min != 10 || m.visibleProfile().Max != 20 {
			t.Fatal("hiding the playing activity retained its marker or cleared the other elevation")
		}
	}
}
