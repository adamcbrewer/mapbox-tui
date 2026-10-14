package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type startupJourney struct {
	Locations []placeLocation
	Mode      journeyMode
}

func parseStartupJourney(start, end string, waypoints []string, mode string) (*startupJourney, error) {
	if start == "" || end == "" {
		return nil, fmt.Errorf("a startup journey requires both --start and --end as latitude,longitude")
	}
	j := &startupJourney{Mode: journeyMode(mode)}
	switch mode {
	case "manual":
		j.Mode = journeyManual
	case "walking", "cycling", "driving":
	default:
		return nil, fmt.Errorf("unknown journey mode %q; choose manual, walking, cycling or driving", mode)
	}
	values := append([]string{start}, waypoints...)
	values = append(values, end)
	for i, value := range values {
		location, matched, err := coordinateLocation(value)
		if err != nil || !matched {
			return nil, fmt.Errorf("invalid %s coordinates %q; use latitude,longitude", journeyStopLabel(i, len(values)), value)
		}
		j.Locations = append(j.Locations, location)
	}
	return j, nil
}

func (m *viewer) startJourneyFromArgs() tea.Cmd {
	initial := m.args.Journey
	if initial == nil || !m.windowSizeKnown {
		return nil
	}
	m.args.Journey = nil
	m.openJourney()
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	stops := make([]journeyStop, len(initial.Locations))
	for i, location := range initial.Locations {
		stops[i] = journeyStop{ID: i + 1, Location: &location}
	}
	if err := m.workspace.editJourneyIntent(m.planner.ref, journeyIntent{Stops: stops, Mode: initial.Mode}); err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	j.nextID, j.modeChosen = len(stops), true
	j.history = nil
	if m.workspace.selection == nil {
		_ = m.workspace.selectItem(m.planner.ref)
	}
	m.planner.row = 0
	m.layout()
	m.fitPlannerJourney(m.planner.ref)
	m.dirty = true
	if j.Mode != journeyManual && m.directions.token == "" {
		m.startupJourneyRouting = true
		m.ui.notice = "Mapbox connection needed for routing"
		return nil
	}
	return m.calculateJourneyIfRouted()
}
