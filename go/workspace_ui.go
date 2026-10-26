package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type workspaceRow struct {
	ref          ItemRef
	label        string
	compactLabel string
	path         string
	source       string
	visible      bool
	color        activityColor
}

func (w *Workspace) itemLabels() map[ItemRef]string {
	labels := make(map[ItemRef]string)
	for _, entry := range w.documents {
		var count int
		for _, item := range entry.document.Items {
			if len(item.Track.Points) > 0 {
				count++
			}
		}
		var number int
		for _, item := range entry.document.Items {
			label := item.Track.Name
			if count > 1 && len(item.Track.Points) > 0 {
				number++
				label = fmt.Sprintf("Track %d · %s", number, label)
			}
			labels[ItemRef{DocumentID: entry.id, ItemID: item.ID}] = label
		}
	}
	return labels
}

func (m *viewer) workspaceRows() []workspaceRow {
	var rows []workspaceRow
	labels := m.workspace.itemLabels()
	for _, item := range m.workspace.items() {
		if item.Item.Journey != nil && !item.Item.Journey.hasCompleteLocations() {
			entry, _, _ := m.workspace.lookup(item.Ref)
			if !m.workspace.savedJourney(entry.saveID) {
				continue
			}
		}
		active, shown := " ", "x"
		if m.workspace.selection != nil && m.workspace.selection.ref == item.Ref {
			active = "*"
		}
		if !item.Visible {
			shown = " "
		}
		prefix := fmt.Sprintf(
			"%d. %s [%s] ",
			len(rows)+1,
			active,
			shown,
		)
		source := clean(item.Document.Filename)
		label := labels[item.Ref]
		rows = append(rows, workspaceRow{
			ref:   item.Ref,
			label: prefix + label,
			compactLabel: fmt.Sprintf(
				"%d.%s[%s] %s %s",
				len(rows)+1,
				active,
				shown,
				source,
				label,
			),
			path:    item.Document.Path,
			source:  source,
			visible: item.Visible,
			color:   item.Color,
		})
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
		m.ui.selected = menuStep(m.ui.selected, -1, len(rows))
	case "down", "j":
		m.ui.selected = menuStep(m.ui.selected, 1, len(rows))
	case "home":
		m.ui.selected = 0
	case "end":
		m.ui.selected = len(rows) - 1
	case "alt+up", "alt+down":
		direction := -1
		if key == "alt+down" {
			direction = 1
		}
		target := m.ui.selected + direction
		if target >= 0 && target < len(rows) {
			err = m.workspace.moveItem(row.ref, rows[target].ref)
			if err == nil {
				m.ui.selected = target
			}
		}
	case "enter":
		err = m.workspace.selectItem(row.ref)
		m.closeDialog()
		m.ui.detailsOpen = true
	case "space":
		err = m.workspace.setVisible(row.ref, !row.visible)
	case "v":
		err = m.workspace.setDocumentVisible(row.ref.DocumentID, !row.visible)
	case "delete", "backspace", "D":
		m.loadID++
		m.loading, m.pendingPaths = false, nil
		m.startupCameraPending = false
		if key == "D" {
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

func workspaceListLayout(height int) (int, int) {
	available := height
	if height > 3 {
		available -= 3
	}
	if height >= 7 {
		available -= 2
	}
	rowHeight := min(2, max(1, available))
	return max(1, available/rowHeight), rowHeight
}

func (m *viewer) workspaceDialogContent(width, height int) (string, string, string) {
	s := m.styles
	rows := m.workspaceRows()
	if len(rows) == 0 {
		return "Loaded files", s.dialogMuted.Render("No files loaded. Use ctrl+o."), "esc close"
	}
	m.ui.selected = max(0, min(len(rows)-1, m.ui.selected))
	count, rowHeight := workspaceListLayout(height)
	start := max(0, m.ui.selected-count+1)
	lines := []string{}
	for i := start; i < min(len(rows), start+count); i++ {
		style := s.dialogText
		if !rows[i].visible {
			style = s.dialogMuted
		}
		prefix := "  "
		if i == m.ui.selected {
			prefix = "> "
			style = s.selected
		}
		if rows[i].color != 0 && i != m.ui.selected {
			style = style.
				Foreground(lipgloss.Color(m.theme().activityTextColor(rows[i].color))).
				Faint(!rows[i].visible)
		}
		label := rows[i].label
		if rowHeight == 1 {
			label = rows[i].compactLabel
		}
		lines = append(lines, style.Render(pad(prefix+label, width)))
		if rowHeight > 1 {
			lines = append(lines, s.dialogMuted.Render(pad("    "+rows[i].source, width)))
		}
	}
	if height > 3 {
		path := rows[m.ui.selected].path
		if path == "" {
			path = rows[m.ui.selected].source
		}
		if height >= 7 {
			lines = append(lines, "")
		}
		lines = append(lines, s.dialogMuted.Render(pad(abbreviatedWorkspacePath(path, width, m.settings.ASCII), width)))
		if height >= 7 {
			lines = append(lines, "")
		}
		lines = append(
			lines,
			s.dialogMuted.Render(pad("alt+up/down move · v file visibility", width)),
			s.dialogMuted.Render(pad("del remove item · D remove file", width)),
		)
	}
	return "Loaded files", strings.Join(lines, "\n"), "enter select · space hide/show · v file · esc close"
}

func (m *viewer) workspaceLegend(width int, profile *elevationProfile) string {
	s := m.styles
	items := m.workspace.sceneItems()
	if len(items) == 0 {
		return s.muted.Render(pad("No visible items · ctrl+l files", width))
	}
	count := min(len(items), max(1, (width-16)/20))
	suffix := " · ctrl+l files"
	if count < len(items) {
		suffix = fmt.Sprintf(" · +%d more", len(items)-count) + suffix
	}
	columnWidth := max(1, (width-ansi.StringWidth(suffix)-(count-1)*2)/count)
	var labels []string
	names := m.workspace.itemLabels()
	for _, item := range items[:count] {
		marker := " "
		if item.Active {
			marker = "*"
		}
		label := fmt.Sprintf(
			"%s %s",
			marker,
			names[item.Ref],
		)
		missingElevation := profile != nil && len(item.Track.Points) > 0 && !profile.AvailableItems[item.Ref]
		if missingElevation {
			label = fmt.Sprintf(
				"%s no elevation: %s",
				marker,
				names[item.Ref],
			)
		}
		style := s.text.Foreground(lipgloss.Color(m.theme().activityTextColor(item.Color))).Bold(item.Active)
		label = pad(ansi.Truncate(label, columnWidth, "…"), columnWidth)
		labels = append(labels, style.Render(label))
	}
	return pad(strings.Join(labels, "  ")+s.muted.Render(suffix), width)
}
