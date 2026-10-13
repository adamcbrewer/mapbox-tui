package main

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestJourneyCommandPreservesStopsModeAndFiles(t *testing.T) {
	for _, mode := range journeyModes {
		t.Run(mode.label(), func(t *testing.T) {
			m := journeyViewer(t)
			m.openJourney()
			j, _ := m.workspace.journey(m.planner.ref)
			j.Mode = mode
			j.Stops = []journeyStop{
				{ID: 1, Location: &placeLocation{Name: "Named start", Lat: -33.123456789, Lon: 18.1}},
				{ID: 3, Location: &placeLocation{Name: "Waypoint", Lat: -33.2, Lon: 18.2}},
				{ID: 2, Location: &placeLocation{Name: "Destination", Lat: -33.3, Lon: 18.3}},
			}
			command, err := m.journeyCommand()
			if err != nil {
				t.Fatal(err)
			}
			for _, part := range []string{
				"mapbox-tui ", "'--mode=" + strings.ToLower(mode.label()) + "'",
				"'--start=-33.123456789,18.1'",
				"'--waypoint=-33.2,18.2'", "'--end=-33.3,18.3'",
			} {
				if !strings.Contains(command, part) {
					t.Fatal("journey command lost journey intent", part, command)
				}
			}
			m.workspace.documents = append(m.workspace.documents, &workspaceDocument{
				document: &Document{Path: "/tmp/Adam's $(ride).gpx"},
			})
			command, err = m.journeyCommand()
			if err != nil || strings.Contains(command, "--no-file") ||
				!strings.Contains(command, "'--file=/tmp/Adam'\"'\"'s $(ride).gpx'") {
				t.Fatal("loaded path was lost or incorrectly quoted", command, err)
			}
			selection, camera := m.workspace.selection, m.camera
			m.planner.row = len(j.Stops) + len(journeyActions) - 1
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil || !reflect.DeepEqual(cmd(), tea.SetClipboard(command)()) {
				t.Fatal("Copy journey did not send the journey command to the terminal clipboard")
			}
			if m.workspace.selection != selection || m.camera != camera || m.ui.dialog != dialogJourney {
				t.Fatal("copying changed the journey view or selected file")
			}
		})
	}
}

func TestCopyJourneyRejectsMissingStops(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	if cmd := m.copyJourney(); cmd != nil || !strings.Contains(m.ui.notice, "Set A Start") {
		t.Fatal("incomplete journey was copied without an actionable message")
	}
}
