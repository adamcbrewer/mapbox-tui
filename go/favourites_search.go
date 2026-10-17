package main

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type favouriteSearchResult struct {
	id       uint64
	owner    string
	location placeLocation
	err      error
}

func (m *viewer) savedSearchFavourite(id string) bool {
	return m.searchFavourite(id) != nil
}

func (m *viewer) searchFavourite(id string) *favourite {
	for i, entry := range m.favourites.entries {
		if entry.Source.mapboxGeocoded() && entry.ProviderID == id {
			return &m.favourites.entries[i]
		}
	}
	return nil
}

func (m *viewer) saveSearchFavourite() tea.Cmd {
	if m.planner.searchBusy {
		return nil
	}
	// Refresh before toggling so the selected entry reflects other viewers’ edits.
	m.ui.notice = ""
	m.ui.feedback = ""
	m.loadFavourites()
	if m.ui.notice != "" {
		return nil
	}
	suggestion := m.planner.suggestions[m.planner.result]
	if entry := m.searchFavourite(suggestion.ID); entry != nil {
		store, err := localFavouriteStore()
		if err == nil {
			err = store.change(entry, nil)
		}
		if err != nil {
			m.ui.notice = err.Error()
			return nil
		}
		m.loadFavourites()
		if m.ui.notice == "" {
			m.ui.feedback = "Favourite removed"
		}
		return nil
	}
	if suggestion.Type == "poi" || suggestion.Type == "category" {
		m.ui.notice = "Mapbox cannot save named venues; choose an address or town"
		return nil
	}
	m.planner.searchID++
	ctx, cancel := context.WithCancel(m.ctx)
	m.planner.searchCancel = cancel
	m.planner.searchBusy, m.planner.savingFavourite = true, true
	m.ui.feedback = "Saving favourite..."
	id, owner, client := m.planner.searchID, m.ui.inputID, m.places
	return func() tea.Msg {
		location, err := client.geocodedLocation(ctx, suggestion)
		return favouriteSearchResult{id: id, owner: owner, location: location, err: err}
	}
}

func (m *viewer) acceptSearchFavourite(result favouriteSearchResult) {
	current := result.id == m.planner.searchID && result.owner == m.ui.inputID && m.placeSearchOpen()
	if !current || !m.planner.savingFavourite {
		return
	}
	if m.planner.searchCancel != nil {
		m.planner.searchCancel()
		m.planner.searchCancel = nil
	}
	m.planner.searchBusy, m.planner.savingFavourite = false, false
	m.ui.feedback = ""
	m.dirty = true
	if result.err != nil {
		m.ui.notice = result.err.Error()
		return
	}
	store, err := localFavouriteStore()
	if err == nil {
		entry := newFavourite(result.location.Name, result.location)
		err = store.change(nil, &entry)
	}
	if err != nil {
		m.ui.notice = err.Error()
		return
	}
	m.ui.notice = ""
	m.loadFavourites()
	if m.ui.notice == "" {
		m.ui.feedback = "Favourite saved"
	}
}
