package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *viewer) placeSearchOpen() bool {
	return m.ui.dialog == dialogSearch || m.ui.dialog == dialogJourneyLocation
}

func (m *viewer) clearTransientInspection() {
	// Search owns its inspection while playback continues in the background.
	if m.inspected != m.searchPosition {
		m.inspected = nil
	}
}

func (m *viewer) applyPlaceLocation(location placeLocation) tea.Cmd {
	if m.ui.dialog != dialogSearch {
		return m.applyJourneyLocation(location)
	}
	m.closeDialog()
	m.following = false
	m.searchPosition = &XY{location.Lon, location.Lat}
	m.inspected = m.searchPosition
	m.searchCredit = location.Attribution
	m.moveCamera(localView(XY{location.Lon, location.Lat}, m.destination()))
	return m.fetch()
}

func (m *viewer) placeSearchContent(width, height int) (string, string) {
	s := m.styles
	m.ui.search.SetWidth(max(1, width-2))
	rows := []string{m.ui.search.View()}
	credit := ""
	if len(m.planner.suggestions) > 0 {
		credit = m.planner.suggestions[0].Attribution
	}
	hints := "enter choose / click map / esc"
	switch {
	case m.ui.dialog == dialogSearch && m.places.token == "":
		hints = "enter coordinates / esc cancel"
	case m.ui.dialog == dialogSearch:
		hints = "enter go / esc cancel"
	case m.places.token == "":
		hints = "enter coordinates / click map"
	}
	if height <= 2 && credit != "" {
		hints = credit
	}
	if height > 2 {
		status := "Mapbox search · type a place"
		style := s.dialogAccent
		if m.places.token == "" {
			status = "Mapbox search [disabled: not connected]"
			style = s.dialogMuted
		}
		if credit != "" {
			status = credit
		}
		if m.planner.searchBusy {
			status = "Looking up location..."
		}
		rows = append(rows, style.Render(pad(status, width)))
	}
	count := max(1, height-len(rows))
	start := max(0, m.planner.result-count+1)
	for i := start; i < min(len(m.planner.suggestions), start+count); i++ {
		style := s.dialogText
		if i == m.planner.result {
			style = s.selected
		}
		rows = append(rows, style.Render(pad(m.planner.suggestions[i].Label, width)))
	}
	return strings.Join(rows, "\n"), hints
}
