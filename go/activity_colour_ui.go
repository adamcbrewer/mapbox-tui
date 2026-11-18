package main

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *viewer) previewActivityColour(ref ItemRef, slot activityColor) activityColor {
	if m.ui.dialog == dialogActivityColour && m.ui.activityColourRef == ref {
		return activityColor(m.ui.selected + 1)
	}
	return slot
}

func (m *viewer) previewSceneColours(items []sceneItem) []sceneItem {
	if m.ui.dialog == dialogActivityColour {
		for i := range items {
			items[i].Color = m.previewActivityColour(items[i].Ref, items[i].Color)
		}
	}
	return items
}

func (m *viewer) updateActivityColour(key string) tea.Cmd {
	switch key {
	case "left", "up", "h", "k":
		m.ui.selected = menuStep(m.ui.selected, -1, activityColorCount)
	case "right", "down", "l", "j":
		m.ui.selected = menuStep(m.ui.selected, 1, activityColorCount)
	case "enter":
		if err := m.workspace.setItemColor(m.ui.activityColourRef, activityColor(m.ui.selected+1)); err != nil {
			return nil
		}
		return m.backDialog()
	}
	return nil
}

func (m *viewer) activitySwatchGlyph() string {
	if m.settings.ASCII {
		return "#"
	}
	return "■"
}

func (m *viewer) activityColourContent(width, height int) (string, string, string) {
	title := "Activity colour"
	ref := m.ui.activityColourRef
	entry, index, err := m.workspace.lookup(ref)
	if err != nil {
		return title, "Activity no longer loaded.", "esc back"
	}
	s := m.styles
	swatch := m.activitySwatchGlyph()
	gap := "\n"
	if height >= 4 {
		gap += "\n"
	}
	var row strings.Builder
	for i, colour := range m.theme().Activities {
		if i > 0 && width >= 36 {
			row.WriteString("   ")
		}
		style, pointer := s.dialogText, " "
		if i == m.ui.selected {
			style, pointer = s.selected, ">"
		}
		row.WriteString(style.Render(pointer + strconv.Itoa(i+1)))
		row.WriteString(style.Foreground(lipgloss.Color(colour)).Render(swatch))
	}
	label := clean(entry.document.Items[index].Track.Name)
	return title, s.dialogText.Render(pad(label, width)) + gap + row.String(), "arrows preview\nenter keep / esc cancel"
}
