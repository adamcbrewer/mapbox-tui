package main

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *viewer) boxBorder() lipgloss.Border {
	if m.settings.ASCII {
		return lipgloss.ASCIIBorder()
	}
	return lipgloss.RoundedBorder()
}

func (m *viewer) dialogAncestors() []string {
	names := map[dialogKind]string{
		dialogCommands: "Commands", dialogFiles: "Open track file",
		dialogHelp: "Keyboard shortcuts", dialogCredits: "Map credits",
		dialogSpeed: "Playback speed", dialogInspect: "Selected position",
		dialogWorkspace: "Loaded files", dialogThemes: "Colour theme",
		dialogMapbox: "Mapbox", dialogMapboxHelp: "Installation",
		dialogSearch: "Quick search", dialogJourney: "Journey Planner",
		dialogJourneyLocation: "Location", dialogJourneyRouting: "Routing",
		dialogJourneyRoutingHelp: "Routing help",
		dialogFavourites:         "Favourites", dialogFavouriteName: "Favourite name",
		dialogFavouriteDelete: "Delete favourite",
	}
	var ancestors []string
	for _, parent := range m.ui.parents {
		ancestors = append(ancestors, names[parent.kind])
	}
	return ancestors
}

// titleBox replaces only the top border, so widget and mouse coordinates stay stable.
func (m *viewer) titleBox(box, title string, ancestors []string) string {
	rows := strings.Split(box, "\n")
	width := lipgloss.Width(box)
	available := max(1, width-6)
	separator, ellipsis := " › ", "…"
	if m.settings.ASCII {
		separator, ellipsis = " > ", "..."
	}
	label := ansi.Truncate(title, available, ellipsis)
	prefix := strings.Join(ancestors, separator)
	if prefix != "" {
		prefix += separator
		room := available - ansi.StringWidth(label)
		for len(ancestors) > 1 && ansi.StringWidth(prefix) > room {
			ancestors = ancestors[1:]
			prefix = strings.Join(ancestors, separator) + separator
		}
		if ansi.StringWidth(prefix) > room {
			prefix = ellipsis + separator
		}
		if ansi.StringWidth(prefix) > room {
			// Keep a textual nesting marker even when the current title is long.
			label = ansi.Truncate(title, max(1, available-ansi.StringWidth(prefix)), ellipsis)
		}
	}
	border := m.boxBorder()
	t := m.theme()
	frame := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Border)).Background(lipgloss.Color(t.Surface))
	remaining := max(0, width-5-ansi.StringWidth(prefix+label))
	titleText := m.styles.dialogAccent.Bold(true).Render(label)
	if len(t.BorderGradient) > 0 {
		titleText = m.gradient(label, lipgloss.Color(t.Surface))
	}
	rows[0] = frame.Render(border.TopLeft+border.Top+" ") +
		m.styles.dialogMuted.Render(prefix) + titleText +
		frame.Render(" "+strings.Repeat(border.Top, remaining)+border.TopRight)
	return strings.Join(rows, "\n")
}

func (m *viewer) contextSummary() (string, string, string) {
	if m.inspected != nil {
		name := "Selected position"
		if m.inspected == m.searchPosition && m.searchName != "" {
			name = cleanPlaceText(m.searchName)
		}
		return name, fmt.Sprintf("Lat %.5f", m.inspected.Y), fmt.Sprintf("Lon %.5f", m.inspected.X)
	}
	if track := m.activeTrack(); track != nil {
		if len(track.Points) == 0 && len(track.Waypoints) > 0 {
			point := track.Waypoints[0]
			return cleanPlaceText(track.Name), fmt.Sprintf("Lat %.5f", point.Lat), fmt.Sprintf("Lon %.5f", point.Lon)
		}
		fact := fmt.Sprintf("%d points", len(track.Points))
		if p := m.activePlayback(); p != nil {
			fact = strings.Fields(p.status())[0] + fmt.Sprintf(" %.0fx", p.multiplier())
		} else if j, err := m.workspace.journey(m.workspace.selection.ref); err == nil {
			fact = j.Mode.label()
			if j.routeCurrent() {
				fact = journeyDuration(j.Route.Duration)
			}
		}
		return cleanPlaceText(track.Name), fmt.Sprintf("%.2f km", track.Distance/1000), fact
	}
	return "Free roam", fmt.Sprintf("Zoom %.2f", m.camera.Zoom),
		fmt.Sprintf("Bearing %.0f°", math.Mod(m.camera.Bearing+360, 360))
}

