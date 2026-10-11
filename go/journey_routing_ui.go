package main

import (
	"fmt"
	"math"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

var journeyModes = []journeyMode{journeyManual, journeyWalking, journeyCycling, journeyDriving}

func (m *viewer) defaultJourneyMode(j *Journey) bool {
	if m.directions.token == "" || j.modeChosen || j.Mode != journeyManual {
		return false
	}
	j.Mode = journeyDriving
	j.invalidateRoute()
	return true
}

func (m *viewer) calculateJourneyIfRouted() tea.Cmd {
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		return nil
	}
	m.planner.showRevision = nil
	m.defaultJourneyMode(j)
	if j.Mode == journeyManual {
		return nil
	}
	if len(j.Stops) <= 25 {
		for _, stop := range j.Stops {
			if stop.Location == nil {
				return nil
			}
		}
	}
	return m.calculateJourneyRoute()
}

func (m *viewer) calculateJourneyRoute() tea.Cmd {
	request, err := m.workspace.beginJourneyRoute(m.ctx, m.planner.ref)
	m.dirty = true
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	m.ui.notice = ""
	client := m.directions
	return func() tea.Msg { return request.run(client) }
}

func (m *viewer) showJourneyOnMap() tea.Cmd {
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	m.defaultJourneyMode(j)
	pendingRoute := j.Busy || !j.routeCurrent()
	if j.Mode != journeyManual && pendingRoute {
		var cmd tea.Cmd
		if !j.Busy {
			cmd = m.calculateJourneyRoute()
			if cmd == nil {
				return nil
			}
		}
		revision := j.revision
		m.planner.showRevision = &revision
		m.ui.notice = "Calculating " + j.Mode.label() + " route..."
		m.dirty = true
		return cmd
	}
	if err := m.workspace.selectItem(m.planner.ref); err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	m.closeDialog()
	m.selectionChanged()
	track := m.activeTrack()
	if len(track.Points) > 0 {
		m.moveCamera(journeyView(j, track, m.camera))
	}
	return m.fetch()
}

func (m *viewer) openJourneyRouting() tea.Cmd {
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		return nil
	}
	cmd := m.pushDialog(dialogJourneyRouting)
	m.ui.selected = max(0, slices.Index(journeyModes, j.Mode))
	return cmd
}

func (m *viewer) updateJourneyRoutingDialog(pressed string, msg tea.Msg) tea.Cmd {
	if m.ui.dialog == dialogJourneyRoutingHelp {
		switch pressed {
		case "esc":
			return m.backDialog()
		case "home":
			m.ui.page.GotoTop()
			return nil
		case "end":
			m.ui.page.GotoBottom()
			return nil
		}
		var cmd tea.Cmd
		m.ui.page, cmd = m.ui.page.Update(msg)
		return cmd
	}
	last := len(journeyModes)
	switch pressed {
	case "esc":
		return m.backDialog()
	case "up", "k":
		m.ui.selected = max(0, m.ui.selected-1)
	case "down", "j":
		m.ui.selected = min(last, m.ui.selected+1)
	case "home":
		m.ui.selected = 0
	case "end":
		m.ui.selected = last
	case "ctrl+u":
		return m.pushDialog(dialogJourneyRoutingHelp)
	case "enter":
		if m.ui.selected == last {
			return m.pushDialog(dialogJourneyRoutingHelp)
		}
		mode := journeyModes[m.ui.selected]
		if mode != journeyManual && m.directions.token == "" {
			m.ui.notice = "Mapbox not connected; connect in settings or choose Manual; ctrl+u for details"
			return nil
		}
		j, err := m.workspace.journey(m.planner.ref)
		if err != nil {
			m.ui.notice = err.Error()
			return nil
		}
		revision := j.revision
		if err := m.workspace.setJourneyMode(m.planner.ref, mode); err != nil {
			m.ui.notice = err.Error()
			return nil
		}
		m.backDialog()
		m.ui.notice = ""
		m.selectionChanged()
		if revision == j.revision {
			return nil
		}
		return m.calculateJourneyIfRouted()
	}
	return nil
}

