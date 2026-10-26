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

func (m *viewer) journeyPanelVisible() bool {
	return m.journeyPanelOpen() && !m.planner.collapsed && !m.planner.pickingStop
}

func (m *viewer) journeySidebar() bool {
	return m.journeyPanelVisible() && m.width >= 70
}

func (m *viewer) journeyPanelWidth() int {
	if m.width >= 70 {
		return min(50, max(42, m.width/3)) - 2
	}
	return m.width - 2
}

func (m *viewer) journeyPanelX() int {
	return m.width - m.journeyPanelWidth()
}

func (m *viewer) mapOriginY() int { return 0 }

func (m *viewer) journeyPanelY() int {
	if m.ui.dialog == dialogJourney || m.ui.dialog == dialogJourneyLocation || m.camera.Height < 5 {
		return m.mapOriginY()
	}
	return m.mapOriginY() + 2
}

func (m *viewer) journeyPanel() string {
	width, height := m.journeyPanelWidth(), m.camera.Height+1-m.journeyPanelY()
	innerWidth := max(1, width-4)
	showNotice := m.ui.notice != "" && height >= 5
	footerLines := 1
	if m.ui.dialog == dialogJourneyLocation && height >= 8 {
		footerLines = 2
	}
	if m.ui.dialog == dialogJourney {
		footerLines = min(3, max(0, height-3))
		if showNotice {
			footerLines = min(footerLines, max(0, height-4))
		}
	}
	if m.ui.feedback != "" && height >= 6 {
		footerLines++
	}
	showLocationHeading := m.ui.dialog == dialogJourneyLocation && height >= 8
	topPadding := 0
	if m.ui.dialog == dialogJourney || showLocationHeading {
		topPadding = 1
	}
	bodyHeight := max(1, height-2-footerLines-topPadding)
	if showNotice {
		bodyHeight = max(1, bodyHeight-1)
	}
	compactInput := m.ui.dialog == dialogJourneyLocation && bodyHeight < 2
	if compactInput {
		bodyHeight = 2
	}
	if showLocationHeading {
		bodyHeight = max(2, bodyHeight-1)
	}
	title, body, hints := m.journeyDialogContent(innerWidth, bodyHeight)
	if m.ui.dialog == dialogJourney {
		if j, err := m.workspace.journey(m.planner.ref); err == nil {
			if m.planner.row == len(j.Stops)+len(journeyActions) {
				hints = "enter add waypoint\n\ntab next / ctrl+b hide"
			} else if m.planner.row < len(j.Stops) {
				hints = "enter edit / ctrl+p pick\ndel remove / alt+up/down order\ntab next / ctrl+b hide"
			} else {
				hints = "enter run\ndel remove / alt+up/down order\ntab next / ctrl+b hide"
			}
		}
		if m.planner.collapsed {
			hints = "arrows pan / ctrl+b planner"
		}
	}
	if m.ui.dialog != dialogJourney {
		hints = m.dialogHints(hints)
	}
	var rows []string
	if topPadding > 0 {
		rows = append(rows, "")
	}
	if showLocationHeading {
		rows = append(rows, m.styles.dialogHeading.Render(pad(title, innerWidth)))
	}
	if showNotice {
		rows = append(rows, m.styles.dialogMuted.Render(pad(m.ui.notice, innerWidth)))
	}
	surface := lipgloss.Color(m.theme().Surface)
	rows = append(rows, lipgloss.NewStyle().Background(surface).Width(innerWidth).
		Height(bodyHeight).MaxHeight(bodyHeight).Render(body))
	if !compactInput {
		for i, hint := range m.dialogFooter(hints, innerWidth) {
			if i >= footerLines {
				break
			}
			rows = append(rows, hint)
		}
	}
	border := m.boxBorder()
	if m.ui.dialog == dialogJourneyLocation {
		if cursor := m.ui.search.Cursor(); cursor != nil {
			cursor.X += m.journeyPanelX() + 2
			cursor.Y += m.journeyPanelY() + 1 + topPadding
			if showLocationHeading {
				cursor.Y++
			}
			if showNotice {
				cursor.Y++
			}
			m.cursor = cursor
		}
	}
	box := m.theme().borderStyle().Background(surface).
		Border(border).
		Padding(0, 1).Width(width).MaxHeight(height).
		Render(strings.Join(rows, "\n"))
	if m.ui.dialog == dialogJourney || m.ui.dialog == dialogJourneyLocation {
		return m.panelTabs(box, dialogJourney)
	}
	return m.titleBox(box, title, m.dialogAncestors())
}

