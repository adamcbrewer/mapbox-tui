package main

import "fmt"

type elevationMode uint8

const (
	elevationAuto elevationMode = iota
	elevationShow
	elevationHide
)

func parseElevationMode(choice string) (elevationMode, error) {
	switch choice {
	case "", "auto":
		return elevationAuto, nil
	case "show":
		return elevationShow, nil
	case "hide":
		return elevationHide, nil
	default:
		return elevationAuto, fmt.Errorf("elevation must be auto, show or hide")
	}
}

// A saved show choice remains enabled even when the workspace has no elevation.
// Layout separately requires visible recorded elevation before reserving space.
func (m *viewer) profileEnabled() bool {
	return m.settings.Elevation == elevationShow ||
		(m.settings.Elevation == elevationAuto && m.workspace.hasVisibleElevation())
}

// Resolve saved choices and launch overrides only into viewer state. Shared Args
// and Workspace retain their own defaults and lifecycle.
func (m *viewer) initViewerPreferences(saved appSettings) (int, error) {
	m.savedView = saved.LastView
	m.camera = m.restoreStartupView(m.camera)
	m.settings = viewerSettings{ASCII: m.args.ASCII, NoAnimation: m.args.NoAnimation}
	if saved.ASCII != nil && !m.args.ASCIISet {
		m.settings.ASCII = *saved.ASCII
	}
	if saved.CameraAnimation != nil && !m.args.NoAnimationSet {
		m.settings.NoAnimation = !*saved.CameraAnimation
	}
	choice := m.args.Theme
	fromSaved := saved.Theme != "" && !m.args.ThemeSet
	if fromSaved {
		choice = saved.Theme
	}
	themes, selected, err := loadThemes(choice)
	if err != nil && fromSaved {
		m.themeNotice = "Saved theme unavailable; using Catppuccin: " + err.Error()
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
	m.settings.Elevation, err = parseElevationMode(elevation)
	return selected, err
}

func (m *viewer) saveViewerPreference(change func(*appSettings)) bool {
	_, err := updateAppSettings(change)
	if err != nil {
		m.settingsNotice = "Preference not saved: " + err.Error()
	} else {
		m.settingsNotice = ""
	}
	// A failed save still leaves the deliberate change usable for this session.
	// Do not reload other fields into the running viewer or persist launch overrides.
	m.dirty = true
	return err == nil
}

func (m *viewer) saveThemePreference() {
	theme := m.theme()
	choice := theme.ID
	if theme.Path != "" {
		choice = theme.Path
	}
	if m.saveViewerPreference(func(s *appSettings) { s.Theme = choice }) {
		m.themeNotice = ""
	}
}

func (m *viewer) preferenceNotice() string {
	if m.settingsNotice != "" {
		return m.settingsNotice
	}
	return m.themeNotice
}
