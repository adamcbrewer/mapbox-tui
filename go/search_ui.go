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
	m.searchName = location.Name
	m.searchLocation = &location
	m.moveCamera(localView(XY{location.Lon, location.Lat}, m.destination()))
	return m.fetch()
}

func (m *viewer) placeSearchContent(width, height int) (string, string) {
	s := m.styles
	m.ui.search.SetWidth(max(1, width-2))
	rows := []string{m.ui.search.View()}
	hints := "enter choose / click map / esc"
	switch {
	case m.ui.dialog == dialogSearch && m.places.token == "":
		hints = "enter coordinates / esc cancel"
	case m.ui.dialog == dialogSearch:
		hints = "enter go / esc cancel"
	case m.places.token == "":
		hints = "enter coordinates / click map"
	}
	if height > 2 && len(m.planner.suggestions) == 0 {
		status := "Mapbox search · type a place"
		style := s.dialogAccent
		if m.places.token == "" {
			status = "Mapbox search [disabled: not connected]"
			style = s.dialogMuted
		}
		if m.planner.searchBusy {
			status = "Looking up location..."
			if m.planner.savingFavourite {
				status = "Saving favourite..."
			}
		}
		rows = append(rows, style.Render(pad(status, width)))
	}
	if height > 3 {
		hints = "ctrl+f favourites / alt+s toggle\n" + hints
	}
	count := max(1, height-len(rows))
	start := max(0, m.planner.result-count+1)
	for i := start; i < min(len(m.planner.suggestions), start+count); i++ {
		style := s.dialogText
		if i == m.planner.result {
			style = s.selected
		}
		suggestion := m.planner.suggestions[i]
		label := suggestion.Label
		if m.savedSearchFavourite(suggestion.ID) {
			marker := "★ "
			if m.settings.ASCII {
				marker = "* "
			}
			label = marker + label
		}
		rows = append(rows, style.Render(pad(label, width)))
	}
	return strings.Join(rows, "\n"), hints
}
