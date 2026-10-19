package main

import (
	"crypto/rand"
	"fmt"
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *viewer) workspaceNameInput(placeholder, value string) tea.Cmd {
	m.ui.inputID = rand.Text()
	m.ui.search.CharLimit = 256
	m.ui.search.Placeholder = placeholder
	m.ui.search.SetValue(value)
	m.ui.search.CursorEnd()
	return m.ui.search.Focus()
}

func (m *viewer) manageSavedWorkspace() tea.Cmd {
	target := m.savedWork.target
	store, err := localWorkspaceStore()
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	var result *workspaceCheckpoint
	switch m.savedWork.mode {
	case "rename":
		result, err = store.rename(target, m.ui.search.Value())
	case "duplicate":
		result, err = store.duplicate(target, m.ui.search.Value())
	case "delete":
		err = store.delete(target)
	}
	if err != nil {
		m.ui.notice = err.Error() + "; Esc returns to the list, R refreshes it."
		return nil
	}
	active := m.workspace.checkpoint
	if active != nil && active.path == target.path {
		switch m.savedWork.mode {
		case "rename":
			if active.digest == target.digest {
				m.workspace.checkpoint = result
				m.workspace.edited()
			}
		case "delete":
			m.workspace.detachCheckpoint()
		}
	}
	if result != nil {
		m.savedWork.anchor = result.path
	}
	m.savedWork.target = nil
	return m.loadWorkspaceLibrary()
}

func (m *viewer) workspaceManagementContent(input string, width, height int) (string, string, string) {
	target := m.savedWork.target
	identity := fmt.Sprintf("ID: %s\n%s", target.manifest.ID, target.manifest.Name)
	m.ui.page.SetWidth(max(1, width))
	m.ui.page.SetHeight(max(1, height))
	if m.savedWork.mode == "delete" {
		body := identity + "\n\nDelete this saved workspace?" +
			"\nThe current working copy and original import files stay available."
		m.ui.page.SetContent(ansi.Hardwrap(body, width, true))
		return "Delete saved workspace?", m.ui.page.View(), "enter delete • pgup/pgdown details • esc cancel"
	}
	title, explanation := "Rename saved workspace", "Only the saved name changes. Unsaved working edits stay in memory."
	if m.savedWork.mode == "duplicate" {
		title = "Duplicate saved workspace"
		explanation = "Copies the saved contents with a new ID. Unsaved working edits stay in memory."
	}
	m.ui.page.SetHeight(max(1, height-2))
	m.ui.page.SetContent(ansi.Hardwrap(identity+"\n\n"+explanation, width, true))
	return title, input + "\n\n" + m.ui.page.View(), "enter confirm • pgup/pgdown details • esc cancel"
}

func (m *viewer) startCleanWorkspace() tea.Cmd {
	if !m.workspace.modified(m.destination(), m.planner.ref) {
		return m.clearWorkspace()
	}
	m.openSavedWorkspaceDialog("unsaved")
	m.ui.search.Blur()
	m.savedWork.switchAction = "clear"
	m.savedWork.confirmSelected = 2
	m.savedWork.generation, m.savedWork.revision = m.workspace.liveIdentity(), m.workspace.liveRevision
	return nil
}

func (m *viewer) updateUnsavedWorkspaceDialog(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
		for i, bounds := range m.savedWork.confirmButtons {
			if image.Pt(click.X, click.Y).In(bounds) {
				m.savedWork.confirmSelected = i
				return m.updateUnsavedWorkspace([]string{"c", "d", "s"}[i])
			}
		}
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "left", "shift+tab", "up":
			m.savedWork.confirmSelected = menuStep(m.savedWork.confirmSelected, -1, 3)
		case "right", "tab", "down":
			m.savedWork.confirmSelected = menuStep(m.savedWork.confirmSelected, 1, 3)
		case "enter", "space":
			return m.updateUnsavedWorkspace([]string{"c", "d", "s"}[m.savedWork.confirmSelected])
		case "esc":
			return m.updateUnsavedWorkspace("c")
		default:
			return m.updateUnsavedWorkspace(key.String())
		}
	}
	return nil
}