func (m *viewer) contextSummaryRows() []string {
	name, first, second := m.contextSummary()
	action := "sidebar"
	if m.ui.detailsOpen {
		action = "hide"
	}
	separator := " · "
	if m.width < 40 {
		separator = " "
	}
	shortcuts := []string{"/ search", "? help"}
	switch {
	case m.ui.dialog == dialogWorkspace:
		action = "hide"
		if m.ui.filesCollapsed {
			action = "files"
		}
		separator = " · "
		shortcuts = []string{"esc close", "? help"}
		if m.width < 40 {
			shortcuts = shortcuts[:1]
		}
	case m.journeyPanelOpen():
		action = "hide"
		if m.planner.collapsed {
			action = "planner"
		}
	}
	hints := "ctrl+b " + action + separator + strings.Join(shortcuts, separator)
	if m.loading || m.sources.busy() {
		second = m.ui.spinner.View() + " loading"
	} else if m.sources.view().status() != "" {
		second = "source status: v"
	}
	facts := first + " · " + second
	theme := "Theme: " + m.theme().Name
	if m.width < 60 {
		theme = ansi.Truncate(m.theme().Name, m.width-6, "…")
		// Keep the leading context fact visible even with a long build label.
		factsRoom := min(ansi.StringWidth(first), m.width/2)
		buildVersion := ansi.Truncate(version, min(24, m.width-factsRoom-2), "…")
		facts = pad(facts, m.width-ansi.StringWidth(buildVersion)-2) + "  " + buildVersion
		room := m.width - ansi.StringWidth(theme) - 2
		title := m.styles.heading.Render(pad(ansi.Truncate(name, room, "…"), room)) +
			"  " + m.styles.muted.Render(theme)
		return []string{
			title,
			m.styles.text.Render(pad(facts, m.width)),
			m.styles.muted.Render(pad(hints, m.width)),
		}
	}
	hintsRoom := m.width
	if m.width >= 110 {
		// Reserve the facts, four name cells and the two gaps on the shared row.
		hintsRoom -= ansi.StringWidth(facts) + 9
	}
	versionRoom := min(24, max(0, hintsRoom-ansi.StringWidth(hints+separator+separator)-2))
	versionLabel := separator + ansi.Truncate(version, versionRoom, "…")
	themeRoom := min(24, hintsRoom-ansi.StringWidth(hints+separator+versionLabel))
	hints += separator + ansi.Truncate(theme, max(0, themeRoom), "…") + versionLabel
	if m.width < 110 {
		// Split the line on small screens; hints must never be clipped by a long name.
		room := max(4, m.width-ansi.StringWidth(facts)-3)
		line := m.styles.heading.Render(ansi.Truncate(name, room, "…")) + " · " + m.styles.text.Render(facts)
		return []string{pad(line, m.width), m.styles.muted.Render(pad(hints, m.width))}
	}
	room := max(4, m.width-ansi.StringWidth(facts+hints)-6)
	line := m.styles.heading.Render(ansi.Truncate(name, room, "…")) + " · " + m.styles.text.Render(facts)
	return []string{pad(line, m.width-ansi.StringWidth(hints)-2) + "  " + m.styles.muted.Render(hints)}
}

func (m *viewer) contextDrawerHeight() int { return m.camera.Height }

func (m *viewer) contextDrawerY() int { return m.mapOriginY() }

func (m *viewer) contextDetails() []string {
	lines := m.details()
	section := "Selected position"
	if m.inspected == nil && m.activeTrack() == nil {
		section = "Map center"
	}
	for start, line := range lines {
		if line != section {
			continue
		}
		end := start + 1
		for end < len(lines) && lines[end] != "" {
			end++
		}
		// Lead with the current position; route metadata and diagnostics remain scrollable.
		ordered := append([]string{}, lines[start:end]...)
		ordered = append(ordered, "")
		ordered = append(ordered, lines[:start]...)
		return append(ordered, lines[end:]...)
	}
	return lines
}

func (m *viewer) contextDrawer(status string) {
	height := m.contextDrawerHeight()
	width := max(1, m.journeyPanelWidth()-4)
	m.ui.details.SetWidth(width)
	m.ui.details.SetHeight(max(1, height-5))
	lines := m.contextDetails()
	// Keep status and diagnostics available without taking permanent map space.
	lines = append(lines, "", ansi.Strip(status))
	if len(m.workspace.items()) > 1 {
		lines = append(lines, ansi.Strip(m.workspaceLegend(width, m.visibleProfile())))
	}
	m.ui.details.Style = m.styles.dialogText
	m.ui.details.SetContent(ansi.Wrap(strings.Join(lines, "\n"), width, ""))
	name, _, _ := m.contextSummary()
	body := "\n" + m.styles.dialogHeading.Render(pad(name, width)) + "\n" + m.ui.details.View() + "\n" + m.styles.dialogMuted.Render(pad("pgup/dn scroll · esc close", width))
	box := m.theme().borderStyle().Background(lipgloss.Color(m.theme().Surface)).
		Foreground(lipgloss.Color(m.theme().Text)).
		Border(m.boxBorder()).
		Padding(0, 1).Width(m.journeyPanelWidth()).Render(body)
	box = m.panelTabs(box, dialogNone)
	m.content = lipgloss.NewCanvas(m.width, m.height).Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(m.content),
		lipgloss.NewLayer(box).X(m.journeyPanelX()).Y(m.contextDrawerY()).Z(1),
	)).Render()
}

func (m *viewer) hasJourneyContext() bool {
	_, err := m.workspace.journey(m.planner.ref)
	return err == nil
}

func (m *viewer) contextFooter() []string {
	rows := m.contextSummaryRows()
	if !m.hasJourneyContext() {
		return rows
	}
	j, _ := m.workspace.journey(m.planner.ref)
	label, action := "Journey", "ctrl+j edit"
	if m.journeyPanelOpen() {
		action = "esc close"
		if len(m.ui.parents) > 0 {
			action = "esc back"
		}
	}
	if m.planner.pickingStop {
		label = "Pick " + journeyStopLabel(m.planner.row, len(j.Stops))
		action = "click/enter · esc cancel"
	} else if m.width >= 60 {
		entry, index, _ := m.workspace.lookup(m.planner.ref)
		label += " · " + journeyRoutingStatus(j, entry.document.Items[index].Track)
	}
	room := max(1, m.width-ansi.StringWidth(action)-2)
	line := m.styles.accent.Render(pad(ansi.Truncate(label, room, "…"), room)) +
		"  " + m.styles.muted.Render(action)
	return append(rows, pad(line, m.width))
}
