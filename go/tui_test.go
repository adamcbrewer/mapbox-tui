package main

import (
	"math"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func testWorkspace(track *Track) Workspace {
	var w Workspace
	w.addDocument(&Document{
		Path:   "test.gpx",
		Name:   track.Name,
		Bounds: track.Bounds,
		Items:  []*DocumentItem{{ID: 1, Kind: "track", Track: track}},
	})
	return w
}

func TestDragMovesOneCellWithoutFirstMotionJumpAndStopsOnRelease(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	c := fit(track, 88, 34, 2)
	m := &viewer{workspace: testWorkspace(track), camera: c}
	m.Update(tea.MouseClickMsg{X: 20, Y: 10, Button: tea.MouseLeft})
	if m.camera.Center != c.Center {
		t.Fatal("press moved map")
	}
	want := c
	want.pan(2, 0)
	m.Update(tea.MouseMotionMsg{X: 21, Y: 10, Button: tea.MouseLeft})
	if math.Hypot(m.camera.Center.X-want.Center.X, m.camera.Center.Y-want.Center.Y) > 1e-12 {
		t.Fatal("first motion jumped")
	}
	m.Update(tea.MouseReleaseMsg{X: 21, Y: 10, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 40, Y: 20, Button: tea.MouseLeft})
	if m.camera.Center != want.Center {
		t.Fatal("pan continued after release")
	}
}

func TestCommandPaletteOpensFromEveryViewAndReturns(t *testing.T) {
	for _, test := range []struct {
		name      string
		kind      dialogKind
		context   bool
		collapsed bool
		picking   bool
	}{
		{name: "map", kind: dialogNone},
		{name: "context", kind: dialogNone, context: true},
		{name: "files", kind: dialogWorkspace},
		{name: "collapsed files", kind: dialogWorkspace, collapsed: true},
		{name: "journey", kind: dialogJourney},
		{name: "collapsed journey", kind: dialogJourney, collapsed: true},
		{name: "journey map picking", kind: dialogJourney, picking: true},
		{name: "location input", kind: dialogJourneyLocation},
		{name: "collapsed location input", kind: dialogJourneyLocation, collapsed: true},
		{name: "location map picking", kind: dialogJourneyLocation, picking: true},
		{name: "quick search", kind: dialogSearch},
		{name: "file picker", kind: dialogFiles},
		{name: "help", kind: dialogHelp},
		{name: "credits", kind: dialogCredits},
		{name: "playback speed", kind: dialogSpeed},
		{name: "inspection", kind: dialogInspect},
		{name: "themes", kind: dialogThemes},
		{name: "Mapbox settings", kind: dialogMapbox},
		{name: "Mapbox help", kind: dialogMapboxHelp},
		{name: "travel mode", kind: dialogJourneyRouting},
		{name: "routing help", kind: dialogJourneyRoutingHelp},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := journeyViewer(t)
			if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
				t.Fatal(err)
			}
			m.layout()
			if test.kind >= dialogJourney {
				m.openJourney()
			}
			if test.kind == dialogJourneyLocation {
				m.editJourneyLocation(0)
			} else {
				m.openDialog(test.kind)
			}
			if test.kind == dialogThemes {
				m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			input := m.placeSearchOpen()
			if input {
				m.Update(tea.PasteMsg{Content: "51.31, -2.58" + strings.Repeat(" ", 85)})
				m.ui.search.SetCursor(2)
			}
			if test.kind == dialogFiles {
				m.ui.picker.CurrentDirectory = t.TempDir()
			}
			m.ui.detailsOpen = test.context
			m.ui.filesCollapsed = test.collapsed && test.kind == dialogWorkspace
			m.planner.collapsed = test.collapsed && m.journeyPanelOpen()
			m.planner.pickingStop = test.picking
			if test.collapsed || test.picking {
				m.ui.search.Blur()
			}
			m.ui.notice = "Previous view notice"
			m.rebuild()
			m.ui.page.GotoBottom()
			camera, selection, playback := m.camera, m.workspace.selection, *m.activePlayback()
			selected, parents := m.ui.selected, len(m.ui.parents)
			theme := m.settings.Theme
			query, cursor := m.ui.search.Value(), m.ui.search.Position()
			limit, placeholder := m.ui.search.CharLimit, m.ui.search.Placeholder
			directory, scroll := m.ui.picker.CurrentDirectory, m.ui.page.YOffset()
			m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
			m.rebuild()
			if m.ui.dialog != dialogCommands || !m.ui.search.Focused() {
				t.Fatal("Ctrl-K did not open and focus the command palette")
			}
			if !strings.Contains(ansi.Strip(m.content), "Type a command") {
				t.Fatal("command palette was not visible")
			}
			m.Update(tea.PasteMsg{Content: "colour"})
			if m.ui.search.Value() != "colour" {
				t.Fatal("palette input retained the previous view's query")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			previousViewChanged := m.ui.dialog != test.kind ||
				len(m.ui.parents) != parents || m.ui.selected != selected
			if previousViewChanged {
				t.Fatal("Escape did not return to the previous view and selection")
			}
			if m.settings.Theme != theme {
				t.Fatal("Escape lost the highlighted theme preview")
			}
			if test.kind != dialogNone && (m.ui.notice != "Previous view notice" || m.ui.page.YOffset() != scroll) {
				t.Fatal("Escape lost the previous view's notice or scroll position")
			}
			if input {
				if m.ui.search.Value() != query || m.ui.search.Position() != cursor {
					t.Fatal("Escape lost the previous input and cursor")
				}
				if m.ui.search.CharLimit != limit || m.ui.search.Placeholder != placeholder {
					t.Fatal("previous input retained the palette's text settings")
				}
				wantFocus := !test.collapsed && !test.picking
				if m.ui.search.Focused() != wantFocus {
					t.Fatal("Escape did not restore input focus for the previous mode")
				}
			}
			if test.kind == dialogFiles && m.ui.picker.CurrentDirectory != directory {
				t.Fatal("Escape reset the file picker directory")
			}
			if m.ui.detailsOpen != test.context || m.planner.pickingStop != test.picking {
				t.Fatal("Escape changed the context panel or map picking mode")
			}
			if test.kind == dialogWorkspace && m.ui.filesCollapsed != test.collapsed {
				t.Fatal("Escape changed the Files panel visibility")
			}
			if test.kind >= dialogJourney && m.planner.collapsed != test.collapsed {
				t.Fatal("Escape changed the Journey panel visibility")
			}
			mapChanged := m.camera != camera || m.workspace.selection != selection
			if mapChanged || !reflect.DeepEqual(*m.activePlayback(), playback) {
				t.Fatal("opening or closing the palette changed map or playback state")
			}
		})
	}
}
