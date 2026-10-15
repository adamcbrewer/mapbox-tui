package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCommandPaletteRestoresJourneyEditorTarget(t *testing.T) {
	for _, test := range []struct {
		name           string
		index, nested  int
		addingWaypoint bool
		otherJourney   bool
	}{
		{name: "start", index: 0, nested: 1},
		{name: "destination", index: 1, nested: 0},
		{name: "waypoint", addingWaypoint: true},
		{name: "other journey", index: 0, nested: 1, otherJourney: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := journeyViewer(t)
			m.openJourney()
			enterJourneyCoordinate(m, 0, "51.5, -0.1")
			enterJourneyCoordinate(m, 1, "51.6, -0.2")
			ref := m.planner.ref
			if test.addingWaypoint {
				m.editNewWaypoint()
			} else {
				m.editJourneyLocation(test.index)
			}
			m.ui.search.SetValue("51.55, -0.15")
			var otherRef ItemRef
			if test.otherJourney {
				otherRef = m.workspace.newJourney()
				if err := m.workspace.setJourneyLocation(otherRef, 1, placeLocation{Lat: 51.8, Lon: -0.4}); err != nil {
					t.Fatal(err)
				}
				if err := m.workspace.selectItem(otherRef); err != nil {
					t.Fatal(err)
				}
			}
			m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
			m.Update(tea.PasteMsg{Content: "Journey Planner"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.ui.dialog != dialogJourney {
				t.Fatal("palette did not open the nested planner")
			}
			var late placeSearchResult
			if !test.addingWaypoint {
				m.editJourneyLocation(test.nested)
				late = placeSearchResult{
					id: m.planner.searchID, ref: m.planner.ref, stopID: m.planner.stopID,
					location: &placeLocation{Lat: 52, Lon: -1},
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.ui.dialog != dialogJourneyLocation || m.ui.search.Value() != "51.55, -0.15" {
				t.Fatal("Escape did not restore the original editor input")
			}
			if !test.addingWaypoint {
				m.Update(late)
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			j, err := m.workspace.journey(ref)
			if err != nil {
				t.Fatal(err)
			}
			expected := []placeLocation{{Lat: 51.5, Lon: -0.1}, {Lat: 51.6, Lon: -0.2}}
			location := placeLocation{Lat: 51.55, Lon: -0.15}
			if test.addingWaypoint {
				expected = []placeLocation{expected[0], location, expected[1]}
			} else {
				expected[test.index] = location
			}
			if len(j.Stops) != len(expected) {
				t.Fatal("resumed editor did not add its waypoint", m.ui.notice)
			}
			for i, want := range expected {
				got := j.Stops[i].Location
				if got == nil || got.Lat != want.Lat || got.Lon != want.Lon {
					t.Fatalf(
						"resumed editor changed the wrong stop: stop %d is %v, want %v",
						i,
						got,
						want,
					)
				}
			}
			if test.otherJourney {
				other, err := m.workspace.journey(otherRef)
				if err != nil {
					t.Fatal(err)
				}
				if other.Stops[0].Location.Lat != 51.8 || other.Stops[0].Location.Lon != -0.4 {
					t.Fatal("resumed editor changed the nested journey")
				}
			}
		})
	}
}

func TestCommandPaletteRestoresJourneyMapPicking(t *testing.T) {
	for _, test := range []struct {
		name string
		kind dialogKind
	}{
		{name: "planner", kind: dialogJourney},
		{name: "location editor", kind: dialogJourneyLocation},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := journeyViewer(t)
			m.openJourney()
			if test.kind == dialogJourneyLocation {
				m.editJourneyLocation(0)
			}
			m.startJourneyPick()
			m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
			m.Update(tea.PasteMsg{Content: "Journey Planner"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			j, err := m.workspace.journey(m.planner.ref)
			if err != nil {
				t.Fatal(err)
			}
			if j.Stops[0].Location == nil || j.Stops[1].Location != nil {
				t.Fatal("resumed picking mode did not choose the original stop")
			}
		})
	}
}

func TestMenusWrapSelection(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		dialog      dialogKind
	}{
		{name: "commands", dialog: dialogCommands},
		{name: "filtered commands", dialog: dialogCommands, query: "toggle"},
		{name: "single command", dialog: dialogCommands, query: "Colour theme"},
		{name: "empty commands", dialog: dialogCommands, query: "no matching commands"},
		{name: "themes", dialog: dialogThemes},
		{name: "speed", dialog: dialogSpeed},
		{name: "loaded files", dialog: dialogWorkspace},
		{name: "Mapbox settings", dialog: dialogMapbox},
		{name: "journey", dialog: dialogJourney},
		{name: "travel mode", dialog: dialogJourneyRouting},
		{name: "place suggestions", dialog: dialogJourneyLocation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := journeyViewer(t)
			if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
				t.Fatal(err)
			}
			if tc.dialog >= dialogJourney {
				m.openJourney()
			}
			m.openDialog(tc.dialog)
			selected, count := &m.ui.selected, 0
			up, down := tea.KeyPressMsg{Code: 'k', Text: "k"}, tea.KeyPressMsg{Code: 'j', Text: "j"}
			switch tc.dialog {
			case dialogCommands:
				m.ui.search.SetValue(tc.query)
				count = len(m.commands())
				up = tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
				down = tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
			case dialogThemes:
				count = len(m.themes)
			case dialogSpeed:
				count = 5
			case dialogWorkspace:
				count = len(m.workspaceRows())
			case dialogMapbox:
				count = len(m.mapboxActions())
			case dialogJourney:
				j, _ := m.workspace.journey(m.planner.ref)
				selected, count = &m.planner.row, len(j.Stops)+len(journeyActions)
			case dialogJourneyRouting:
				count = len(journeyModes) + 1
			case dialogJourneyLocation:
				m.editJourneyLocation(0)
				m.planner.suggestions = []placeSuggestion{
					{ID: "a", Label: "First"},
					{ID: "b", Label: "Last"},
				}
				selected, count = &m.planner.result, len(m.planner.suggestions)
				up, down = tea.KeyPressMsg{Code: tea.KeyUp}, tea.KeyPressMsg{Code: tea.KeyDown}
			}
			for range *selected {
				m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			}
			for _, step := range []struct {
				key  tea.KeyPressMsg
				want int
			}{
				{key: tea.KeyPressMsg{Code: tea.KeyUp}, want: max(0, count-1)},
				{key: tea.KeyPressMsg{Code: tea.KeyDown}, want: 0},
				{key: up, want: max(0, count-1)},
				{key: down, want: 0},
			} {
				m.Update(step.key)
				if *selected != step.want || m.ui.dialog != tc.dialog {
					t.Fatalf(
						"%s selected %d, want %d, dialog %v",
						step.key.String(),
						*selected,
						step.want,
						m.ui.dialog,
					)
				}
				if tc.dialog == dialogThemes && m.settings.Theme != step.want {
					t.Fatal("wrapped theme selection was not previewed")
				}
			}
			if tc.dialog == dialogCommands && m.ui.search.Value() != tc.query {
				t.Fatal("wrapping changed the command filter")
			}
		})
	}
}

func TestFilePickerWrapsSelection(t *testing.T) {
	m := journeyViewer(t)
	m.openDialog(dialogFiles)
	m.ui.picker.CurrentDirectory = ".."
	m.Update(m.ui.picker.Init()())
	first := m.ui.picker.HighlightedPath()
	m.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	last := m.ui.picker.HighlightedPath()
	if first == "" || first == last {
		t.Fatal("file picker fixture did not contain multiple entries")
	}
	m.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	for _, step := range []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{key: tea.KeyPressMsg{Code: tea.KeyUp}, want: last},
		{key: tea.KeyPressMsg{Code: tea.KeyDown}, want: first},
		{key: tea.KeyPressMsg{Code: 'k', Text: "k"}, want: last},
		{key: tea.KeyPressMsg{Code: 'j', Text: "j"}, want: first},
		{key: tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}, want: last},
		{key: tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}, want: first},
	} {
		m.Update(step.key)
		if got := m.ui.picker.HighlightedPath(); got != step.want || m.ui.dialog != dialogFiles {
			t.Fatalf(
				"%s highlighted %q, want %q",
				step.key.String(),
				got,
				step.want,
			)
		}
	}
	m.openDialog(dialogFiles)
	m.ui.picker.CurrentDirectory = t.TempDir()
	m.Update(m.ui.picker.Init()())
	for _, code := range []rune{tea.KeyUp, tea.KeyDown, tea.KeyEnter} {
		_, cmd := m.Update(tea.KeyPressMsg{Code: code})
		if cmd != nil || m.ui.picker.HighlightedPath() != "" || m.ui.dialog != dialogFiles {
			t.Fatal("navigation or selection changed an empty file picker")
		}
	}
}

