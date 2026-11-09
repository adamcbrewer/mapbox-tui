package main

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *viewer) currentSession() sessionInfo {
	if m.control == nil || m.control.server == nil {
		return sessionInfo{}
	}
	return m.control.server.info
}

func (m *viewer) sessionLabel() string {
	info := m.currentSession()
	if info.ID == "" {
		return ""
	}
	identity := info.Name
	if identity == "" {
		identity = info.ID[:8]
	}
	return "Session " + ansi.Truncate(identity, 16, "...")
}

func (m *viewer) sessionContent(width, height int) (string, string, string) {
	info := m.currentSession()
	content := "Local control is unavailable."
	hints := "up/down esc close"
	if m.args.NoControl {
		content = "Local control is disabled by --no-control. This viewer has no session to connect to."
	} else if !sessionControlSupported {
		content = "Local control is unsupported on this platform. This viewer has no session to connect to."
	} else if info.ID != "" {
		content = "UUID\n" + info.ID
		if info.Name != "" {
			content = "Name: " + info.Name + "\n\n" + content
		}
		content += "\n\nCLI status command\nmapbox-tui remote --session " + info.ID + " status --json" +
			"\n\nMCP command\nmapbox-tui mcp --session " + info.ID +
			"\n\nConfigure your MCP client to launch this command." +
			"\n\nThis live session ends when the viewer closes. It is separate from any saved workspace." +
			"\n\nCopying needs terminal clipboard support."
		hints = "Copy: 1 ID 2 CLI 3 MCP\nup/down esc close"
	}
	m.ui.page.SetWidth(width)
	m.ui.page.SetHeight(height)
	m.ui.page.SetContent(m.styles.dialogText.Render(ansi.Hardwrap(content, width, true)))
	return "Session", m.ui.page.View(), hints
}

func (m *viewer) copySession(key string) tea.Cmd {
	info := m.currentSession()
	if info.ID == "" {
		return nil
	}
	text, label := info.ID, "UUID"
	switch key {
	case "2":
		text, label = "mapbox-tui remote --session "+info.ID+" status --json", "CLI command"
	case "3":
		text, label = "mapbox-tui mcp --session "+info.ID, "MCP command"
	}
	m.ui.notice = label + " sent to clipboard"
	m.dirty = true
	return tea.SetClipboard(text)
}
