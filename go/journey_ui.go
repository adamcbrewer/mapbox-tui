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
	ref                         ItemRef
	row, stopID                 int
	session                     string
	sessionStarted              time.Time
	sessionRequests             int
	query                       string
	suggestions                 []placeSuggestion
	result                      int
	searchID                    uint64
	searchCancel                context.CancelFunc
	searchBusy                  bool
	showRevision                *uint64
	mapFocused                  bool
	menuStart, menuY, menuCount int
	menuDialog                  dialogKind
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

var journeyActions = [...]struct{ title, key, action string }{
	{title: "Travel mode", key: "ctrl+b", action: "mode"},
	{title: "Add waypoint", key: "ctrl+n", action: "waypoint"},
	{title: "Reverse journey", key: "ctrl+r", action: "reverse"},
	{title: "Undo edit", key: "ctrl+z", action: "undo"},
	{title: "Recalculate route", key: "ctrl+d", action: "calculate"},
	{title: "Routing feedback / usage", key: "ctrl+u", action: "feedback"},
	{title: "Show journey on map", action: "show"},
}

func (m *viewer) openJourney() tea.Cmd {
	if m.workspace.selection != nil {
		ref := m.workspace.selection.ref
		if _, err := m.workspace.journey(ref); err == nil && ref != m.planner.ref {
			m.cancelPlaceSearch()
			m.planner = journeyUI{ref: ref, searchID: m.planner.searchID}
		}
	}
	if _, err := m.workspace.journey(m.planner.ref); err != nil {
		m.cancelPlaceSearch()
		m.planner = journeyUI{
			ref: m.workspace.newJourney(), searchID: m.planner.searchID,
		}
	}
	if err := m.workspace.selectItem(m.planner.ref); err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	m.selectionChanged()
	cmd := m.pushDialog(dialogJourney)
	j, _ := m.workspace.journey(m.planner.ref)
	if m.defaultJourneyMode(j) {
		return tea.Batch(cmd, m.calculateJourneyIfRouted())
	}
	return cmd
}

func (m *viewer) cancelPlaceSearch() {
	if m.planner.searchCancel != nil {
		m.planner.searchCancel()
		m.planner.searchCancel = nil
	}
	m.planner.searchID++
	m.planner.searchBusy = false
	m.planner.suggestions = nil
	m.planner.result = 0
}

func (m *viewer) editJourneyLocation(index int) tea.Cmd {
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil || index < 0 || index >= len(j.Stops) {
		return nil
	}
	m.cancelPlaceSearch()
	m.planner.showRevision = nil
	m.planner.stopID = j.Stops[index].ID
	m.planner.row = index
	m.planner.session = rand.Text()
	m.planner.sessionStarted, m.planner.sessionRequests = time.Time{}, 0
	m.planner.query = ""
	m.pushDialog(dialogJourneyLocation)
	m.ui.inputID = rand.Text()
	m.ui.search.CharLimit = 256
	m.ui.search.Placeholder = "Place or latitude, longitude"
	m.ui.search.SetValue("")
	if location := j.Stops[index].Location; location != nil {
		lat := strconv.FormatFloat(location.Lat, 'f', -1, 64)
		lon := strconv.FormatFloat(location.Lon, 'f', -1, 64)
		m.ui.search.SetValue(lat + ", " + lon)
	}
	m.layout()
	m.dirty = true
	return m.ui.search.Focus()
}

func (m *viewer) schedulePlaceSearch() tea.Cmd {
	m.cancelPlaceSearch()
	m.planner.query = strings.TrimSpace(m.ui.search.Value())
	m.ui.notice = ""
	_, coordinate, _ := coordinateLocation(m.planner.query)
	searchAvailable := m.places.token != "" && len([]rune(m.planner.query)) >= 3
	if !searchAvailable || coordinate || coordinatePrefix(m.planner.query) {
		return nil
	}
	id := m.planner.searchID
	return tea.Tick(350*time.Millisecond, func(time.Time) tea.Msg { return placeSearchReady{id: id} })
}

