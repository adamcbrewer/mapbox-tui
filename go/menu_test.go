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
		"theme": "Cycle Theme", "traffic": "Toggle traffic",
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
	}
	for _, query := range []string{"ascii", "animations"} {
		m.openDialog(dialogCommands)
		m.ui.search.SetValue(query)
		before := m.commands()[0].checked
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
	if m.settings.Theme != (initial+1)%len(m.themes) || m.ui.dialog != dialogNone {
		t.Fatal("Cycle Theme did not cycle the palette")
	}
}

func TestMapCreditsReturnsToMapboxSettings(t *testing.T) {
	m := authTestViewer(t)
	m.openDialog(dialogMapbox)
	for i, action := range m.mapboxActions() {
		if action.action == "credits" {
			m.ui.selected = i
		}
	}
	m.updateMapboxDialog("enter")
	if m.ui.dialog != dialogCredits || m.ui.creditsParent != dialogMapbox {
		t.Fatal("Mapbox settings did not open its credits")
	}
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
