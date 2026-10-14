package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var panelViews = []struct {
	name string
	kind dialogKind
}{
	{name: "Context", kind: dialogNone},
	{name: "Files", kind: dialogWorkspace},
	{name: "Journey", kind: dialogJourney},
}

func (m *viewer) panelVisible() bool {
	switch {
	case m.journeyPanelOpen():
		return m.journeyPanelVisible()
	case m.ui.dialog == dialogWorkspace:
		return !m.ui.filesCollapsed
	default:
		return m.ui.detailsOpen
	}
}

func (m *viewer) togglePanel() tea.Cmd {
	m.dirty = true
	m.dragging, m.rotating = false, false
	switch {
	case m.journeyPanelOpen():
		if m.planner.pickingStop {
			m.planner.pickingStop = false
			m.planner.collapsed = false
		} else {
			m.planner.collapsed = !m.planner.collapsed
		}
		if m.ui.dialog == dialogJourneyLocation {
			if m.planner.collapsed {
				m.ui.search.Blur()
			} else {
				return m.ui.search.Focus()
			}
		}
	case m.ui.dialog == dialogWorkspace:
		m.ui.filesCollapsed = !m.ui.filesCollapsed
	default:
		m.ui.detailsOpen = !m.ui.detailsOpen
	}
	return nil
}

func (m *viewer) cyclePanel() tea.Cmd {
	return m.cyclePanelDirection(1)
}

func (m *viewer) cyclePanelDirection(direction int) tea.Cmd {
	if !m.panelVisible() {
		return m.togglePanel()
	}
	current := 0
	if m.journeyPanelOpen() {
		current = 2
	} else if m.ui.dialog == dialogWorkspace {
		current = 1
	}
	return m.switchPanel(panelViews[menuStep(current, direction, len(panelViews))].kind)
}

func (m *viewer) switchPanel(kind dialogKind) tea.Cmd {
	switch kind {
	case dialogJourney:
		if m.journeyPanelOpen() {
			m.planner.collapsed = false
			m.dirty = true
			if m.ui.dialog == dialogJourneyLocation && !m.planner.pickingStop {
				return m.ui.search.Focus()
			}
			return nil
		}
		return m.openJourney()
	case dialogWorkspace:
		if m.ui.dialog == dialogWorkspace {
			m.ui.filesCollapsed = false
			m.dirty = true
			return nil
		}
		return m.openDialog(dialogWorkspace)
	default:
		m.closeDialog()
		m.ui.detailsOpen = true
		return nil
	}
}

// The same border tabs anchor every panel view, including narrow terminals.
func (m *viewer) panelTabs(box string, active dialogKind) string {
	border, theme := m.boxBorder(), m.theme()
	frame := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Border)).Background(lipgloss.Color(theme.Surface))
	var tabs []string
	for _, view := range panelViews {
		style := m.styles.dialogMuted
		if view.kind == active {
			style = m.styles.dialogAccent.Bold(true).Underline(true)
		}
		tabs = append(tabs, style.Render(view.name))
	}
	label := strings.Join(tabs, m.styles.dialogMuted.Render(" · "))
	rows := strings.Split(box, "\n")
	remaining := max(0, lipgloss.Width(box)-3-ansi.StringWidth(label))
	tail := ""
	if remaining > 0 {
		tail = " " + strings.Repeat(border.Top, remaining-1)
	}
	rows[0] = frame.Render(border.TopLeft+" ") + label + frame.Render(tail+border.TopRight)
	return strings.Join(rows, "\n")
}

func (m *viewer) clickPanelTab(x, y int) (tea.Cmd, bool) {
	if !m.panelVisible() || y != m.mapOriginY() {
		return nil, false
	}
	left := m.journeyPanelX() + 2
	for _, view := range panelViews {
		if x >= left && x < left+len(view.name) {
			return m.switchPanel(view.kind), true
		}
		left += len(view.name) + 3
	}
	return nil, false
}

func (m *viewer) overlayFilesPanel() {
	if m.ui.dialog != dialogWorkspace || m.ui.filesCollapsed {
		return
	}
	width, height := m.journeyPanelWidth(), m.camera.Height
	inner := max(1, width-4)
	title, body, _ := m.workspaceDialogContent(inner, max(1, height-5))
	content := "\n" + m.styles.dialogHeading.Render(pad(title, inner)) + "\n" +
		lipgloss.NewStyle().Height(max(1, height-5)).MaxHeight(max(1, height-5)).Render(body) + "\n" +
		m.styles.dialogMuted.Render(pad(m.dialogHints("enter inspect / space hide / esc close"), inner))
	box := m.theme().borderStyle().Background(lipgloss.Color(m.theme().Surface)).
		Foreground(lipgloss.Color(m.theme().Text)).
		Border(m.boxBorder()).
		Padding(0, 1).Width(width).MaxHeight(height).Render(content)
	box = m.panelTabs(box, dialogWorkspace)
	m.content = lipgloss.NewCanvas(m.width, m.height).Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(m.content), lipgloss.NewLayer(box).X(m.journeyPanelX()).Y(m.mapOriginY()).Z(1),
	)).Render()
}

func (m *viewer) updateFilesPanelMouse(msg tea.Msg) tea.Cmd {
	switch event := msg.(type) {
	case tea.MouseWheelMsg:
		key := "down"
		if event.Button == tea.MouseWheelUp {
			key = "up"
		}
		return m.updateWorkspaceDialog(key)
	case tea.MouseClickMsg:
		if event.Button != tea.MouseLeft {
			return nil
		}
		count := max(1, m.camera.Height-8)
		start := max(0, m.ui.selected-count+1)
		row := event.Y - m.mapOriginY() - 3
		if row >= 0 && row < count && start+row < len(m.workspaceRows()) {
			m.ui.selected = start + row
			return m.updateWorkspaceDialog("enter")
		}
	}
	return nil
}
