package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestFilesPanelMouseSelectsActivityFromEitherDisplayLine(t *testing.T) {
	for _, line := range []int{0, 1} {
		name := "activity line"
		if line == 1 {
			name = "source line"
		}
		t.Run(name, func(t *testing.T) {
			m := themeViewer(t, 120, 40, false)
			m.openDialog(dialogWorkspace)
			m.ui.selected = 0
			rows := m.workspaceRows()
			target := rows[1].ref
			m.updateFilesPanelMouse(tea.MouseClickMsg{
				X:      m.journeyPanelX() + 4,
				Y:      m.mapOriginY() + 5 + line,
				Button: tea.MouseLeft,
			})
			if m.workspace.selection == nil || m.workspace.selection.ref != target || m.ui.dialog != dialogNone {
				t.Fatal("click selected the wrong activity or ignored its source line")
			}
		})
	}
}

func TestFilesPanelMouseSelectsRenderedRowAfterScrolling(t *testing.T) {
	for _, test := range []struct {
		name         string
		height, line int
	}{
		{name: "scrolled activity line", height: 24},
		{name: "scrolled source line", height: 24, line: 1},
		{name: "compact scrolled line", height: 18},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := themeViewer(t, 70, test.height, false)
			m.openDialog(dialogWorkspace)
			rows := m.workspaceRows()
			m.ui.selected = len(rows) - 1
			m.rebuild()
			content := strings.Split(ansi.Strip(m.content), "\n")
			y := m.mapOriginY() + 3
			line := []rune(content[y])
			visible := string(line[m.journeyPanelX():])
			var target ItemRef
			for i, row := range rows {
				if strings.Contains(visible, fmt.Sprintf("%d.", i+1)) {
					target = row.ref
					break
				}
			}
			if target == (ItemRef{}) || target == rows[0].ref {
				t.Fatal("test did not render a scrolled activity", visible)
			}
			if test.line == 1 && !strings.Contains(content[y+1], "example.gpx") {
				t.Fatal("source line was not visible below the activity")
			}
			m.updateFilesPanelMouse(tea.MouseClickMsg{
				X:      m.journeyPanelX() + 4,
				Y:      y + test.line,
				Button: tea.MouseLeft,
			})
			if m.workspace.selection == nil || m.workspace.selection.ref != target {
				t.Fatal("mouse selection did not match the rendered activity", target)
			}
		})
	}
}
