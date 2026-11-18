package main

import (
	"image"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (s uiStyles) choiceRow(label string, width int, focused bool) string {
	style, prefix := s.dialogText, "  "
	if focused {
		style, prefix = s.selected, "> "
	}
	return style.Render(pad(ansi.Truncate(prefix+label, width, "…"), width))
}

func (s uiStyles) choiceList(description string, labels []string, selected, width, height int) string {
	var rows []string
	if height > 3 {
		rows = append(rows, s.dialogMuted.Render(pad(description, width)), "")
	}
	count := max(1, height-len(rows))
	start := max(0, selected-count+1)
	for i := start; i < min(len(labels), start+count); i++ {
		rows = append(rows, s.choiceRow(labels[i], width, i == selected))
	}
	return strings.Join(rows, "\n")
}

func (m *viewer) confirmationButtons(labels []string, bounds []image.Rectangle, width, row, selected, destructive int) string {
	buttons := make([]string, len(labels))
	for i, label := range labels {
		style := m.styles.dialogText.Background(lipgloss.Color(m.theme().Selected)).Padding(0, 1)
		prefix := "  "
		if i == selected {
			colour := m.theme().Accent
			if i == destructive {
				colour = m.theme().Highlight
			}
			style = style.Background(lipgloss.Color(colour)).Foreground(lipgloss.Color(m.theme().Surface)).Bold(true)
			prefix = "> "
		}
		buttons[i] = style.Render(prefix + label)
	}
	joined := strings.Join(buttons, "  ")
	left := width - lipgloss.Width(joined)
	stack := left < 0
	if stack {
		left = 0
		joined = strings.Join(buttons, "\n")
	}
	x := left
	for i, button := range buttons {
		end := x + lipgloss.Width(button)
		bounds[i] = image.Rect(x, row, end, row+1)
		if stack {
			row++
		} else {
			x = end + 2
		}
	}
	return strings.Repeat(" ", left) + joined
}

func confirmationHints(width int) string {
	const hints = "tab choose / enter select / esc cancel"
	if width < len(hints) {
		return "tab choose\nenter select / esc"
	}
	return hints
}
