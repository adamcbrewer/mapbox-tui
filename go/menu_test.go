package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMainMenuLabelsOrderAndToggleTicks(t *testing.T) {
	m := authTestViewer(t)
	m.provider.token, m.authVerified = "pk.checked", true
	wanted := map[string]string{
		"theme": "Colour theme", "traffic": "Toggle traffic",
		"motion": "Toggle animations ✓", "ascii": "Toggle ascii",
	}
	commands := m.commands()
	if last := commands[len(commands)-1]; last.action != "auth" || last.title != "Mapbox settings" {
		t.Fatal("Mapbox settings was not last in the main menu")
	}
	for _, command := range commands {
		if label, exists := wanted[command.action]; exists && command.label() != label {
			t.Fatalf("menu label %q, want %q", command.label(), label)
		}
		if command.action == "credits" || command.action == "inspect" || command.action == "fit" {
			t.Fatal("free-roam menu retained a relocated/removed command", command.title)
		}
		if strings.Contains(command.label(), "[") {
			t.Fatal("menu retained a checkbox prefix", command.label())
		}
		if command.action == "theme" && command.shortcut != "" {
			t.Fatal("theme command retained a keyboard shortcut")
		}
	}
	for _, query := range []string{"ascii", "animations"} {
		m.openDialog(dialogCommands)
		m.ui.search.SetValue(query)
		before := m.commands()[0].checked
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
		command := m.commands()[0]
		if command.checked == before || strings.HasSuffix(command.label(), " ✓") != command.checked {
			t.Fatal("Space did not update the trailing tick", command.label())
		}
		if m.ui.dialog != dialogCommands || m.ui.search.Value() != query {
			t.Fatal("Space closed or changed the menu filter")
		}
	}
	initial := m.settings.Theme
	m.openDialog(dialogCommands)
	m.runCommand("theme")
	if m.settings.Theme != initial || m.ui.dialog != dialogThemes || m.ui.selected != initial {
		t.Fatal("Colour theme did not open the picker at the current palette")
	}
}

func TestMapCreditsReturnsToMapboxSettings(t *testing.T) {
	m := authTestViewer(t)
	m.openDialog(dialogMapbox)
	m.authBusy = true
	for i, action := range m.mapboxActions() {
		if action.action == "credits" {
			m.ui.selected = i
		}
	}
	m.updateMapboxDialog("enter")
	if m.ui.dialog != dialogCredits {
		t.Fatal("Mapbox settings did not open its credits")
	}
	m.acceptCredentials(credentialResult{credential: renderingCredential{
		source: "disabled", dependency: mapboxDependencyStatus{State: "ready"},
	}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogMapbox || m.mapboxActions()[m.ui.selected].action != "credits" {
		t.Fatal("credits did not return to the selected settings action")
	}
	m.closeDialog()
	m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogNone {
		t.Fatal("direct credits shortcut unexpectedly returned to settings")
	}
}

func TestMainMenuGroupsTogglesBeforeMapboxSettings(t *testing.T) {
	for _, imported := range []bool{false, true} {
		m := authTestViewer(t)
		if imported {
			if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
				t.Fatal(err)
			}
		}
		var reachedToggles bool
		commands := m.commands()
		for _, command := range commands[:len(commands)-1] {
			if command.toggle {
				reachedToggles = true
				continue
			}
			if reachedToggles {
				t.Fatal("an action interrupted the toggle group", command.title)
			}
		}
		if !reachedToggles || commands[len(commands)-1].action != "auth" {
			t.Fatal("toggle group was missing or Mapbox settings was not last")
		}
	}
}

func TestMenuSearchSpacesDoNotToggleOptions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := authTestViewer(t)
	m.args.Provider, m.args.ProviderSet = "none", true
	m.provider.token, m.authVerified = "pk.checked", true
	m.openDialog(dialogCommands)
	for _, character := range "toggle ascii" {
		m.Update(tea.KeyPressMsg{Code: character, Text: string(character)})
	}
	if m.ui.search.Value() != "toggle ascii" || m.preferences.Traffic || m.settings.ASCII {
		t.Fatal("typing a search changed an option or swallowed its space", m.ui.search.Value())
	}
}

func TestMenuPlaybackSearchDoesNotTogglePlayback(t *testing.T) {
	m := authTestViewer(t)
	if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
		t.Fatal(err)
	}
	m.openDialog(dialogCommands)
	for _, character := range "play / pause" {
		m.Update(tea.KeyPressMsg{Code: character, Text: string(character)})
	}
	if m.ui.search.Value() != "play / pause" || m.activePlayback().Playing {
		t.Fatal("typing a complete playback search changed playback or swallowed its space", m.ui.search.Value())
	}
}
