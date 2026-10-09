package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type journeyUI struct {
	ref             ItemRef
	row, stopID     int
	online          bool
	session         string
	sessionStarted  time.Time
	sessionRequests int
	query           string
	suggestions     []placeSuggestion
	result          int
	searchID        uint64
	searchCancel    context.CancelFunc
	searchBusy      bool
}

type placeSearchReady struct{ id uint64 }
type placeSearchResult struct {
	id          uint64
	ref         ItemRef
	stopID      int
	suggestions []placeSuggestion
	location    *placeLocation
	err         error
}

var journeyActions = [...]struct{ title, key string }{
	{title: "Add waypoint", key: "ctrl+n"},
	{title: "Reverse journey", key: "ctrl+r"},
	{title: "Undo edit", key: "ctrl+z"},
	{title: "Show journey on map", key: "ctrl+f"},
}

func (m *viewer) openJourney() tea.Cmd {
	if m.workspace.selection != nil {
		ref := m.workspace.selection.ref
		if _, err := m.workspace.journey(ref); err == nil && ref != m.planner.ref {
			m.cancelJourneySearch()
			m.planner = journeyUI{ref: ref, online: m.planner.online, searchID: m.planner.searchID}
		}
	}
	if _, err := m.workspace.journey(m.planner.ref); err != nil {
		m.cancelJourneySearch()
		m.planner = journeyUI{
			ref: m.workspace.newJourney(), online: m.planner.online, searchID: m.planner.searchID,
		}
	}
	if err := m.workspace.selectItem(m.planner.ref); err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	m.selectionChanged()
	return m.openDialog(dialogJourney)
}

func (m *viewer) cancelJourneySearch() {
	if m.planner.searchCancel != nil {
		m.planner.searchCancel()
		m.planner.searchCancel = nil
	}
	m.planner.searchID++
	m.planner.searchBusy = false
	m.planner.suggestions = nil
	m.planner.result = 0
}

func journeyStopLabel(index, count int) string {
	if index == 0 {
		return "A Start"
	}
	if index == count-1 {
		return "B Destination"
	}
	return fmt.Sprintf("%d Waypoint", index)
}

func (m *viewer) editJourneyLocation(index int) tea.Cmd {
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil || index < 0 || index >= len(j.Stops) {
		return nil
	}
	m.cancelJourneySearch()
	m.planner.stopID = j.Stops[index].ID
	m.planner.row = index
	m.planner.session = rand.Text()
	m.planner.sessionStarted, m.planner.sessionRequests = time.Time{}, 0
	m.planner.query = ""
	m.ui.dialog, m.ui.notice = dialogJourneyLocation, ""
	m.ui.inputID = rand.Text()
	m.ui.search.CharLimit = 256
	m.ui.search.Placeholder = "Place or latitude, longitude"
	m.ui.search.SetValue("")
	if location := j.Stops[index].Location; location != nil {
		lat := strconv.FormatFloat(location.Lat, 'f', -1, 64)
		lon := strconv.FormatFloat(location.Lon, 'f', -1, 64)
		m.ui.search.SetValue(lat + ", " + lon)
	}
	m.dirty = true
	return m.ui.search.Focus()
}

func (m *viewer) schedulePlaceSearch() tea.Cmd {
	m.cancelJourneySearch()
	m.planner.query = strings.TrimSpace(m.ui.search.Value())
	m.ui.notice = ""
	_, coordinate, _ := coordinateLocation(m.planner.query)
	canSearch := m.planner.online && len([]rune(m.planner.query)) >= 3 && !coordinate
	if !canSearch {
		return nil
	}
	id := m.planner.searchID
	return tea.Tick(350*time.Millisecond, func(time.Time) tea.Msg { return placeSearchReady{id: id} })
}

func (m *viewer) requestPlaceSearch(id uint64) tea.Cmd {
	current := id == m.planner.searchID && m.ui.dialog == dialogJourneyLocation && m.planner.online
	if !current || m.planner.searchBusy {
		return nil
	}
	now := time.Now()
	expired := !m.planner.sessionStarted.IsZero() && now.Sub(m.planner.sessionStarted) >= 170*time.Second
	if m.planner.session == "" || expired || m.planner.sessionRequests >= 49 {
		m.planner.session = rand.Text()
		m.planner.sessionStarted, m.planner.sessionRequests = time.Time{}, 0
	}
	if m.planner.sessionStarted.IsZero() {
		m.planner.sessionStarted = now
	}
	m.planner.sessionRequests++
	ctx, cancel := context.WithCancel(m.ctx)
	m.planner.searchCancel, m.planner.searchBusy = cancel, true
	ref, stopID := m.planner.ref, m.planner.stopID
	query, session, near := m.planner.query, m.planner.session, unproject(m.camera.Center)
	client := m.places
	m.dirty = true
	return func() tea.Msg {
		suggestions, err := client.suggest(ctx, query, session, near)
		return placeSearchResult{id: id, ref: ref, stopID: stopID, suggestions: suggestions, err: err}
	}
}

