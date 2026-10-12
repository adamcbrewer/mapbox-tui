package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

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

func TestNestedPopupFramesExposeOnlyTopAndLeftEdges(t *testing.T) {
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
				var rows [][]rune
				for _, line := range strings.Split(text, "\n") {
					rows = append(rows, []rune(line))
				}
				corner, rightCorner, bottomCorner := '╭', '╮', '╯'
				horizontal, vertical := '─', '│'
				if ascii {
					corner, rightCorner, bottomCorner = '+', '+', '+'
					horizontal, vertical = '-', '|'
				}
				var found bool
				for y := range len(rows) - 2 {
					for x := range min(len(rows[y]), len(rows[y+1]), len(rows[y+2])) - 2 {
						diagonal := rows[y][x] == corner && rows[y+1][x+1] == corner && rows[y+2][x+2] == corner
						topLeft := rows[y][x+1] == horizontal && rows[y+1][x] == vertical
						if !diagonal || !topLeft {
							continue
						}
						found = true
						right := x + 3
						for right < len(rows[y+2]) && rows[y+2][right] != rightCorner {
							right++
						}
						if right == len(rows[y+2]) || rows[y+1][right-1] != rightCorner || rows[y][right-2] != rightCorner {
							t.Fatal("back frames extended beyond the active top-right footprint", text)
						}
						bottom := y + 3
						for bottom < len(rows) && rows[bottom][right] != bottomCorner {
							bottom++
						}
						if bottom == len(rows) || rows[bottom-1][right-1] == bottomCorner || rows[bottom-2][right-2] == bottomCorner {
							t.Fatal("back frames leaked their bottom-right corners", text)
						}
					}
				}
				if !found || !strings.Contains(text, "esc back") {
					t.Fatal("three-level diagonal stack or back hint missing", text)
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