func (m *viewer) requestPlaceSearch(id uint64) tea.Cmd {
	current := id == m.planner.searchID && m.placeSearchOpen() && m.places.token != ""
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

func (m *viewer) retrievePlaceLocation() tea.Cmd {
	if time.Since(m.planner.sessionStarted) >= 170*time.Second {
		return m.schedulePlaceSearch()
	}
	suggestion := m.planner.suggestions[m.planner.result]
	m.cancelPlaceSearch()
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

func (m *viewer) acceptPlaceSearch(result placeSearchResult) tea.Cmd {
	current := result.id == m.planner.searchID && result.ref == m.planner.ref && result.stopID == m.planner.stopID
	if !current || !m.placeSearchOpen() {
		return nil
	}
	if m.planner.searchCancel != nil {
		m.planner.searchCancel()
		m.planner.searchCancel = nil
	}
	m.planner.searchBusy = false
	m.dirty = true
	if result.err != nil {
		m.ui.notice = result.err.Error()
		return nil
	}
	m.ui.notice = ""
	if result.location != nil {
		return m.applyPlaceLocation(*result.location)
	}
	m.planner.suggestions = result.suggestions
	m.planner.result = 0
	if len(result.suggestions) == 0 {
		m.ui.notice = "No matching places; try a town/country or coordinates"
	}
	return nil
}

func (m *viewer) applyJourneyLocation(location placeLocation) tea.Cmd {
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	revision := j.revision
	if err := m.workspace.setJourneyLocation(m.planner.ref, m.planner.stopID, location); err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	if err := m.workspace.selectItem(m.planner.ref); err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	if m.ui.dialog == dialogJourneyLocation {
		m.backDialog()
	}
	m.ui.notice = ""
	m.planner.mapFocused = false
	m.selectionChanged()
	if revision == j.revision {
		return nil
	}
	m.fitPlannerJourney(m.planner.ref)
	return m.calculateJourneyIfRouted()
}

func (m *viewer) updateJourneyDialog(msg tea.Msg) tea.Cmd {
	key, isKey := msg.(tea.KeyPressMsg)
	if !isKey {
		if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
			return m.clickJourneyPanelRow(click.X, click.Y)
		}
		if wheel, ok := msg.(tea.MouseWheelMsg); ok {
			code := tea.KeyDown
			if wheel.Button == tea.MouseWheelUp {
				code = tea.KeyUp
			}
			if m.ui.dialog == dialogJourney || m.ui.dialog == dialogJourneyRouting {
				return m.updateJourneyDialog(tea.KeyPressMsg{Code: code})
			}
		}
		if m.ui.dialog == dialogJourneyLocation {
			return m.updatePlaceSearchInput(msg)
		}
		if m.ui.dialog == dialogJourneyRoutingHelp {
			var cmd tea.Cmd
			m.ui.page, cmd = m.ui.page.Update(msg)
			return cmd
		}
		return nil
	}
	pressed := key.String()
	if m.ui.dialog == dialogJourneyRouting || m.ui.dialog == dialogJourneyRoutingHelp {
		return m.updateJourneyRoutingDialog(pressed, msg)
	}
	if m.ui.dialog == dialogJourneyLocation {
		return m.updatePlaceLocation(pressed, msg)
	}
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		m.closeDialog()
		return nil
	}
	count := len(j.Stops)
	revision := j.revision
	last := count + len(journeyActions) - 1
	m.planner.row = max(0, min(last, m.planner.row))
	switch pressed {
	case "esc", "ctrl+j":
		return m.backDialog()
	case "up", "k":
		m.planner.row = menuStep(m.planner.row, -1, last+1)
	case "down", "j":
		m.planner.row = menuStep(m.planner.row, 1, last+1)
	case "home":
		m.planner.row = 0
	case "end":
		m.planner.row = last
	case "enter":
		if m.planner.row < count {
			return m.editJourneyLocation(m.planner.row)
		}
		pressed = journeyActions[m.planner.row-count].action
	}
	switch pressed {
	case "ctrl+b", "mode":
		return m.openJourneyRouting()
	case "ctrl+d", "calculate":
		return m.calculateJourneyRoute()
	case "ctrl+u", "feedback":
		return m.pushDialog(dialogJourneyRoutingHelp)
	case "ctrl+n", "waypoint":
		err = m.workspace.addJourneyWaypoint(m.planner.ref)
		m.planner.row = len(j.Stops) - 2
	case "ctrl+r", "reverse":
		err = m.workspace.reverseJourney(m.planner.ref)
		m.planner.row = 0
	case "ctrl+z", "undo":
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
	case "show":
		return m.showJourneyOnMap()
	}
	if err != nil {
		m.ui.notice = err.Error()
	} else {
		m.ui.notice = ""
	}
	if err == nil && revision != j.revision {
		m.selectionChanged()
		m.fitPlannerJourney(m.planner.ref)
		return m.calculateJourneyIfRouted()
	}
	return nil
}

func (m *viewer) updatePlaceLocation(pressed string, msg tea.Msg) tea.Cmd {
	switch pressed {
	case "esc":
		return m.backDialog()
	case "ctrl+s":
		if m.places.token == "" {
			m.ui.notice = "Place search unavailable · connect Mapbox in settings"
			return nil
		}
		return m.schedulePlaceSearch()
	case "alt+m":
		geo := unproject(m.camera.Center)
		return m.applyPlaceLocation(placeLocation{
			Name: fmt.Sprintf("%.6f, %.6f", geo.Y, geo.X), Lon: geo.X, Lat: geo.Y, Source: "map centre",
		})
	case "up", "down":
		if len(m.planner.suggestions) > 0 {
			delta := 1
			if pressed == "up" {
				delta = -1
			}
			m.planner.result = menuStep(m.planner.result, delta, len(m.planner.suggestions))
		}
		return nil
	case "enter":
		location, matched, err := coordinateLocation(m.ui.search.Value())
		if err != nil {
			m.ui.notice = err.Error()
			return nil
		}
		if matched {
			return m.applyPlaceLocation(location)
		}
		if len(m.planner.suggestions) > 0 && !m.planner.searchBusy {
			return m.retrievePlaceLocation()
		}
		m.ui.notice = "Enter latitude, longitude"
		if m.places.token != "" {
			m.ui.notice = "Type a place (3+ characters), choose a match, or enter latitude, longitude"
		}
		return nil
	}
	return m.updatePlaceSearchInput(msg)
}

