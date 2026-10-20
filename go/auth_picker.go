package main

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type authMethodPicker struct {
	selected  int
	width     int
	method    string
	cancelled bool
}

func (m *authMethodPicker) Init() tea.Cmd { return nil }

func (m *authMethodPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "down", "j", "k", "tab", "shift+tab":
			m.selected = 1 - m.selected
		case "1":
			m.selected = 0
		case "2":
			m.selected = 1
		case "enter":
			m.method = []string{"token", "browser"}[m.selected]
			return m, tea.Quit
		case "esc", "ctrl+c", "q":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *authMethodPicker) View() tea.View {
	labels := []string{"Enter public API token", "Browser login"}
	if m.method != "" {
		return tea.NewView("Login method: " + labels[m.selected] + "\n")
	}
	if m.cancelled {
		return tea.NewView("Login cancelled.\n")
	}
	width := max(1, min(64, m.width-2))
	selected := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	muted := lipgloss.NewStyle().Faint(true).Width(width)
	var content strings.Builder
	content.WriteString(lipgloss.NewStyle().Bold(true).Render("Choose login method") + "\n\n")
	for i, label := range labels {
		line := "  " + label
		if i == m.selected {
			line = selected.Render("> " + label)
		}
		content.WriteString(line + "\n")
	}
	descriptions := []string{
		"Use a public pk. token from the Mapbox Console. No CLI needed.",
		"Sign in through the official Mapbox CLI. A browser on another device works too.",
	}
	content.WriteString("\n" + muted.Render(descriptions[m.selected]) + "\n\n")
	content.WriteString(muted.Render("↑/↓ choose · Enter select · Esc cancel") + "\n")
	return tea.NewView(content.String())
}

func chooseAuthMethod(ctx context.Context, streams authStreams) (string, error) {
	picker := &authMethodPicker{width: 80}
	_, err := tea.NewProgram(
		picker,
		tea.WithContext(ctx),
		tea.WithInput(streams.in),
		tea.WithOutput(streams.out),
		tea.WithoutSignalHandler(),
	).Run()
	if ctx.Err() != nil || picker.cancelled || err == nil && picker.method == "" {
		return "", &mapboxFailure{code: "cancelled"}
	}
	if err != nil {
		return "", fmt.Errorf("choose login method: %w", err)
	}
	return picker.method, nil
}
