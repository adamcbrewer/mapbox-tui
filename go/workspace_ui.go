package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type workspaceRow struct {
	ref     ItemRef
	label   string
	path    string
	visible bool
	color   string
}

func (m *viewer) workspaceRows() []workspaceRow {
	var rows []workspaceRow
	for _, entry := range m.workspace.documents {
		visible := false
		for _, item := range entry.document.Items {
			visible = visible || !entry.hidden[item.ID]
		}
		rows = append(rows, workspaceRow{ItemRef{DocumentID: entry.id}, fmt.Sprintf("[%d] %s", entry.id, clean(entry.document.Filename)), entry.document.Path, visible, ""})
		for _, item := range entry.document.Items {
			ref := ItemRef{entry.id, item.ID}
			active, shown := " ", "x"
			if m.workspace.selection != nil && m.workspace.selection.ref == ref {
				active = "*"
			}
			if entry.hidden[item.ID] {
				shown = " "
			}
			label := fmt.Sprintf(" %s [%s] %d %s", active, shown, item.ID, item.Track.Name)
			rows = append(rows, workspaceRow{ref, label, entry.document.Path, !entry.hidden[item.ID], entry.colors[item.ID]})
		}
	}
	return rows
}

func (m *viewer) updateWorkspaceDialog(key string) tea.Cmd {
	rows := m.workspaceRows()
	if len(rows) == 0 {
		return nil
	}
	m.ui.selected = max(0, min(len(rows)-1, m.ui.selected))
	row := rows[m.ui.selected]
	before := m.workspace.selection
	var err error
	switch key {
	case "up", "k":
		m.ui.selected = max(0, m.ui.selected-1)
	case "down", "j":
		m.ui.selected = min(len(rows)-1, m.ui.selected+1)
	case "home":
		m.ui.selected = 0
	case "end":
		m.ui.selected = len(rows) - 1
	case "enter":
		if row.ref.ItemID == 0 {
			for _, entry := range m.workspace.documents {
				if entry.id == row.ref.DocumentID {
					m.workspace.activateDocument(entry)
					break
				}
			}
		} else {
			err = m.workspace.selectItem(row.ref)
		}
		m.closeDialog()
	case "space":
		if row.ref.ItemID == 0 {
			err = m.workspace.setDocumentVisible(row.ref.DocumentID, !row.visible)
		} else {
			err = m.workspace.setVisible(row.ref, !row.visible)
		}
	case "delete", "backspace", "D":
		m.loadID++
		m.loading, m.pendingPaths = false, nil
		if row.ref.ItemID == 0 || key == "D" {
			err = m.workspace.removeDocument(row.ref.DocumentID)
		} else {
			err = m.workspace.removeItem(row.ref)
		}
		m.ui.selected = min(m.ui.selected, max(0, len(m.workspaceRows())-1))
	}
	if before != m.workspace.selection {
		m.selectionChanged()
	}
	if err != nil {
		m.ui.notice = err.Error()
	}
	m.layout()
	m.dirty = true
	return nil
}

func (m *viewer) workspaceDialogContent(width, height int) (string, string, string) {
	rows := m.workspaceRows()
	if len(rows) == 0 {
		return "Loaded files", dialogMuted.Render("No files loaded. Use ctrl+o."), "esc close"
	}
	m.ui.selected = max(0, min(len(rows)-1, m.ui.selected))
	count := max(1, height-3)
	start := max(0, m.ui.selected-count+1)
	lines := []string{}
	for i := start; i < min(len(rows), start+count); i++ {
		style := dialogText
		if rows[i].ref.ItemID == 0 {
			style = dialogHeading
		} else if !rows[i].visible {
			style = dialogMuted
		}
		prefix := "  "
		if i == m.ui.selected {
			prefix = "> "
			style = uiHeading.Foreground(uiRose).Background(uiSelected)
		}
		if rows[i].color != "" {
			style = style.Foreground(lipgloss.Color(rows[i].color)).Faint(!rows[i].visible)
		}
		lines = append(lines, style.Render(pad(prefix+rows[i].label, width)))
	}
	if height > 3 {
		lines = append(lines, "", dialogMuted.Render(pad(clean(rows[m.ui.selected].path), width)), dialogMuted.Render(pad("del remove row · D remove file", width)))
	}
	return "Loaded files", strings.Join(lines, "\n"), "enter select · space hide/show · esc close"
}

func (m *viewer) workspaceLegend(width int, profile *elevationProfile) string {
	items := m.workspace.sceneItems()
	if len(items) == 0 {
		return uiMuted.Render(pad("No visible items · ctrl+l files", width))
	}
	count := min(len(items), max(1, (width-16)/20))
	suffix := " · ctrl+l files"
	if count < len(items) {
		suffix = fmt.Sprintf(" · +%d more", len(items)-count) + suffix
	}
	columnWidth := max(1, (width-ansi.StringWidth(suffix)-(count-1)*2)/count)
	var labels []string
	for _, item := range items[:count] {
		marker := " "
		if item.Active {
			marker = "*"
		}
		label := fmt.Sprintf("%s %d:%d %s", marker, item.Ref.DocumentID, item.Ref.ItemID, item.Track.Name)
		if profile != nil && len(item.Track.Points) > 0 && !profile.AvailableItems[item.Ref] {
			label = fmt.Sprintf("%s %d:%d no elevation: %s", marker, item.Ref.DocumentID, item.Ref.ItemID, item.Track.Name)
		}
		labels = append(labels, uiText.Foreground(lipgloss.Color(item.Color)).Bold(item.Active).Render(pad(ansi.Truncate(label, columnWidth, "…"), columnWidth)))
	}
	return pad(strings.Join(labels, "  ")+uiMuted.Render(suffix), width)
}