func TestNestedCommandsReturnToFilteredMenu(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		for _, action := range []string{"help", "search", "open", "workspace", "journey", "speed", "theme", "auth"} {
			name := fmt.Sprintf("%s/%dx%d", action, size[0], size[1])
			t.Run(name, func(t *testing.T) {
				m := authTestViewer(t)
				if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
					t.Fatal(err)
				}
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				if m.ui.dialog != dialogNone {
					t.Fatal("removed Commands shortcut still opened a dialog")
				}
				m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
				m.Update(tea.PasteMsg{Content: "discarded"})
				m.ui.search.SetCursor(0)
				m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
				if m.ui.dialog != dialogCommands || m.ui.search.Value() != "" {
					t.Fatal("Commands shortcut stole delete-to-end-of-line from its input")
				}
				query := "o"
				switch action {
				case "speed":
					query = "p"
				case "search":
					query = "search"
				}
				m.Update(tea.PasteMsg{Content: query})
				for i, command := range m.commands() {
					if command.action == action {
						for range i {
							m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
						}
						break
					}
				}
				if m.commands()[m.ui.selected].action != action {
					t.Fatal("child action missing from filtered menu")
				}
				m.ui.search.SetCursor(0)
				m.ui.notice = "Parent notice"
				selected, navigated := m.ui.selected, m.ui.menuNavigated
				paste := m.clipboardCommand(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, func() tea.Msg {
					return tea.PasteMsg{Content: "obsolete clipboard"}
				})
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				m.rebuild()
				searchChild := action == "search"
				if m.ui.dialog == dialogCommands || m.ui.search.Focused() != searchChild {
					t.Fatal("child did not take input ownership from the command menu")
				}
				if !strings.Contains(ansi.Strip(m.content), "esc back") {
					t.Fatal("nested dialog omitted its visible back hint", ansi.Strip(m.content))
				}
				m.applyTheme((m.settings.Theme + 1) % len(m.themes))
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				m.Update(paste())
				m.rebuild()
				menuChanged := m.ui.dialog != dialogCommands || m.ui.selected != selected ||
					m.ui.menuNavigated != navigated || m.ui.notice != "Parent notice"
				inputChanged := m.ui.search.Value() != query || m.ui.search.Position() != 0 || !m.ui.search.Focused()
				if menuChanged || inputChanged {
					t.Fatal("back lost menu context or accepted an old clipboard result")
				}
				if m.ui.search.Styles().Cursor.Color != lipgloss.Color(m.theme().Highlight) {
					t.Fatal("restored input used the old theme")
				}
				if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
					t.Fatal("restored menu overflowed terminal")
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				if m.ui.dialog != dialogNone {
					t.Fatal("reopening a child added an extra back step")
				}
			})
		}
	}
}

