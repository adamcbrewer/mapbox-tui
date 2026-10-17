package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func preferenceViewer(t *testing.T, flags ...string) *viewer {
	t.Helper()
	command, err := parseCLI(append([]string{"--no-provider"}, flags...), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	m := &viewer{args: command.Args, ctx: t.Context(), provider: newProvider("none"), width: 120, height: 40}
	if err := m.initUI(); err != nil {
		t.Fatal(err)
	}
	m.layout()
	t.Cleanup(m.sources.close)
	return m
}

func settingsBytes(t *testing.T) []byte {
	t.Helper()
	path, err := appSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestViewerPreferencesRestartAndLaunchOverrides(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveAppSettings(appSettings{Traffic: true, MapboxCLITelemetry: true}); err != nil {
		t.Fatal(err)
	}
	m := preferenceViewer(t)
	m.toggleMenuOption("ascii")
	m.toggleMenuOption("motion")
	m.toggleMenuOption("profile")
	m.openDialog(dialogThemes)
	m.updateDialog(tea.KeyPressMsg{Code: tea.KeyDown})
	m.updateDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	before := settingsBytes(t)
	restarted := preferenceViewer(t)
	if !restarted.settings.ASCII || !restarted.settings.NoAnimation || !restarted.profileExplicit ||
		!restarted.showProfile || restarted.theme().ID != m.theme().ID {
		t.Fatal("viewer choices did not survive restart")
	}
	if restarted.profileHeight != 0 {
		t.Fatal("saved show reserved an empty chart")
	}
	override := preferenceViewer(t, "--ascii=false", "--no-animation=false", "--theme=paper", "--elevation=auto")
	if override.settings.ASCII || override.settings.NoAnimation || override.profileExplicit || override.theme().ID != "paper" {
		t.Fatal("explicit false/default overrides did not win")
	}
	if !bytes.Equal(before, settingsBytes(t)) {
		t.Fatal("launch overrides rewrote saved preferences")
	}
	// Save one deliberate change while all other values are overridden.
	override.toggleMenuOption("profile")
	saved, err := loadAppSettings()
	if err != nil || !*saved.ASCII || !*saved.NoAnimation || saved.Theme != m.theme().ID ||
		!saved.Traffic || !saved.MapboxCLITelemetry {
		t.Fatal("saving one preference leaked launch overrides or lost existing preferences", err)
	}
	override.clearWorkspace()
	if override.settings.ASCII || override.theme().ID != "paper" || !override.profileExplicit {
		t.Fatal("empty workspace reset global session preferences")
	}
	command, err := parseCLI([]string{"render", "--no-provider"}, io.Discard)
	if err != nil || command.Args.ASCII {
		t.Fatal("saved viewer preferences leaked into static rendering", err)
	}
}

func TestViewerThemePreviewPersistence(t *testing.T) {
	for _, dismiss := range []string{"escape", "replace", "close", "commands"} {
		t.Run(dismiss, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			m := preferenceViewer(t)
			before := m.theme().ID
			m.openDialog(dialogThemes)
			m.updateDialog(tea.KeyPressMsg{Code: tea.KeyDown})
			if m.theme().ID == before {
				t.Fatal("theme did not preview")
			}
			switch dismiss {
			case "escape":
				m.updateDialog(tea.KeyPressMsg{Code: tea.KeyEscape})
			case "replace":
				m.openDialog(dialogHelp)
			case "close":
				m.closeDialog()
			case "commands":
				m.pushDialog(dialogCommands)
			}
			if m.theme().ID != before {
				t.Fatal("dismissal did not restore theme")
			}
			path, _ := appSettingsPath()
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("preview or startup created settings", err)
			}
		})
	}
}

func TestViewerCustomThemePersistsAbsolutePathAndFallsBack(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	data, err := themeFiles.ReadFile("themes/catppuccin.toml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "palette.toml")
	data = bytes.ReplaceAll(data, []byte("catppuccin"), []byte("custom"))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	m := preferenceViewer(t, "--theme=palette.toml")
	m.openDialog(dialogThemes)
	m.updateDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	saved, err := loadAppSettings()
	if err != nil || saved.Theme != path {
		t.Fatal("custom theme path was not saved absolutely", saved.Theme, err)
	}
	t.Chdir(t.TempDir())
	if restarted := preferenceViewer(t); restarted.theme().ID != "custom" {
		t.Fatal("custom theme did not resolve from another working directory")
	}
	before := settingsBytes(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "not a palette", strings.Repeat("# comment\n", 7000)} {
		if invalid != "" {
			if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
				t.Fatal(err)
			}
		}
		restarted := preferenceViewer(t)
		if restarted.theme().ID != "catppuccin" || !strings.Contains(restarted.settingsNotice, "Saved theme unavailable") {
			t.Fatal("missing/invalid saved theme did not fall back with feedback")
		}
		restarted.rebuild()
		if !strings.Contains(restarted.content, "Saved theme unavailable") || !bytes.Equal(before, settingsBytes(t)) {
			t.Fatal("fallback warning invisible or fallback overwritten on disk")
		}
	}
	restarted := preferenceViewer(t)
	restarted.openDialog(dialogThemes)
	restarted.updateDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if restarted.settingsNotice != "" || preferenceViewer(t).theme().ID != "catppuccin" {
		t.Fatal("confirming the fallback did not replace the broken saved theme and clear its warning")
	}
}

func TestViewerSavedElevationLifecycle(t *testing.T) {
	flat := importFile(t, "flat.gpx", []byte(`<gpx><trk><trkseg><trkpt lat="51" lon="0"/><trkpt lat="51" lon="0.01"/></trkseg></trk></gpx>`))
	for _, mode := range []string{"", "auto", "show", "hide"} {
		t.Run("mode="+mode, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := saveAppSettings(appSettings{Elevation: mode}); err != nil {
				t.Fatal(err)
			}
			m := preferenceViewer(t)
			fullHeight := m.camera.Height
			if _, err := m.workspace.open(flat); err != nil {
				t.Fatal(err)
			}
			m.layout()
			if m.profileHeight != 0 || m.camera.Height != fullHeight {
				t.Fatal("unelevated activity reserved a chart")
			}
			ref, err := m.workspace.open("../fixtures/example.gpx")
			if err != nil {
				t.Fatal(err)
			}
			m.layout()
			if (m.visibleProfile() != nil) != (mode != "hide") {
				t.Fatal("saved elevation choice ignored on later load")
			}
			if err := m.workspace.removeDocument(ref.DocumentID); err != nil {
				t.Fatal(err)
			}
			m.layout()
			if m.profileHeight != 0 || m.camera.Height != fullHeight {
				t.Fatal("removing last elevation data left reserved chart space")
			}
		})
	}
}

func TestViewerStalePreferencesPreserveUnrelatedUpdates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first, second := preferenceViewer(t), preferenceViewer(t)
	first.toggleMenuOption("ascii")
	second.toggleMenuOption("motion")
	_, err := updateAppSettings(func(s *appSettings) { s.MapboxCLITelemetry, s.Traffic = true, true })
	if err != nil {
		t.Fatal(err)
	}
	first.toggleMenuOption("profile")
	saved, err := loadAppSettings()
	if err != nil || saved.ASCII == nil || !*saved.ASCII || saved.NoAnimation == nil || !*saved.NoAnimation ||
		saved.Elevation != "show" || !saved.MapboxCLITelemetry || !saved.Traffic {
		t.Fatal("stale viewer overwrote unrelated preferences", saved, err)
	}
}