func (m *viewer) retrieveJourneyLocation() tea.Cmd {
	if time.Since(m.planner.sessionStarted) >= 170*time.Second {
		return m.schedulePlaceSearch()
	}
	suggestion := m.planner.suggestions[m.planner.result]
	m.cancelJourneySearch()
	ctx, cancel := context.WithCancel(m.ctx)
	m.planner.searchCancel, m.planner.searchBusy = cancel, true
	id, ref, stopID, session := m.planner.searchID, m.planner.ref, m.planner.stopID, m.planner.session
	m.planner.session = ""
	m.planner.sessionStarted, m.planner.sessionRequests = time.Time{}, 0
	client := m.places
	return func() tea.Msg {
		location, err := client.retrieve(ctx, suggestion, session)
		return placeSearchResult{id: id, ref: ref, stopID: stopID, location: &location, err: err}
	}
}

func (m *viewer) acceptPlaceSearch(result placeSearchResult) {
	current := result.id == m.planner.searchID && result.ref == m.planner.ref && result.stopID == m.planner.stopID
	if !current || m.ui.dialog != dialogJourneyLocation {
		return
	}
	if m.planner.searchCancel != nil {
		m.planner.searchCancel()
		m.planner.searchCancel = nil
	}
	m.planner.searchBusy = false
	m.dirty = true
	if result.err != nil {
		m.ui.notice = result.err.Error()
		return
	}
	if result.location != nil {
		m.applyJourneyLocation(*result.location)
		return
	}
	m.planner.suggestions = result.suggestions
	m.planner.result = 0
	if len(result.suggestions) == 0 {
		m.ui.notice = "No matching places; try a town/country or coordinates"
	}
}

func (m *viewer) applyJourneyLocation(location placeLocation) {
	if err := m.workspace.setJourneyLocation(m.planner.ref, m.planner.stopID, location); err != nil {
		m.ui.notice = err.Error()
		return
	}
	if err := m.workspace.selectItem(m.planner.ref); err != nil {
		m.ui.notice = err.Error()
		return
	}
	m.cancelJourneySearch()
	m.ui.search.Blur()
	m.ui.dialog, m.ui.notice = dialogJourney, ""
	m.selectionChanged()
}

func (m *viewer) updateJourneyDialog(msg tea.Msg) tea.Cmd {
	key, isKey := msg.(tea.KeyPressMsg)
	if !isKey {
		if m.ui.dialog == dialogJourneyLocation {
			return m.updateJourneySearchInput(msg)
		}
		if m.ui.dialog == dialogJourneySearch {
			var cmd tea.Cmd
			m.ui.page, cmd = m.ui.page.Update(msg)
			return cmd
		}
		return nil
	}
	pressed := key.String()
	if m.ui.dialog == dialogJourneySearch {
		return m.updateJourneyConsent(pressed, msg)
	}
	if m.ui.dialog == dialogJourneyLocation {
		return m.updateJourneyLocation(pressed, msg)
	}
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		m.closeDialog()
		return nil
	}
	count := len(j.Stops)
	last := count + len(journeyActions) - 1
	m.planner.row = max(0, min(last, m.planner.row))
	switch pressed {
	case "esc", "ctrl+g":
		m.closeDialog()
		return nil
	case "up", "k":
		m.planner.row = max(0, m.planner.row-1)
	case "down", "j":
		m.planner.row = min(last, m.planner.row+1)
	case "home":
		m.planner.row = 0
	case "end":
		m.planner.row = last
	case "enter":
		if m.planner.row < count {
			return m.editJourneyLocation(m.planner.row)
		}
		pressed = journeyActions[m.planner.row-count].key
	}
	switch pressed {
	case "ctrl+n":
		err = m.workspace.addJourneyWaypoint(m.planner.ref)
		m.planner.row = len(j.Stops) - 2
	case "ctrl+r":
		err = m.workspace.reverseJourney(m.planner.ref)
		m.planner.row = 0
	case "ctrl+z":
		err = m.workspace.undoJourney(m.planner.ref)
	case "delete", "backspace":
		if m.planner.row < count {
			err = m.workspace.removeJourneyLocation(m.planner.ref, m.planner.row)
			m.planner.row = min(m.planner.row, len(j.Stops)-1)
		}
	case "alt+up", "alt+down":
		direction := -1
		if pressed == "alt+down" {
			direction = 1
		}
		err = m.workspace.moveJourneyWaypoint(m.planner.ref, m.planner.row, direction)
		if err == nil {
			m.planner.row += direction
		}
	case "ctrl+f":
		m.closeDialog()
		if err := m.workspace.selectItem(m.planner.ref); err != nil {
			return nil
		}
		m.selectionChanged()
		track := m.activeTrack()
		if len(track.Points) > 0 {
			m.moveCamera(fit(
				track,
				m.camera.Width,
				m.camera.Height,
				m.camera.Aspect,
			))
		}
		return m.fetch()
	}
	if err != nil {
		m.ui.notice = err.Error()
	} else {
		m.ui.notice = ""
	}
	m.selectionChanged()
	return nil
}