func TestCompletedNestedActionsReturnToMap(t *testing.T) {
	for _, action := range []string{"speed", "workspace", "theme", "auth"} {
		t.Run(action, func(t *testing.T) {
			m := authTestViewer(t)
			if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
				t.Fatal(err)
			}
			initialTheme := m.settings.Theme
			m.openDialog(dialogCommands)
			m.runCommand(action)
			switch action {
			case "speed", "theme":
				m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			case "auth":
				for i, command := range m.mapboxActions() {
					if command.action == "continue" {
						m.ui.selected = i
					}
				}
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.ui.dialog != dialogNone {
				t.Fatal("completed action did not return to the map")
			}
			if action == "speed" && m.activePlayback().speed != 1 {
				t.Fatal("completed speed choice was not applied")
			}
			if action == "theme" && m.settings.Theme != min(initialTheme+1, len(m.themes)-1) {
				t.Fatal("completed theme choice was not applied")
			}
			m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.ui.dialog != dialogNone {
				t.Fatal("direct help restored an old menu after completing an action")
			}
		})
	}
}

func TestNestedPopupUsesBreadcrumbsWithoutBackFrames(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			name := fmt.Sprintf("%dx%d/ascii=%v", size[0], size[1], ascii)
			t.Run(name, func(t *testing.T) {
				m := authTestViewer(t)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.settings.ASCII = ascii
				m.openDialog(dialogCommands)
				m.ui.search.SetValue("Mapbox")
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				m.authBusy = false
				m.authCLI.State = "missing"
				m.authMessage = "Check failed: Mapbox CLI not found."
				m.rebuild()
				if !strings.Contains(ansi.Strip(m.content), "Check failed:") {
					t.Fatal("nested setup lost connection feedback", ansi.Strip(m.content))
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if m.ui.dialog != dialogMapboxHelp {
					t.Fatal("menu/setup/help chain did not open")
				}
				m.rebuild()
				text := ansi.Strip(m.content)
				separator := " › "
				if ascii {
					separator = " > "
				}
				if !strings.Contains(text, separator+"Installation") || !strings.Contains(text, "esc back") {
					t.Fatal("current modal title or explicit nesting missing", text)
				}
				if size[0] >= 70 && !strings.Contains(text, "Commands"+separator+"Mapbox"+separator) {
					t.Fatal("ancestor names missing from wide modal", text)
				}
				if !ascii && strings.Count(text, "╭") > 2 {
					t.Fatal("nested modal rendered fake parent frames", text)
				}
				if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
					t.Fatal("stack overflowed terminal")
				}
				m.applyTheme((m.settings.Theme + 1) % len(m.themes))
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				m.rebuild()
				if m.ui.dialog != dialogMapbox || !strings.Contains(ansi.Strip(m.content), "esc back") {
					t.Fatal("popping the larger help dialog lost its smaller setup parent")
				}
			})
		}
	}
}