func (m *viewer) updatePlaceSearchInput(msg tea.Msg) tea.Cmd {
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
	if m.ui.dialog == dialogJourneyRouting || m.ui.dialog == dialogJourneyRoutingHelp {
		return m.journeyRoutingDialogContent(width, height)
	}
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		return "Journey Planner", err.Error(), "esc close"
	}
	if m.ui.dialog == dialogJourneyLocation {
		body, hints := m.placeSearchContent(width, height)
		return journeyStopLabel(m.planner.row, len(j.Stops)), body, hints
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
		title := action.title
		if action.key == "ctrl+b" {
			title += ": " + j.Mode.label()
		}
		if action.key != "" {
			title += " (" + action.key + ")"
		}
		labels = append(labels, title)
	}
	m.planner.row = max(0, min(len(labels)-1, m.planner.row))
	rows := []string{}
	if height > 3 {
		entry, i, _ := m.workspace.lookup(m.planner.ref)
		status := journeyRoutingStatus(j, entry.document.Items[i].Track)
		if j.routeCurrent() {
			status = strings.TrimPrefix(status, j.Mode.label()+" · ")
		} else if j.Mode == journeyManual {
			status = fmt.Sprintf("%.2f km · straight-line", entry.document.Items[i].Track.Distance/1000)
		}
		rows = append(rows, s.dialogMuted.Render(pad(status, width)))
		if height > 5 {
			rows = append(rows, s.dialogMuted.Render(pad("del remove · alt+up/down order", width)))
		}
	}
	count := max(1, height-len(rows))
	start := max(0, m.planner.row-count+1)
	m.planner.menuDialog = dialogJourney
	m.planner.menuStart, m.planner.menuY = start, len(rows)
	m.planner.menuCount = min(len(labels)-start, count)
	for i := start; i < min(len(labels), start+count); i++ {
		style, prefix := s.dialogText, "  "
		if i == m.planner.row {
			style, prefix = s.selected, "> "
		}
		rows = append(rows, style.Render(pad(prefix+labels[i], width)))
	}
	return "Journey Planner", strings.Join(rows, "\n"), "ctrl+b mode / ctrl+u feedback / esc map"
}

func (m *viewer) journeyDetails() []string {
	j, err := m.workspace.journey(m.workspace.selection.ref)
	if err != nil {
		return nil
	}
	lines := []string{
		"Journey plan", "", journeyRoutingStatus(j, m.activeTrack()),
		"ctrl+j to edit", "",
	}
	if j.routeCurrent() {
		lines = []string{
			"Journey plan", "", "Mapbox " + j.Mode.label() + " roads/paths",
			fmt.Sprintf("%.2f km routed", j.Route.Distance/1000),
			"Estimated travel time", journeyDuration(j.Route.Duration),
			"No recorded timestamps", "ctrl+j to edit", "",
		}
		if j.Busy {
			lines = append(lines, "Recalculating route...", "")
		}
	} else if j.Mode == journeyManual {
		lines = append(lines, "Manual straight-line geometry", "No estimated travel time", "")
	}
	if j.Error != "" {
		lines = append(lines, j.Error, "ctrl+u in planner: feedback", "")
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
			if j.routeCurrent() {
				snapped := j.Route.Snapped[i]
				lines = append(lines, fmt.Sprintf("Road/path: %.6f, %.6f", snapped.Y, snapped.X))
			}
		}
		lines = append(lines, "")
	}
	return append(lines, "Session only; no locations saved")
}

func (m *viewer) journeyCredits(existing ...sourceCredit) string {
	var credits []string
	addCredit := func(credit string) {
		if credit == "" || slices.Contains(credits, credit) {
			return
		}
		if slices.ContainsFunc(existing, func(c sourceCredit) bool { return c.Text == credit }) {
			return
		}
		credits = append(credits, credit)
	}
	addCredit(m.searchCredit)
	if m.placeSearchOpen() {
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
		if item.Item.Journey.routeCurrent() {
			for _, credit := range strings.Split(directionsCredits, "\n") {
				addCredit(credit)
			}
		}
	}
	return strings.Join(credits, "\n")
}
