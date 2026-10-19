package main

import (
	"fmt"
	"slices"
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
	m.savedWork.allEntries = result.entries
	m.filterWorkspaceLibrary()
	return nil
}

func (m *viewer) filterWorkspaceLibrary() {
	anchor := m.savedWork.anchor
	entries := []workspaceListing{}
	query := strings.ToLower(strings.TrimSpace(m.savedWork.query))
	for _, entry := range m.savedWork.allEntries {
		if strings.Contains(strings.ToLower(entry.name), query) {
			entries = append(entries, entry)
		}
	}
	slices.SortFunc(entries, func(a, b workspaceListing) int {
		if !m.savedWork.sortName {
			if order := b.modified.Compare(a.modified); order != 0 {
				return order
			}
		}
		if order := strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)); order != 0 {
			return order
		}
		return strings.Compare(a.path, b.path)
	})
	m.savedWork.entries = entries
	selected := slices.IndexFunc(entries, func(entry workspaceListing) bool { return entry.path == anchor })
	m.selectLibraryWorkspace(max(0, selected))
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
	m.savedWork.anchor = entry.path
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
	case "/":
		if m.savedWork.busy {
			return nil
		}
		m.savedWork.mode = "library-search"
		return m.workspaceNameInput("Find saved workspace", m.savedWork.query)
	case "o":
		if m.savedWork.busy {
			return nil
		}
		m.savedWork.sortName = !m.savedWork.sortName
		m.filterWorkspaceLibrary()
	case "m":
		if m.savedWork.busy {
			return nil
		}
		m.savedWork.mode, m.savedWork.actionSelected = "library-actions", 0
	case "n", "d", "delete", "backspace":
		if count == 0 || m.savedWork.entries[m.savedWork.selected].checkpoint == nil {
			return nil
		}
		m.savedWork.target = m.savedWork.entries[m.savedWork.selected].checkpoint
		m.ui.page.GotoTop()
		switch msg.String() {
		case "n":
			m.savedWork.mode = "rename"
			return m.workspaceNameInput("Workspace name", m.savedWork.target.manifest.Name)
		case "d":
			m.savedWork.mode = "duplicate"
			return m.workspaceNameInput("Copy name", m.savedWork.target.manifest.Name)
		default:
			m.savedWork.mode = "delete"
		}
	case "s":
		return m.openSavedWorkspaceDialog("save")
	case "a":
		return m.openSavedWorkspaceDialog("save-as")
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
	case "tab", "p":
		m.savedWork.request++
		m.savedWork.mode, m.savedWork.busy = "open", false
		m.ui.notice = ""
		cmd := m.workspaceNameInput("Workspace JSON path", "")
		m.ui.search.CharLimit = 4096
		if msg.String() == "tab" {
			return m.browseSavedWorkspaces()
		}
		return cmd
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
	hints := "↑/↓ select • enter open • / search • o sort"
	hints += "\nm actions • tab files • p path • r refresh • esc close"
	if m.savedWork.mode == "library-search" {
		hints = "Type to filter • enter/esc back to list"
	}
	count := len(m.savedWork.entries)
	sort := "newest first"
	if m.savedWork.sortName {
		sort = "name A-Z"
	}
	filter := "Search: " + m.savedWork.query + " | Sort: " + sort
	if m.savedWork.mode == "library-search" {
		filter = m.ui.search.View() + " | Sort: " + sort
	}
	if count == 0 {
		body := "No saved workspaces yet.\nUse Save workspace to create one, or Tab to choose a file."
		if len(m.savedWork.allEntries) > 0 {
			body = "No matching workspaces. Press / to change or clear the search."
		}
		if m.savedWork.busy {
			body = "Reading saved workspaces..."
		}
		return "Open saved workspace", ansi.Hardwrap(filter+"\n\n"+body, width, true), hints
	}
	rows := min(count, max(1, min(5, height/4)))
	start := max(0, min(m.savedWork.selected-rows/2, count-rows))
	lines := []string{
		ansi.Truncate(filter, width, ""),
		fmt.Sprintf("Saved workspaces (%d/%d)", m.savedWork.selected+1, count),
	}
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
		suffix := ""
		active := m.workspace.checkpoint != nil && m.workspace.checkpoint.path == entry.path
		if active {
			suffix = " (active)"
		}
		duplicates := 0
		for _, other := range m.savedWork.allEntries {
			if other.name == entry.name {
				duplicates++
			}
		}
		longName := ansi.StringWidth(prefix+name+suffix) > width
		if entry.checkpoint != nil && (duplicates > 1 || longName) {
			suffix = " [" + entry.checkpoint.manifest.ID[:8] + "]"
			if active {
				suffix += " (active)"
			}
		}
		ending := "…"
		if m.settings.ASCII {
			ending = "..."
		}
		line := ansi.Truncate(prefix+name, max(1, width-ansi.StringWidth(suffix)), ending) + suffix
		style := m.styles.dialogText
		if active {
			style = m.styles.dialogAccent.Bold(true)
		}
		if i == m.savedWork.selected {
			style = m.styles.selected
		}
		lines = append(lines, style.Render(pad(line, width)))
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
	return "Open saved workspace", body, hints
}