func TestDialogOverlayPreservesBackgroundColours(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	for _, dialog := range []struct {
		name string
		kind dialogKind
	}{
		{name: "Commands", kind: dialogCommands},
		{name: "Colour theme", kind: dialogThemes},
	} {
		for i, theme := range m.themes {
			t.Run(dialog.name+"/"+theme.ID, func(t *testing.T) {
				m.openDialog(dialog.kind)
				m.applyTheme(i)
				var row strings.Builder
				for _, role := range []mapRole{
					roleWater, roleRoad, rolePlace, roleTrafficLow, roleTrafficModerate,
					roleTrafficHeavy, roleTrafficSevere, roleTrafficClosed,
				} {
					row.WriteString(m.paint("⠿", role))
				}
				row.WriteString(m.paint("⠿", roleActiveRoute, m.activeSwatch()))
				base := strings.Repeat(row.String()+"\n", m.height-1) + row.String()
				m.content = base
				m.overlay()
				if !strings.Contains(ansi.Strip(m.content), dialog.name) {
					t.Fatal("dialog was not rendered")
				}
				before := lipgloss.NewCanvas(m.width, m.height).Compose(
					lipgloss.NewCompositor(lipgloss.NewLayer(base)),
				)
				after := lipgloss.NewCanvas(m.width, m.height).Compose(
					lipgloss.NewCompositor(lipgloss.NewLayer(m.content)),
				)
				// These columns stay visible beside both dialogs, even at their full height.
				for y := range m.height {
					for x := range 9 {
						if !reflect.DeepEqual(before.CellAt(x, y), after.CellAt(x, y)) {
							t.Fatalf("dialog changed the underlying map cell at %d,%d", x, y)
						}
					}
				}
				m.closeDialog()
			})
		}
	}
}
