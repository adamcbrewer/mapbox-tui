package main

import (
	"fmt"
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *viewer) journeyPanelOpen() bool {
	return m.ui.dialog >= dialogJourney
}

func (m *viewer) journeySidebar() bool {
	return m.journeyPanelOpen() && m.width >= 70
}

func (m *viewer) journeyPanelWidth() int {
	if m.journeySidebar() {
		return min(42, max(36, m.width/3))
	}
	return m.width
}

func (m *viewer) journeyStripHeight() int {
	if !m.journeyPanelOpen() || m.journeySidebar() {
		return 0
	}
	minimum := 5
	if m.ui.dialog == dialogJourneyLocation {
		minimum = 6
	}
	return min(8, max(minimum, m.height/3))
}

func (m *viewer) mapOriginY() int {
	return 2 + m.journeyStripHeight()
}

func (m *viewer) journeyPanel() string {
	width, height := m.journeyPanelWidth(), m.journeyStripHeight()
	if m.journeySidebar() {
		height = m.camera.Height + 2
	}
	innerWidth := max(1, width-4)
	bodyHeight := max(1, height-4)
	if m.ui.notice != "" {
		bodyHeight = max(1, bodyHeight-1)
	}
	compactInput := m.ui.dialog == dialogJourneyLocation && bodyHeight < 2
	if compactInput {
		bodyHeight = 2
	}
	title, body, hints := m.journeyDialogContent(innerWidth, bodyHeight)
	if m.ui.dialog == dialogJourney {
		if j, err := m.workspace.journey(m.planner.ref); err == nil {
			title += " | " + j.Mode.label()
			if m.planner.row < len(j.Stops) {
				hints = "click map / enter type / tab map"
			} else {
				hints = "enter run / tab map / esc close"
			}
		}
		if m.planner.mapFocused {
			hints = "arrows pan / tab planner"
		}
	}
	rows := []string{m.styles.dialogHeading.Render(pad(title, innerWidth))}
	if m.ui.notice != "" {
		rows = append(rows, m.styles.dialogMuted.Render(pad(m.ui.notice, innerWidth)))
	}
	surface := lipgloss.Color(m.theme().Surface)
	rows = append(rows, lipgloss.NewStyle().Background(surface).Width(innerWidth).
		Height(bodyHeight).MaxHeight(bodyHeight).Render(body))
	if !compactInput {
		rows = append(rows, m.styles.dialogMuted.Render(pad(hints, innerWidth)))
	}
	border := lipgloss.NormalBorder()
	if m.settings.ASCII {
		border = lipgloss.ASCIIBorder()
	}
	if m.ui.dialog == dialogJourneyLocation {
		if cursor := m.ui.search.Cursor(); cursor != nil {
			cursor.X += 2
			if m.journeySidebar() {
				cursor.X += m.camera.Width + 4
			}
			cursor.Y += 3
			if m.ui.notice != "" {
				cursor.Y++
			}
			m.cursor = cursor
		}
	}
	return lipgloss.NewStyle().Background(surface).
		Border(border).BorderForeground(lipgloss.Color(m.theme().Accent)).
		Padding(0, 1).Width(width).MaxHeight(height).
		Render(strings.Join(rows, "\n"))
}

func (m *viewer) clickJourneyPanelRow(x, y int) tea.Cmd {
	if m.planner.menuDialog != m.ui.dialog {
		return nil
	}
	left := 2
	if m.journeySidebar() {
		left += m.camera.Width + 4
	}
	if x < left || x >= left+m.journeyPanelWidth()-4 {
		return nil
	}
	row := y - 3 - m.planner.menuY
	if m.ui.notice != "" {
		row--
	}
	if row < 0 || row >= m.planner.menuCount {
		return nil
	}
	index := m.planner.menuStart + row
	switch m.ui.dialog {
	case dialogJourney:
		m.planner.row = index
		return m.updateJourneyDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	case dialogJourneyRouting:
		m.ui.selected = index
		return m.updateJourneyRoutingDialog("enter", tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	return nil
}

func (m *viewer) pickJourneyLocation(location XY) (tea.Cmd, bool) {
	switch m.ui.dialog {
	case dialogJourney:
		j, err := m.workspace.journey(m.planner.ref)
		if err != nil || m.planner.row >= len(j.Stops) {
			return nil, false
		}
		m.planner.stopID = j.Stops[m.planner.row].ID
	case dialogJourneyLocation:
	default:
		return nil, false
	}
	return m.applyJourneyLocation(placeLocation{
		Name: fmt.Sprintf("%.6f, %.6f", location.Y, location.X),
		Lon:  location.X, Lat: location.Y, Source: "map point",
	}), true
}

func (m *viewer) fitPlannerJourney(ref ItemRef) {
	if !m.journeyPanelOpen() || ref != m.planner.ref {
		return
	}
	entry, i, err := m.workspace.lookup(ref)
	if err != nil || len(entry.document.Items[i].Track.Points) == 0 {
		return
	}
	item := entry.document.Items[i]
	m.moveCamera(journeyView(item.Journey, item.Track, m.camera))
}

func journeyView(j *Journey, track *Track, viewport Camera) Camera {
	bounds := track.Bounds
	const radiusM = 1609.344
	latitudeRadius := radiusM / 6371000 * 180 / math.Pi
	for _, stop := range j.Stops {
		if stop.Location == nil {
			continue
		}
		location := stop.Location
		latitude := unproject(project(location.Lon, location.Lat)).Y
		longitudeRadius := latitudeRadius / math.Cos(latitude*math.Pi/180)
		bounds[0] = math.Min(bounds[0], location.Lon-longitudeRadius)
		bounds[1] = math.Min(bounds[1], latitude-latitudeRadius)
		bounds[2] = math.Max(bounds[2], location.Lon+longitudeRadius)
		bounds[3] = math.Max(bounds[3], latitude+latitudeRadius)
	}
	return fitBounds(
		bounds,
		viewport.Width,
		viewport.Height,
		viewport.Aspect,
	)
}

func journeyMapKey(key string) bool {
	switch key {
	case "up", "down", "left", "right", "h", "j", "k", "l", "+", "=", "-", "f", "r", "n", ",", ".", "a", "m":
		return true
	}
	return false
}
