package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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