func (m *viewer) journeyRoutingDialogContent(width, height int) (string, string, string) {
	if m.ui.dialog == dialogJourneyRoutingHelp {
		var lines []string
		if j, err := m.workspace.journey(m.planner.ref); err == nil {
			entry, i, _ := m.workspace.lookup(m.planner.ref)
			lines = append(lines, journeyRoutingStatus(j, entry.document.Items[i].Track), "")
			if j.Error != "" {
				lines = append(lines, "Routing feedback:", j.Error, "")
			}
		}
		lines = append(lines,
			"Manual plots straight lines offline, without journey-time estimates.",
			"Driving is the default when Mapbox is connected. Walking, Cycling and Driving follow roads/paths using your selected token.",
			"Mapbox receives all ordered stop coordinates. Each calculation is one account request; usage may be billable.",
			"Routes recalculate after committed edits, reversal and undo. Typing and camera movement do not request routes.",
			"Standard and card-free trial accounts have different allowances/rate limits; map access does not prove Directions access.",
			"Current account limits: https://docs.mapbox.com/accounts/guides/signup/",
			"Current pricing: https://www.mapbox.com/pricing#directions-api",
			"Directions: https://docs.mapbox.com/api/navigation/directions/",
			"Limits: 2–25 locations; walking 1,000 km, cycling 1,500 km, driving 10,000 km between stops.",
			"Over-limit plans are rejected with every stop intact; they are never split or truncated.",
			"Rate limiting pauses further requests. Use Recalculate route after the wait; there are no automatic retries.",
			"Failed or stale routes use a manual preview. Choose Manual at any time; imported data stays usable.",
			"Estimated travel time is not a recorded timestamp. No live navigation or elevation is added.",
			"Routes stay in memory for this session only.",
		)
		m.ui.page.SetWidth(width)
		m.ui.page.SetHeight(max(1, height))
		m.ui.page.SetContent(strings.Join(lines, "\n\n"))
		return "Routing feedback / usage", m.ui.page.View(), "up/down scroll / esc close"
	}
	labels := []string{
		"Manual · offline", "Walking · Mapbox", "Cycling · Mapbox", "Driving · Mapbox", "Usage, limits and feedback",
	}
	if j, err := m.workspace.journey(m.planner.ref); err == nil {
		labels[slices.Index(journeyModes, j.Mode)] += " (current)"
	}
	var rows []string
	if height > len(labels)+2 {
		rows = append(rows,
			m.styles.dialogMuted.Render(pad("Mapbox receives stops; account usage may be billable", width)),
			m.styles.dialogMuted.Render(pad("Recalculates after edits · Manual works offline", width)),
		)
	}
	count := max(1, height-len(rows))
	start := max(0, m.ui.selected-count+1)
	m.planner.menuDialog = dialogJourneyRouting
	m.planner.menuStart, m.planner.menuY = start, len(rows)
	m.planner.menuCount = min(len(labels)-start, count)
	for i := start; i < min(len(labels), start+count); i++ {
		style, prefix := m.styles.dialogText, "  "
		if i == m.ui.selected {
			style, prefix = m.styles.selected, "> "
		}
		rows = append(rows, style.Render(pad(prefix+labels[i], width)))
	}
	return "Travel mode", strings.Join(rows, "\n"), "enter choose / ctrl+u usage / esc back"
}

func journeyRoutingStatus(j *Journey, track *Track) string {
	if j.Mode == journeyManual {
		return fmt.Sprintf("Manual · works offline | %.2f km plotted", track.Distance/1000)
	}
	if j.routeCurrent() {
		status := fmt.Sprintf(
			"%s · %.2f km · est %s",
			j.Mode.label(),
			j.Route.Distance/1000,
			journeyDuration(j.Route.Duration),
		)
		if j.Busy {
			status += " · recalculating"
		}
		return status
	}
	for _, stop := range j.Stops {
		if stop.Location == nil {
			if j.Route != nil {
				return j.Mode.label() + " · stale; set all locations"
			}
			return j.Mode.label() + " · set all locations to route"
		}
	}
	status := "manual preview; no estimate"
	switch {
	case j.Busy && j.Route != nil:
		status = "stale; calculating · manual preview"
	case j.Busy:
		status = "calculating; manual preview"
	case j.Route != nil:
		status = "stale route; manual preview"
	}
	return j.Mode.label() + " · " + status
}

func journeyDuration(seconds float64) string {
	minutes := math.Ceil(seconds / 60)
	if minutes < 60 {
		return fmt.Sprintf("%.0f min", minutes)
	}
	return fmt.Sprintf("%.0f h %.0f min", math.Floor(minutes/60), math.Mod(minutes, 60))
}
