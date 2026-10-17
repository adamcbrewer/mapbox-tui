package main

import (
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNativeJourneyArguments(t *testing.T) {
	for _, mode := range []string{"offline", "manual"} {
		command, err := parseCLI([]string{"--mode=" + mode, "--start=1,2", "--end=3,4"}, io.Discard)
		if err != nil || command.Args.Journey == nil || command.Args.Journey.Mode != journeyManual {
			t.Fatal("offline journey mode or compatibility alias was rejected", mode, err)
		}
	}
	command, err := parseCLI([]string{"--mode=cycling", "--start=-33.123456789,18.1",
		"--end=-33.3,18.3", "--waypoint=-33.2,18.2", "--file=first.gpx", "--file=second.gpx"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	j := command.Args.Journey
	if j == nil || j.Mode != journeyCycling || len(j.Locations) != 3 ||
		j.Locations[0].Lat != -33.123456789 || j.Locations[1].Lon != 18.2 || len(command.Paths) != 2 {
		t.Fatal("native journey options lost stops, precision, mode or files", command)
	}
	for _, args := range [][]string{
		{"--start=1,2"}, {"--end=1,2"}, {"--mode=offline"}, {"--waypoint=1,2"},
		{"--start=91,0", "--end=1,2"}, {"--start=London", "--end=1,2"},
		{"--start=1,2", "--end=1,2", "--mode=flying"},
		{"render", "--start=1,2", "--end=1,2"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := parseCLI(args, io.Discard); err == nil {
				t.Fatal("invalid startup journey accepted")
			}
		})
	}
}

func TestNativeJourneyStartsAfterImportsAndOnlyOnce(t *testing.T) {
	m := journeyViewer(t)
	command, err := parseCLI([]string{"--mode=offline", "--start=51.3,-2.5", "--end=51.4,-2.6",
		"--waypoint=51.35,-2.55"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	m.args.Journey = command.Args.Journey
	m.pendingPaths = []string{"../fixtures/waypoints.gpx"}
	runLoadCommand(m, m.load("../fixtures/example.gpx"))
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.workspace.documents) != 3 || len(j.Stops) != 3 || j.Mode != journeyManual ||
		!j.modeChosen || !m.journeyPanelVisible() || m.workspace.selection.ref == m.planner.ref {
		t.Fatal("native startup lost imports, selection or journey state")
	}
	ref := m.planner.ref
	if cmd := m.startJourneyFromArgs(); cmd != nil || m.planner.ref != ref || len(m.workspace.documents) != 3 {
		t.Fatal("startup journey was created more than once")
	}
	if err := m.workspace.addJourneyWaypoint(ref); err != nil {
		t.Fatal(err)
	}
	if j.Stops[2].ID == j.Stops[3].ID {
		t.Fatal("new waypoint reused a startup stop ID")
	}
}

func TestNativeJourneyWaitsForCredentials(t *testing.T) {
	m := journeyViewer(t)
	initial, err := parseStartupJourney("51.3,-2.5", "51.4,-2.6", nil, "cycling")
	if err != nil {
		t.Fatal(err)
	}
	m.args.Journey = initial
	if cmd := m.startJourneyFromArgs(); cmd != nil || !m.startupJourneyRouting {
		t.Fatal("unauthenticated startup tried to route or failed to retain routing intent")
	}
	j, _ := m.workspace.journey(m.planner.ref)
	if !j.hasCompleteLocations() || j.Mode != journeyCycling || j.Busy {
		t.Fatal("startup lost offline journey geometry")
	}
	result := credentialResult{id: m.authID, credential: renderingCredential{token: "test-token"}}
	m.acceptCredentials(result)
	if m.startupJourneyRouting || !j.Busy || j.Mode != journeyCycling {
		t.Fatal("credentials did not start the requested route")
	}
	request := j.requestID
	m.acceptCredentials(result)
	if j.requestID != request {
		t.Fatal("credential refresh duplicated the route request")
	}
	m.workspace.cancelJourneyRoutes()
}

func TestMixedFileArgumentsPreserveOrder(t *testing.T) {
	for _, args := range [][]string{
		{"--file", "first.gpx", "second.gpx", "--file=third.gpx"},
		{"first.gpx", "--file=second.gpx", "third.gpx"},
		{"--file=first.gpx", "--", "second.gpx", "third.gpx"},
	} {
		command, err := parseCLI(args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(command.Paths, ",") != "first.gpx,second.gpx,third.gpx" {
			t.Fatalf("file order changed: %v", command.Paths)
		}
	}
}

func TestStartupJourneyWaitsForTerminalSize(t *testing.T) {
	for _, imported := range []bool{false, true} {
		m := journeyViewer(t)
		m.windowSizeKnown = false
		m.args.Journey, _ = parseStartupJourney("51.3,-2.5", "51.4,-2.6", nil, "manual")
		if imported {
			runLoadCommand(m, m.load("../fixtures/country-ride.gpx"))
		} else {
			m.Init()
		}
		if m.args.Journey == nil || m.journeyPanelOpen() {
			t.Fatal("journey started before terminal size arrived")
		}
		m.Update(tea.WindowSizeMsg{Width: 70, Height: 18})
		j, err := m.workspace.journey(m.planner.ref)
		if err != nil || !j.hasCompleteLocations() {
			t.Fatal("size message did not start journey", err)
		}
		before := m.camera
		m.fitPlannerJourney(m.planner.ref)
		if m.camera != before {
			t.Fatal("startup camera was not fitted to the actual terminal size")
		}
	}
}