func (m *viewer) updateJourneyConsent(pressed string, msg tea.Msg) tea.Cmd {
	if pressed == "esc" || pressed == "n" {
		m.ui.dialog = dialogJourneyLocation
		return m.ui.search.Focus()
	}
	if pressed == "y" {
		if m.places.token == "" {
			m.ui.notice = "Mapbox search disabled · account not connected"
			return nil
		}
		m.planner.online = true
		m.ui.dialog = dialogJourneyLocation
		return tea.Batch(m.ui.search.Focus(), m.schedulePlaceSearch())
	}
	var cmd tea.Cmd
	m.ui.page, cmd = m.ui.page.Update(msg)
	return cmd
}

func (m *viewer) updateJourneyLocation(pressed string, msg tea.Msg) tea.Cmd {
	switch pressed {
	case "esc":
		m.cancelJourneySearch()
		m.ui.search.Blur()
		m.ui.dialog, m.ui.notice = dialogJourney, ""
		return nil
	case "ctrl+s":
		if m.places.token == "" {
			m.ui.notice = "Place search unavailable · connect Mapbox in settings"
			return nil
		}
		if !m.planner.online {
			m.ui.search.Blur()
			return m.openDialog(dialogJourneySearch)
		}
		return m.schedulePlaceSearch()
	case "alt+m":
		geo := unproject(m.camera.Center)
		m.applyJourneyLocation(placeLocation{
			Name: fmt.Sprintf("%.6f, %.6f", geo.Y, geo.X), Lon: geo.X, Lat: geo.Y, Source: "map centre",
		})
		return nil
	case "up", "down":
		if len(m.planner.suggestions) > 0 {
			delta := 1
			if pressed == "up" {
				delta = -1
			}
			m.planner.result = max(0, min(len(m.planner.suggestions)-1, m.planner.result+delta))
		}
		return nil
	case "enter":
		location, matched, err := coordinateLocation(m.ui.search.Value())
		if err != nil {
			m.ui.notice = err.Error()
			return nil
		}
		if matched {
			m.applyJourneyLocation(location)
			return nil
		}
		if len(m.planner.suggestions) > 0 && !m.planner.searchBusy {
			return m.retrieveJourneyLocation()
		}
		m.ui.notice = "Enter latitude, longitude"
		if m.places.token != "" {
			m.ui.notice += " or use ctrl+s for Mapbox search"
		}
		return nil
	}
	return m.updateJourneySearchInput(msg)
}

func (m *viewer) updateJourneySearchInput(msg tea.Msg) tea.Cmd {
	before := m.ui.search.Value()
	var cmd tea.Cmd
	m.ui.search, cmd = m.ui.search.Update(msg)
	cmd = m.clipboardCommand(msg, cmd)
	if before != m.ui.search.Value() {
		return tea.Batch(cmd, m.schedulePlaceSearch())
	}
	return cmd
}

