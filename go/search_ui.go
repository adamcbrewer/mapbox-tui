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
	defer m.recordViewChange(m.destination())
	m.closeDialog()
	m.setFollowing(false)
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
	access := m.preferences.productAccess(m.credential.token)
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
			status = "Place search unavailable: not connected"
			if access == mapboxOffline {
				status = "Place search unavailable while offline"
			}
			if access == mapboxNeedsConsent {
				status = "Place search paused: review billing risks"
			}
			style = s.dialogMuted
		}
		if m.planner.searchBusy {
			status = "Looking up location..."
			if m.planner.savingFavourite {
				status = "Saving favourite..."
			}
		}
		rows = append(rows, style.Render(pad(status, width)))
		if m.places.token == "" && height > 3 {
			rows = append(rows, s.dialogMuted.Render(pad("Enter latitude, longitude coordinates", width)))
			if height > 4 {
				guidance := "Connect Mapbox in Mapbox settings"
				if access == mapboxOffline {
					guidance = "Go online in Mapbox settings"
				}
				if access == mapboxNeedsConsent {
					guidance = "Review billing risks in Mapbox settings"
				}
				rows = append(rows, s.dialogMuted.Render(pad(guidance, width)))
			}
		}
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
