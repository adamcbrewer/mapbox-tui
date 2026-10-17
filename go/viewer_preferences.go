package main

import "strings"

// Viewer preferences never enter Args or Workspace: static rendering and workspace
// lifecycle operations keep their own defaults and state.
func (m *viewer) initViewerPreferences(saved appSettings) (int, error) {
	m.settings = viewerSettings{ASCII: m.args.ASCII, NoAnimation: m.args.NoAnimation}
	if saved.ASCII != nil && !m.args.ASCIISet {
		m.settings.ASCII = *saved.ASCII
	}
	if saved.NoAnimation != nil && !m.args.NoAnimationSet {
		m.settings.NoAnimation = *saved.NoAnimation
	}
	choice := m.args.Theme
	fromSaved := saved.Theme != "" && !m.args.ThemeSet
	if fromSaved {
		choice = saved.Theme
	}
	themes, selected, err := loadThemes(choice)
	if err != nil && fromSaved {
		m.settingsNotice = "Saved theme unavailable; using Catppuccin: " + err.Error()
		themes, selected, err = loadThemes("")
	}
	if err != nil {
		return 0, err
	}
	m.themes = themes
	elevation := saved.Elevation
	if m.args.ElevationSet {
		elevation = m.args.Elevation
	}
	m.profileExplicit = elevation == "show" || elevation == "hide"
	m.showProfile = elevation == "show" || (!m.profileExplicit && m.workspace.hasVisibleElevation())
	return selected, nil
}

func (m *viewer) saveViewerPreference(change func(*appSettings)) bool {
	_, err := updateAppSettings(change)
	if err != nil {
		m.settingsNotice = "Preference not saved: " + err.Error()
	} else if strings.HasPrefix(m.settingsNotice, "Preference not saved:") {
		m.settingsNotice = ""
	}
	// A failed save still leaves the deliberate change usable for this session.
	// Do not reload other fields into the running viewer or persist launch overrides.
	m.dirty = true
	return err == nil
}

func (m *viewer) saveThemePreference() {
	choice := m.theme().ID
	if m.theme().Path != "" {
		choice = m.theme().Path
	}
	saved := m.saveViewerPreference(func(s *appSettings) { s.Theme = choice })
	if saved && strings.HasPrefix(m.settingsNotice, "Saved theme unavailable;") {
		m.settingsNotice = ""
	}
}