func (m *viewer) clickJourneyPanelRow(x, y int) tea.Cmd {
	if m.planner.menuDialog != m.ui.dialog {
		return nil
	}
	left := m.journeyPanelX() + 2
	if x < left || x >= left+m.journeyPanelWidth()-4 {
		return nil
	}
	row := y - m.journeyPanelY() - 1 - m.planner.menuY
	if m.ui.dialog == dialogJourney {
		row--
	}
	if m.ui.notice != "" && m.camera.Height+1-m.journeyPanelY() >= 5 {
		row--
	}
	if row < 0 || row >= m.planner.menuCount {
		return nil
	}
	index := m.planner.menuStart + row
	if m.ui.dialog == dialogJourney {
		if row >= len(m.planner.menuIndices) || m.planner.menuIndices[row] < 0 {
			return nil
		}
		index = m.planner.menuIndices[row]
	}
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

func (m *viewer) startJourneyPick() tea.Cmd {
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil || m.planner.row >= len(j.Stops) {
		return nil
	}
	m.cancelPlaceSearch()
	m.planner.stopID = j.Stops[m.planner.row].ID
	m.planner.pickingStop = true
	m.dragging, m.rotating = false, false
	m.ui.search.Blur()
	m.dirty = true
	return nil
}

func (m *viewer) pickJourneyLocation(location XY) (tea.Cmd, bool) {
	if !m.journeyPanelOpen() || !(m.planner.pickingStop || (m.ui.dialog == dialogJourneyLocation && !m.planner.collapsed)) {
		return nil, false
	}
	return m.applyJourneyLocation(placeLocation{
		Name: fmt.Sprintf("%.6f, %.6f", location.Y, location.X),
		Lon:  location.X, Lat: location.Y, Source: placeSourceMapPoint,
	}), true
}

func (m *viewer) overlayJourneyPanel() {
	if !m.journeyPanelVisible() {
		return
	}
	box := m.journeyPanel()
	layers := []*lipgloss.Layer{lipgloss.NewLayer(m.content)}
	if m.ui.dialog != dialogJourney && m.ui.dialog != dialogJourneyLocation {
		tabs := strings.Split(m.panelTabs(box, dialogJourney), "\n")[0]
		layers = append(layers,
			lipgloss.NewLayer(tabs).X(m.journeyPanelX()).Y(m.mapOriginY()).Z(1),
			lipgloss.NewLayer(lipgloss.NewStyle().Background(lipgloss.Color(m.theme().Surface)).
				Render(strings.Repeat(" ", m.journeyPanelWidth()))).
				X(m.journeyPanelX()).Y(m.mapOriginY()+1).Z(1))
	}
	layers = append(layers, lipgloss.NewLayer(box).X(m.journeyPanelX()).Y(m.journeyPanelY()).Z(1))
	m.content = lipgloss.NewCanvas(m.width, m.height).Compose(lipgloss.NewCompositor(layers...)).Render()
}

func (m *viewer) fitPlannerJourney(ref ItemRef) {
	if !m.journeyPanelVisible() || ref != m.planner.ref {
		return
	}
	entry, i, err := m.workspace.lookup(ref)
	if err != nil || len(entry.document.Items[i].Track.Points) == 0 {
		return
	}
	item := entry.document.Items[i]
	if revision := m.planner.preserveViewRevision; revision != nil && *revision == item.Journey.revision {
		return
	}
	m.setFollowing(false)
	viewport := m.camera
	if m.journeySidebar() {
		viewport.Width = max(1, m.journeyPanelX()-1)
	}
	target := journeyView(item.Journey, item.Track, viewport)
	// Fit within the exposed map while retaining the full camera dimensions.
	target.Width = m.camera.Width
	target.pan(float64(viewport.Width-target.Width), 0)
	m.moveCamera(target)
}

func journeyView(j *Journey, track *Track, viewport Camera) Camera {
	bounds := track.Bounds
	latitudeRadius := localViewRadiusM / 6371000 * 180 / math.Pi
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
	case "up", "down", "left", "right", "h", "j", "k", "l", "+", "=", "-", "f", "r", "c", "n", ",", ".", "a", "m":
		return true
	}
	return false
}