func (m *viewer) unsavedWorkspaceContent(width int) (string, string, string) {
	labels := []string{"Cancel", "Discard changes", "Save"}
	rowWidth := 4
	for _, label := range labels {
		rowWidth += ansi.StringWidth(label) + 4
	}
	stack := rowWidth > width
	x, y := max(0, width-rowWidth), 0
	buttons := []string{}
	for i, label := range labels {
		style := m.styles.dialogText.Background(lipgloss.Color(m.theme().Selected)).Padding(0, 1)
		prefix := "  "
		if i == m.savedWork.confirmSelected {
			style = style.Background(lipgloss.Color(m.theme().Accent)).
				Foreground(lipgloss.Color(m.theme().Surface)).Bold(true)
			prefix = "> "
		}
		button := style.Render(prefix + label)
		buttonWidth := lipgloss.Width(button)
		m.savedWork.confirmButtons[i] = image.Rect(x, y, x+buttonWidth, y+1)
		buttons = append(buttons, button)
		if stack {
			x, y = 0, y+1
		} else {
			x += buttonWidth + 2
		}
	}
	separator, left := "  ", max(0, width-rowWidth)
	if stack {
		separator, left = "\n", 0
	}
	title := "Save before loading?"
	if m.savedWork.switchAction == "clear" {
		title = "Save before clearing?"
	}
	body := strings.Repeat(" ", left) + strings.Join(buttons, separator)
	hints := "tab/←/→ choose • enter select • esc cancel"
	if width < ansi.StringWidth(hints) {
		hints = "tab choose • enter select\nesc cancel"
	}
	return title, body, hints
}

var workspaceActions = []struct {
	label string
	key   rune
}{
	{label: "Rename saved workspace", key: 'n'},
	{label: "Duplicate saved workspace", key: 'd'},
	{label: "Delete saved workspace", key: tea.KeyDelete},
	{label: "Save current workspace", key: 's'},
	{label: "Save current workspace as", key: 'a'},
}

func (m *viewer) updateWorkspaceActions(key string) tea.Cmd {
	switch key {
	case "up", "k":
		m.savedWork.actionSelected = menuStep(m.savedWork.actionSelected, -1, len(workspaceActions))
	case "down", "j", "tab":
		m.savedWork.actionSelected = menuStep(m.savedWork.actionSelected, 1, len(workspaceActions))
	case "enter":
		action := workspaceActions[m.savedWork.actionSelected]
		m.savedWork.mode = "library"
		return m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: action.key})
	}
	return nil
}

func (m *viewer) workspaceActionsContent(width, height int) (string, string, string) {
	lines := []string{}
	count := max(1, height)
	start := max(0, m.savedWork.actionSelected-count+1)
	for i := start; i < min(len(workspaceActions), start+count); i++ {
		action := workspaceActions[i]
		style, prefix := m.styles.dialogText, "  "
		if i == m.savedWork.actionSelected {
			style, prefix = m.styles.selected, "> "
		}
		lines = append(lines, style.Render(pad(ansi.Truncate(prefix+action.label, width, "…"), width)))
	}
	return "Workspace actions", strings.Join(lines, "\n"), "↑/↓ choose • enter select • esc back"
}

func (m *viewer) updateUnsavedWorkspace(key string) tea.Cmd {
	switch key {
	case "s":
		name := ""
		if m.workspace.checkpoint != nil {
			name = m.workspace.checkpoint.manifest.Name
		}
		m.savedWork.mode = "save"
		return m.workspaceNameInput("Workspace name", name)
	case "d":
		if !m.savedOpenCurrent() {
			return nil
		}
		if m.savedWork.switchAction == "clear" {
			return m.clearWorkspace()
		}
		return m.commitSavedWorkspace()
	case "c":
		m.closeDialog()
	}
	return nil
}
