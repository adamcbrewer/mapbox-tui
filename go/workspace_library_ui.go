package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type workspaceLibraryReady struct {
	request uint64
	entries []workspaceListing
	err     error
}

func (m *viewer) loadWorkspaceLibrary() tea.Cmd {
	m.savedWork.request++
	request := m.savedWork.request
	m.savedWork.mode, m.savedWork.busy = "library", true
	m.savedWork.entries, m.savedWork.candidate = nil, nil
	m.savedWork.selected = 0
	m.ui.search.Blur()
	m.ui.notice = "Reading saved workspaces..."
	return func() tea.Msg {
		store, err := localWorkspaceStore()
		if err != nil {
			return workspaceLibraryReady{request: request, err: err}
		}
		entries, err := store.list()
		return workspaceLibraryReady{request: request, entries: entries, err: err}
	}
}

func (m *viewer) acceptWorkspaceLibrary(result workspaceLibraryReady) tea.Cmd {
	if result.request != m.savedWork.request || m.ui.dialog != dialogSavedWorkspace || m.savedWork.mode != "library" {
		return nil
	}
	m.savedWork.busy = false
	m.dirty = true
	m.ui.notice = ""
	if result.err != nil {
		m.ui.notice = result.err.Error()
		return nil
	}
	m.savedWork.entries = result.entries
	return m.selectLibraryWorkspace(0)
}

func (m *viewer) selectLibraryWorkspace(index int) tea.Cmd {
	m.savedWork.request++
	m.savedWork.candidate = nil
	m.savedWork.busy = false
	m.ui.notice = ""
	m.ui.page.GotoTop()
	if len(m.savedWork.entries) == 0 {
		return nil
	}
	m.savedWork.selected = index
	entry := m.savedWork.entries[index]
	if entry.problem != "" {
		m.ui.notice = entry.problem
		return nil
	}
	return nil
}

func (m *viewer) updateWorkspaceLibrary(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.savedWork.entries)
	if m.savedWork.busy && count > 0 {
		return nil
	}
	switch msg.String() {
	case "up", "k", "down", "j":
		if count == 0 {
			return nil
		}
		step := 1
		if msg.String() == "up" || msg.String() == "k" {
			step = -1
		}
		return m.selectLibraryWorkspace(menuStep(m.savedWork.selected, step, count))
	case "enter":
		if count > 0 && m.savedWork.entries[m.savedWork.selected].problem == "" {
			return m.beginSavedOpen(m.savedWork.entries[m.savedWork.selected].path)
		}
	case "tab":
		m.savedWork.request++
		m.savedWork.busy = false
		return m.browseSavedWorkspaces()
	case "p":
		m.savedWork.request++
		m.savedWork.mode, m.savedWork.busy = "open", false
		m.ui.notice = ""
		return m.ui.search.Focus()
	case "r":
		return m.loadWorkspaceLibrary()
	case "home":
		m.ui.page.GotoTop()
	case "end":
		m.ui.page.GotoBottom()
	default:
		var cmd tea.Cmd
		m.ui.page, cmd = m.ui.page.Update(msg)
		return cmd
	}
	return nil
}

func (m *viewer) workspaceLibraryContent(width, height int) (string, string, string) {
	hints := "↑/↓ select • enter open • tab files • p path • r refresh"
	count := len(m.savedWork.entries)
	if count == 0 {
		body := "No saved workspaces yet.\nUse Save workspace to create one, or Tab to choose a file."
		if m.savedWork.busy {
			body = "Reading saved workspaces..."
		}
		return "Open saved workspace", ansi.Hardwrap(body, width, true), hints + " • esc cancel"
	}
	rows := min(count, max(1, min(5, height/4)))
	start := max(0, min(m.savedWork.selected-rows/2, count-rows))
	lines := []string{fmt.Sprintf("Saved workspaces (%d/%d)", m.savedWork.selected+1, count)}
	for i := start; i < start+rows; i++ {
		entry := m.savedWork.entries[i]
		prefix := "  "
		if i == m.savedWork.selected {
			prefix = "> "
		}
		name := entry.name
		if entry.problem != "" {
			name += " (unavailable)"
		}
		lines = append(lines, ansi.Truncate(prefix+name, width, ""))
	}
	entry := m.savedWork.entries[m.savedWork.selected]
	details := entry.preview.pickerDetails(width, m.settings.ASCII)
	if entry.problem != "" {
		details = "Cannot open this save.\n" + cleanPlaceText(entry.problem)
	}
	m.ui.page.SetWidth(max(1, width))
	m.ui.page.SetHeight(max(1, height-len(lines)-1))
	m.ui.page.SetContent(details)
	body := strings.Join(lines, "\n") + "\n\n" + m.ui.page.View()
	return "Open saved workspace", body, hints + "\npgup/pgdown details • esc keep current"
}