func (m *viewer) journeyDialogContent(width, height int) (string, string, string) {
	s := m.styles
	if m.ui.dialog == dialogJourneySearch {
		lines := []string{
			"Search addresses and named POIs with autocomplete.",
			"Mapbox receives your search text and map-centre bias.",
			"Uses your configured account; usage may be billable.",
			"Results stay in this session only.",
			"Or enter latitude, longitude offline.",
		}
		m.ui.page.SetWidth(width)
		m.ui.page.SetHeight(height)
		m.ui.page.SetContent(strings.Join(lines, "\n"))
		return "Enable Mapbox search?", m.ui.page.View(), "y allow / n skip"
	}
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		return "Plan journey", err.Error(), "esc close"
	}
	if m.ui.dialog == dialogJourneyLocation {
		m.ui.search.SetWidth(max(1, width-2))
		rows := []string{m.ui.search.View()}
		credit := ""
		if len(m.planner.suggestions) > 0 {
			credit = m.planner.suggestions[0].Attribution
		}
		hints := "enter choose / esc back"
		if m.places.token == "" {
			hints = "enter coordinates / alt+m centre / esc back"
		}
		if height <= 2 && credit != "" {
			hints = credit
		}
		if height > 2 {
			status := "ctrl+s Mapbox | alt+m map centre"
			style := s.dialogAccent
			if m.places.token == "" {
				status = "Mapbox search [disabled: not connected]"
				style = s.dialogMuted
			} else if m.planner.online {
				status = "Mapbox Search Box | lat, lon offline"
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
		return journeyStopLabel(m.planner.row, len(j.Stops)), strings.Join(rows, "\n"), hints
	}
	labels := []string{}
	for i, stop := range j.Stops {
		name := "(enter location)"
		if stop.Location != nil {
			name = stop.Location.Name
		}
		labels = append(labels, journeyStopLabel(i, len(j.Stops))+": "+name)
	}
	for _, action := range journeyActions {
		labels = append(labels, action.title+" ("+action.key+")")
	}
	m.planner.row = max(0, min(len(labels)-1, m.planner.row))
	rows := []string{}
	if height > 3 {
		entry, i, _ := m.workspace.lookup(m.planner.ref)
		distance := entry.document.Items[i].Track.Distance / 1000
		status := fmt.Sprintf("Manual · works offline | %.2f km", distance)
		rows = append(rows, s.dialogMuted.Render(pad(status, width)))
		if height > 5 {
			rows = append(rows, s.dialogMuted.Render(pad("del clear/remove | alt+up/down reorder", width)))
		}
	}
	count := max(1, height-len(rows))
	start := max(0, m.planner.row-count+1)
	for i := start; i < min(len(labels), start+count); i++ {
		style, prefix := s.dialogText, "  "
		if i == m.planner.row {
			style, prefix = s.selected, "> "
		}
		rows = append(rows, style.Render(pad(prefix+labels[i], width)))
	}
	return "Plan journey", strings.Join(rows, "\n"), "enter edit/run / esc map"
}

func (m *viewer) journeyDetails() []string {
	j, err := m.workspace.journey(m.workspace.selection.ref)
	if err != nil {
		return nil
	}
	lines := []string{
		"Journey plan", "", "Manual straight-line geometry",
		fmt.Sprintf("%.2f km plotted", m.activeTrack().Distance/1000),
		"No road routing or travel time", "ctrl+g to edit", "",
	}
	if m.inspected != nil {
		lines = append(lines,
			"Selected position",
			fmt.Sprintf("Lat %.6f", m.inspected.Y),
			fmt.Sprintf("Lon %.6f", m.inspected.X),
			"",
		)
	}
	for i, stop := range j.Stops {
		name := "Not set"
		if stop.Location != nil {
			name = stop.Location.Name
		}
		lines = append(lines, journeyStopLabel(i, len(j.Stops)), name)
		if stop.Location != nil {
			lines = append(lines, fmt.Sprintf("%.6f, %.6f", stop.Location.Lat, stop.Location.Lon))
		}
	}
	return append(lines, "", "Session only; no locations saved")
}

func (m *viewer) journeyCredits() string {
	var credits []string
	addCredit := func(credit string) {
		if credit != "" && !slices.Contains(credits, credit) {
			credits = append(credits, credit)
		}
	}
	if m.ui.dialog == dialogJourneyLocation {
		for _, suggestion := range m.planner.suggestions {
			addCredit(suggestion.Attribution)
		}
	}
	for _, item := range m.workspace.items() {
		if !item.Visible || item.Item.Journey == nil {
			continue
		}
		for _, stop := range item.Item.Journey.Stops {
			if stop.Location != nil {
				addCredit(stop.Location.Attribution)
			}
		}
	}
	return strings.Join(credits, "\n")
}
