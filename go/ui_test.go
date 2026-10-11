package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestNestedCommandsReturnToFilteredMenu(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		for _, action := range []string{"help", "open", "workspace", "journey", "speed", "theme", "auth"} {
			name := fmt.Sprintf("%s/%dx%d", action, size[0], size[1])
			t.Run(name, func(t *testing.T) {
				m := authTestViewer(t)
				if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
					t.Fatal(err)
				}
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				query := "o"
				if action == "speed" {
					query = "p"
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
				if m.ui.dialog == dialogCommands || m.ui.search.Focused() {
					t.Fatal("child did not take focus from the command input")
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
