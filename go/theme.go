package main

import (
	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type presentationTheme struct {
	ID            string            `toml:"id"`
	Name          string            `toml:"name"`
	Background    string            `toml:"background"`
	Surface       string            `toml:"surface"`
	Selected      string            `toml:"selected"`
	Text          string            `toml:"text"`
	Muted         string            `toml:"muted"`
	Accent        string            `toml:"accent"`
	Highlight     string            `toml:"highlight"`
	Border        string            `toml:"border"`
	Dimmed        string            `toml:"dimmed"`
	ProgressEmpty string            `toml:"progress_empty"`
	Map           map[string]string `toml:"map"`
	Activities    []string          `toml:"activities"`
}

type uiStyles struct {
	text, muted, heading, accent, selected               lipgloss.Style
	dialogText, dialogMuted, dialogHeading, dialogAccent lipgloss.Style
}

func (t *presentationTheme) styles() uiStyles {
	text := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Text))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent))
	surface := lipgloss.Color(t.Surface)
	return uiStyles{
		text:          text,
		muted:         muted,
		heading:       text.Bold(true),
		accent:        accent,
		selected:      text.Bold(true).Foreground(lipgloss.Color(t.Highlight)).Background(lipgloss.Color(t.Selected)),
		dialogText:    text.Background(surface),
		dialogMuted:   muted.Background(surface),
		dialogHeading: text.Bold(true).Background(surface),
		dialogAccent:  accent.Background(surface),
	}
}

func (t *presentationTheme) mapColor(role mapRole, colors ...activityColor) string {
	if color := activitySwatch(colors...); color != 0 {
		return t.Activities[color-1]
	}
	return t.Map[mapRoleNames[role]]
}

func (m *viewer) theme() *presentationTheme {
	return &m.themes[m.settings.Theme]
}

func (m *viewer) applyTheme(index int) {
	m.settings.Theme = index
	if m.ui.dialog == dialogThemes {
		m.ui.selected = index
	}
	t := m.theme()
	m.styles = t.styles()
	s := m.styles
	m.ui.help.Styles = help.Styles{
		ShortKey:       s.accent.Bold(true),
		ShortDesc:      s.muted,
		ShortSeparator: s.muted,
		Ellipsis:       s.muted,
		FullKey:        s.dialogAccent.Bold(true),
		FullDesc:       s.dialogText,
		FullSeparator:  s.dialogMuted,
	}
	m.ui.details.Style = s.text
	m.ui.page.Style = s.dialogText
	inputStyles := m.ui.search.Styles()
	inputStyles.Focused.Text = s.text.Background(lipgloss.Color(t.Selected))
	inputStyles.Focused.Placeholder = s.muted.Background(lipgloss.Color(t.Selected))
	inputStyles.Focused.Suggestion = inputStyles.Focused.Placeholder
	inputStyles.Focused.Prompt = s.accent.Background(lipgloss.Color(t.Selected))
	inputStyles.Blurred = inputStyles.Focused
	inputStyles.Cursor.Color = lipgloss.Color(t.Highlight)
	inputStyles.Cursor.Shape = tea.CursorBar
	m.ui.search.SetStyles(inputStyles)
	m.ui.spinner.Style = s.accent
	m.stylePicker()
	if m.progress != nil {
		m.progress.FullColor = lipgloss.Color(m.activeColor())
		m.progress.EmptyColor = lipgloss.Color(t.ProgressEmpty)
	}
	m.dirty = true
}

func (m *viewer) stylePicker() {
	s := m.styles
	m.ui.picker.Styles = filepicker.Styles{
		File:             s.dialogText,
		Directory:        s.dialogAccent,
		Symlink:          s.dialogAccent,
		DisabledFile:     s.dialogMuted,
		DisabledSelected: s.dialogMuted,
		DisabledCursor:   s.dialogMuted,
		Selected:         s.selected,
		Cursor:           s.dialogAccent,
		Permission:       s.dialogMuted,
		FileSize:         s.dialogMuted,
		EmptyDirectory:   s.dialogMuted.SetString("No files in this directory"),
	}
}
