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
	rows[0] = frame.Render(border.TopLeft+border.Top+" ") +
		m.styles.dialogMuted.Render(prefix) + m.styles.dialogAccent.Bold(true).Render(label) +
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

func (m *viewer) contextFooter() []string {
	name, first, second := m.contextSummary()
	hints := "tab details · / search · ? help"
	if m.ui.detailsOpen {
		hints = "tab close · / search · ? help"
	}
	if m.width < 40 {
		hints = "tab details / search ? help"
		if m.ui.detailsOpen {
			hints = "tab close / search ? help"
		}
	}
	if m.loading || m.sources.busy() {
		second = m.ui.spinner.View() + " loading"
	} else if m.sources.view().status() != "" {
		second = "source status: v"
	}
	facts := first + " · " + second
	if m.width < 60 {
		return []string{
			m.styles.heading.Render(pad(name, m.width)),
			m.styles.text.Render(pad(facts, m.width)),
			m.styles.muted.Render(pad(hints, m.width)),
		}
	}
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

func (m *viewer) contextDrawerHeight() int {
	return min(m.camera.Height+2, max(8, min(16, m.height/3)))
}

func (m *viewer) contextDrawerY() int {
	return m.mapOriginY() + m.camera.Height + 1 - m.contextDrawerHeight()
}

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
	width := max(1, m.width-4)
	m.ui.details.SetWidth(width)
	m.ui.details.SetHeight(max(1, height-3))
	lines := m.contextDetails()
	// Keep status and diagnostics available without taking permanent map space.
	lines = append(lines, "", ansi.Strip(status))
	if len(m.workspace.items()) > 1 {
		lines = append(lines, ansi.Strip(m.workspaceLegend(width, m.visibleProfile())))
	}
	m.ui.details.Style = m.styles.dialogText
	m.ui.details.SetContent(strings.Join(lines, "\n"))
	body := m.ui.details.View() + "\n" + m.styles.dialogMuted.Render(pad("pgup/dn scroll · esc close", width))
	box := lipgloss.NewStyle().Background(lipgloss.Color(m.theme().Surface)).
		Foreground(lipgloss.Color(m.theme().Text)).
		Border(m.boxBorder()).BorderForeground(lipgloss.Color(m.theme().Border)).
		Padding(0, 1).Width(m.width).Render(body)
	name, _, _ := m.contextSummary()
	box = m.titleBox(box, name, nil)
	m.content = lipgloss.NewCanvas(m.width, m.height).Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(m.content),
		lipgloss.NewLayer(box).Y(m.contextDrawerY()).Z(1),
	)).Render()
}
