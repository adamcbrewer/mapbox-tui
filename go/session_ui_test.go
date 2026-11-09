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

func TestSessionDialogCopiesCurrentViewerWithoutChangingWorkspace(t *testing.T) {
	if !sessionControlSupported {
		t.Skip("live control requires Unix")
	}
	for _, name := range []string{"", "main"} {
		t.Run("name="+name, func(t *testing.T) {
			m := themeViewer(t, 120, 40, false)
			info := sessionInfo{ID: newSessionID(), Name: name}
			m.control = &viewerControl{server: &sessionServer{info: info}}
			if _, err := m.saveWorkspaceCheckpoint("Saved workspace", false); err != nil {
				t.Fatal(err)
			}
			checkpoint := m.workspace.checkpoint
			generation, revision := m.workspace.liveIdentity(), m.workspace.liveRevision
			selection, camera := m.workspace.selection, m.camera
			m.openDialog(dialogCommands)
			m.ui.search.SetValue("session")
			if commands := m.commands(); len(commands) != 1 || commands[0].title != "Session" {
				t.Fatal("Session is not discoverable in the palette", commands)
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.rebuild()
			if m.ui.dialog != dialogSession || !strings.Contains(ansi.Strip(m.content), info.ID) {
				t.Fatal("Session did not show the viewer's full UUID", m.content)
			}
			if name != "" && !strings.Contains(ansi.Strip(m.content), "Name: "+name) {
				t.Fatal("Session did not show its name")
			}
			for _, action := range []struct {
				name string
				key  rune
				text string
			}{
				{name: "UUID", key: '1', text: info.ID},
				{name: "CLI", key: '2', text: "mapbox-tui remote --session " + info.ID + " status --json"},
				{name: "MCP", key: '3', text: "mapbox-tui mcp --session " + info.ID},
			} {
				t.Run(action.name, func(t *testing.T) {
					_, cmd := m.Update(tea.KeyPressMsg{Code: action.key, Text: string(action.key)})
					if cmd == nil || !reflect.DeepEqual(cmd(), tea.SetClipboard(action.text)()) {
						t.Fatal("copy action did not send the exact current session value")
					}
					if !strings.Contains(m.ui.notice, "sent to clipboard") || m.ui.dialog != dialogSession {
						t.Fatal("copy lost the dialog or its feedback")
					}
				})
			}
			if m.workspace.checkpoint != checkpoint || m.workspace.liveIdentity() != generation ||
				m.workspace.liveRevision != revision || m.workspace.selection != selection || m.camera != camera {
				t.Fatal("viewing or copying session details changed the working or saved workspace")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.ui.dialog != dialogCommands || m.ui.search.Value() != "session" {
				t.Fatal("Session did not return to the filtered palette")
			}
		})
	}
}

func TestSessionDialogWrapsScrollsAndCopiesInNarrowLayouts(t *testing.T) {
	if !sessionControlSupported {
		t.Skip("live control requires Unix")
	}
	for _, size := range [][2]int{{120, 40}, {70, 24}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/ascii=%t", size[0], size[1], ascii), func(t *testing.T) {
				m := themeViewer(t, size[0], size[1], ascii)
				info := sessionInfo{ID: newSessionID(), Name: strings.Repeat("n", 48)}
				m.control = &viewerControl{server: &sessionServer{info: info}}
				m.openDialog(dialogCommands)
				m.runCommand("session")
				m.rebuild()
				content := ansi.Strip(m.ui.page.GetContent())
				unwrapped := strings.Join(strings.Fields(content), "")
				for _, value := range []string{info.ID, info.Name, "mapbox-tuiremote--session" + info.ID + "status--json"} {
					if !strings.Contains(unwrapped, value) {
						t.Fatal("narrow Session content lost a full value", content)
					}
				}
				if !strings.Contains(ansi.Strip(m.content), "1 ID 2 CLI 3 MCP") {
					t.Fatal("copy shortcuts were hidden", ansi.Strip(m.content))
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				if m.ui.page.YOffset() == 0 {
					t.Fatal("Session does not scroll")
				}
				_, cmd := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
				want := "mapbox-tui remote --session " + info.ID + " status --json"
				if cmd == nil || !reflect.DeepEqual(cmd(), tea.SetClipboard(want)()) {
					t.Fatal("narrow Session copied a truncated command")
				}
				m.rebuild()
				if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
					t.Fatal("Session exceeded terminal bounds")
				}
				if ascii && strings.ContainsAny(ansi.Strip(m.content), "╭╮╰╯") {
					t.Fatal("ASCII Session retained Unicode borders")
				}
			})
		}
	}
}

func TestSessionDialogExplainsUnavailableControl(t *testing.T) {
	for _, noControl := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-control=%t", noControl), func(t *testing.T) {
			m := themeViewer(t, 70, 24, false)
			m.args.NoControl = noControl
			m.openDialog(dialogCommands)
			m.runCommand("session")
			m.rebuild()
			want := "unavailable"
			if noControl {
				want = "disabled by --no-control"
			} else if !sessionControlSupported {
				want = "unsupported on this platform"
			}
			if !strings.Contains(ansi.Strip(m.ui.page.GetContent()), want) {
				t.Fatal("Session did not explain why control is unavailable", m.content)
			}
			for _, key := range "123" {
				if _, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)}); cmd != nil {
					t.Fatal("unavailable Session offered a copy command")
				}
			}
			if m.sessionLabel() != "" || strings.Contains(ansi.Strip(m.content), "Copy:") {
				t.Fatal("unavailable Session showed an identity or copy actions")
			}
		})
	}
}

func TestSessionFooterKeepsContextAndShortcuts(t *testing.T) {
	for _, name := range []string{"", "main"} {
		t.Run("name="+name, func(t *testing.T) {
			m := themeViewer(t, 160, 50, false)
			info := sessionInfo{ID: newSessionID(), Name: name}
			m.control = &viewerControl{server: &sessionServer{info: info}}
			identity := name
			if identity == "" {
				identity = info.ID[:8]
			}
			footer := ansi.Strip(strings.Join(m.contextFooter(), "\n"))
			for _, text := range []string{"Session " + identity, "ctrl+b sidebar", "/ search", "? help", m.theme().Name} {
				if !strings.Contains(footer, text) {
					t.Fatal("footer lost session, context or shortcuts", text, footer)
				}
			}
			m.width = 120
			m.inspected = &XY{X: -111, Y: 27}
			footer = ansi.Strip(strings.Join(m.contextFooter(), "\n"))
			if !strings.Contains(footer, "Selected position") {
				t.Fatal("session label crowded out the selected context", footer)
			}
		})
	}
}
