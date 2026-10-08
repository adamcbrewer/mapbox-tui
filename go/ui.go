package main

import (
	"crypto/rand"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	pager "charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type dialogKind int

const (
	dialogNone dialogKind = iota
	dialogCommands
	dialogFiles
	dialogHelp
	dialogCredits
	dialogSpeed
	dialogInspect
	dialogWorkspace
	dialogThemes
	dialogJourney
	dialogJourneyLocation
	dialogJourneySearch
)

type uiState struct {
	dialog         dialogKind
	help           help.Model
	picker         filepicker.Model
	search         textinput.Model
	inputID        string
	page, details  pager.Model
	spinner        spinner.Model
	spinning       bool
	selected       int
	detailsFocused bool
	notice         string
}

type shortcutMap struct{ compact, playing, detailsFocused, freeRoam, profiles bool }

type viewerCommand struct{ title, shortcut, action string }

type searchClipboardResult struct {
	owner  string
	dialog dialogKind
	msg    tea.Msg
}

func (m *viewer) clipboardCommand(msg tea.Msg, cmd tea.Cmd) tea.Cmd {
	pressed, isKey := msg.(tea.KeyPressMsg)
	if cmd == nil || !isKey || !key.Matches(pressed, m.ui.search.KeyMap.Paste) {
		return cmd
	}
	owner, dialog := m.ui.inputID, m.ui.dialog
	return func() tea.Msg {
		return searchClipboardResult{owner: owner, dialog: dialog, msg: cmd()}
	}
}

func binding(keys, description string) key.Binding {
	return key.NewBinding(key.WithKeys(strings.Split(keys, "/")...), key.WithHelp(keys, description))
}
func (k shortcutMap) ShortHelp() []key.Binding {
	focus := "details"
	if k.detailsFocused {
		focus = "map"
	}
	if k.freeRoam {
		if k.compact {
			return []key.Binding{
				binding("?", "help"),
				binding("q", "quit"),
				binding("ctrl+o", "open"),
				binding("ctrl+l", "files"),
			}
		}
		return []key.Binding{
			binding("?", "help"),
			binding("ctrl+o", "open"),
			binding("ctrl+l", "files"),
			binding("ctrl+p", "commands"),
			binding("tab", focus),
			binding("q", "quit"),
		}
	}
	play := "play"
	if k.playing {
		play = "pause"
	}
	if k.compact {
		return []key.Binding{
			binding("?", "help"),
			binding("q", "quit"),
			binding("ctrl+o", "open"),
			binding("ctrl+l", "files"),
			binding("space", play),
		}
	}
	keys := []key.Binding{
		binding("?", "help"),
		binding("ctrl+o", "open"),
		binding("ctrl+l", "files"),
		binding("space", play),
	}
	keys = append(
		keys,
		binding("ctrl+p", "commands"),
		binding("p", "speed"),
		binding("tab", focus),
	)
	return append(keys, binding("q", "quit"))
}
func (k shortcutMap) FullHelp() [][]key.Binding {
	if k.freeRoam {
		var elevation []key.Binding
		if k.profiles {
			elevation = []key.Binding{binding("e", "elevation profiles")}
		}
		return [][]key.Binding{
			{
				binding("arrows/hjkl", "pan"),
				binding("+/-", "zoom"),
				binding("ctrl/cmd+wheel", "2x zoom speed"),
				binding("f/r", "fit all visible"),
				binding(",/.", "rotate"),
				binding("n", "north-up"),
			},
			{
				binding("click", "select coordinates"),
				binding("enter", "inspect location"),
				binding("tab", "focus map/details"),
			},
			elevation,
			{
				binding("ctrl+o", "add file"),
				binding("ctrl+l", "loaded files"),
				binding("ctrl+g", "plan journey"),
				binding("ctrl+p", "command palette"),
				binding("ctrl+t", "cycle theme"),
				binding("a", "ASCII map"),
				binding("v", "credits"),
				binding("m", "toggle camera animation"),
				binding("esc", "close panel"),
				binding("q", "quit"),
			},
		}
	}
	return [][]key.Binding{
		{
			binding("arrows/hjkl", "pan; playback continues"),
			binding("+/-", "zoom"),
			binding("ctrl/cmd+wheel", "2x zoom speed"),
			binding("f/r", "fit all visible"),
			binding("c", "center and follow"),
			binding(",/.", "rotate"),
			binding("n", "north-up"),
		},
		{
			binding("[/]", "previous/next point"),
			binding("pgup/pgdown", "seek 100 points"),
			binding("home/end", "first/last point"),
			binding("enter", "inspect position"),
			binding("tab", "focus map/details"),
		},
		{
			binding("space", "play/pause selected activity"),
			binding("s/S", "next/previous speed"),
			binding("p", "speed picker"),
			binding("e", "visible elevation profiles"),
		},
		{
			binding("ctrl+o", "add file"),
			binding("ctrl+l", "loaded files"),
			binding("ctrl+g", "plan journey"),
			binding("ctrl+p", "command palette"),
			binding("ctrl+t", "cycle theme"),
			binding("a", "ASCII map"),
			binding("v", "credits"),
			binding("m", "toggle camera animation"),
			binding("esc", "close panel"),
			binding("q", "quit"),
		},
	}
}

func (m *viewer) initUI() error {
	if m.places == nil {
		token := ""
		if m.provider != nil {
			token = m.provider.token
		}
		m.places = newPlaceClient(token)
	}
	themes, selected, err := loadThemes(m.args.Theme)
	if err != nil {
		return err
	}
	m.themes = themes
	m.settings = viewerSettings{ASCII: m.args.ASCII, NoAnimation: m.args.NoAnimation}
	m.ui.help = help.New()
	m.ui.details = pager.New()
	m.ui.details.SoftWrap = true
	m.ui.page = pager.New()
	m.ui.page.SoftWrap = true
	m.ui.search = textinput.New()
	m.ui.search.Prompt = "› "
	m.ui.search.Placeholder = "Type a command…"
	m.ui.search.CharLimit = 80
	m.ui.search.SetVirtualCursor(false)
	m.ui.spinner = spinner.New()
	m.ui.spinner.Spinner = spinner.Dot
	m.applyTheme(selected)
	return nil
}

func (m *viewer) startSpinner() tea.Cmd {
	if m.ui.spinning {
		return nil
	}
	m.ui.spinning = true
	return m.ui.spinner.Tick
}
func (m *viewer) gradient(s string, background color.Color) string {
	runes := []rune(s)
	t := m.theme()
	colors := lipgloss.Blend1D(len(runes), lipgloss.Color(t.Accent), lipgloss.Color(t.Highlight))
	var out strings.Builder
	for i, r := range runes {
		out.WriteString(lipgloss.NewStyle().Foreground(colors[i]).Background(background).Bold(true).Render(string(r)))
	}
	return out.String()
}
func (m *viewer) closeDialog() {
	m.cancelJourneySearch()
	m.ui.dialog = dialogNone
	m.ui.search.Blur()
	m.ui.inputID = ""
	m.ui.notice = ""
	m.dirty = true
}
func (m *viewer) openDialog(kind dialogKind) tea.Cmd {
	m.ui.dialog = kind
	m.ui.selected = 0
	m.ui.notice = ""
	m.dragging, m.rotating = false, false
	m.dirty = true
	m.ui.page.GotoTop()
	switch kind {
	case dialogCommands:
		m.ui.inputID = rand.Text()
		m.ui.search.CharLimit = 80
		m.ui.search.Placeholder = "Type a command…"
		m.ui.search.SetValue("")
		return m.ui.search.Focus()
	case dialogFiles:
		picker := filepicker.New()
		picker.CurrentDirectory, _ = filepath.Abs(filepath.Dir(m.path))
		picker.AllowedTypes = []string{".gpx", ".fit", ".tcx", ".kml", ".GPX", ".FIT", ".TCX", ".KML"}
		picker.AutoHeight = false
		picker.SetHeight(max(1, min(17, m.height-10)-2))
		picker.ShowPermissions, picker.ShowSize = false, false
		picker.Cursor = "›"
		m.ui.picker = picker
		m.stylePicker()
		return picker.Init()
	case dialogThemes:
		m.ui.selected = m.settings.Theme
	case dialogSpeed:
		if p := m.activePlayback(); p != nil {
			m.ui.selected = p.speed
		}
	case dialogWorkspace:
		for i, row := range m.workspaceRows() {
			if m.workspace.selection != nil && row.ref == m.workspace.selection.ref {
				m.ui.selected = i
				break
			}
		}
	}
	return nil
}

func (m *viewer) commands() []viewerCommand {
	commands := []viewerCommand{
		{title: "Open track file", shortcut: "ctrl+o", action: "open"},
		{title: "Loaded files and routes", shortcut: "ctrl+l", action: "workspace"},
		{title: "Plan journey", shortcut: "ctrl+g", action: "journey"},
		{title: "Keyboard shortcuts", shortcut: "?", action: "help"},
		{title: "Provider credits", shortcut: "v", action: "credits"},
		{title: "Choose colour theme", shortcut: "ctrl+t cycle", action: "theme"},
	}
	if len(m.workspace.documents) > 0 {
		commands = append(commands,
			viewerCommand{title: "Fit all visible routes", shortcut: "f", action: "fit"},
			viewerCommand{title: "Clear workspace (remove all files)", action: "clear"},
		)
	} else {
		commands = append(commands, viewerCommand{title: "Reset world view", shortcut: "f", action: "fit"})
	}
	if len(m.workspace.profileItems()) > 0 {
		commands = append(commands, viewerCommand{title: "Toggle elevation profiles", shortcut: "e", action: "profile"})
	}
	if m.activeTrack() != nil {
		commands = append(commands, viewerCommand{title: "Free roam (clear selection)", action: "roam"})
	}
	if m.activePlayback() != nil {
		commands = append(
			commands,
			viewerCommand{title: "Center and follow position", shortcut: "c", action: "center"},
			viewerCommand{title: "Play / pause", shortcut: "space", action: "play"},
			viewerCommand{title: "Playback speed", shortcut: "p", action: "speed"},
		)
	}
	commands = append(
		commands,
		viewerCommand{title: "Inspect position", shortcut: "enter", action: "inspect"},
		viewerCommand{title: "Toggle camera animation", shortcut: "m", action: "motion"},
		viewerCommand{title: "Toggle ASCII map", shortcut: "a", action: "ascii"},
	)
	query := strings.ToLower(m.ui.search.Value())
	filtered := []viewerCommand{}
	for _, command := range commands {
		if strings.Contains(strings.ToLower(command.title), query) {
			filtered = append(filtered, command)
		}
	}
	return filtered
}
func (m *viewer) runCommand(action string) tea.Cmd {
	m.closeDialog()
	switch action {
	case "roam":
		return m.freeRoam()
	case "clear":
		return m.clearWorkspace()
	case "workspace":
		return m.openDialog(dialogWorkspace)
	case "journey":
		return m.openJourney()
	case "open":
		return m.openDialog(dialogFiles)
	case "help":
		return m.openDialog(dialogHelp)
	case "credits":
		return m.openDialog(dialogCredits)
	case "speed":
		return m.openDialog(dialogSpeed)
	case "theme":
		return m.openDialog(dialogThemes)
	case "inspect":
		return m.openDialog(dialogInspect)
	}
	keys := map[string]string{
		"fit":     "f",
		"center":  "c",
		"play":    "space",
		"profile": "e",
		"motion":  "m",
		"ascii":   "a",
	}
	text := keys[action]
	if text == "space" {
		text = " "
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: []rune(text)[0], Text: text})
	return cmd
}

func (m *viewer) updateDialog(msg tea.Msg) tea.Cmd {
	m.dirty = true
	if m.ui.dialog >= dialogJourney {
		return m.updateJourneyDialog(msg)
	}
	if v, ok := msg.(tea.KeyPressMsg); ok {
		pressed := v.String()
		closeCredits := m.ui.dialog == dialogCredits && pressed == "v"
		closeHelp := m.ui.dialog == dialogHelp && pressed == "?"
		closeWorkspace := m.ui.dialog == dialogWorkspace && pressed == "ctrl+l"
		if pressed == "esc" || closeCredits || closeHelp || closeWorkspace {
			m.closeDialog()
			return nil
		}
		switch m.ui.dialog {
		case dialogWorkspace:
			return m.updateWorkspaceDialog(pressed)
		case dialogCommands:
			commands := m.commands()
			switch pressed {
			case "down", "ctrl+n":
				m.ui.selected = min(m.ui.selected+1, max(0, len(commands)-1))
				return nil
			case "up", "ctrl+p":
				m.ui.selected = max(0, m.ui.selected-1)
				return nil
			case "enter":
				if len(commands) > 0 {
					return m.runCommand(commands[min(m.ui.selected, len(commands)-1)].action)
				}
				return nil
			}
		case dialogSpeed, dialogThemes:
			last := 4
			if m.ui.dialog == dialogThemes {
				last = len(m.themes) - 1
			}
			switch pressed {
			case "down", "j":
				m.ui.selected = min(last, m.ui.selected+1)
			case "up", "k":
				m.ui.selected = max(0, m.ui.selected-1)
			case "enter":
				if m.ui.dialog == dialogThemes {
					m.applyTheme(m.ui.selected)
				} else if p := m.activePlayback(); p != nil {
					m.advancePlayback(time.Now())
					p.speed = m.ui.selected
				}
				m.closeDialog()
			}
			return nil
		}
	}
	var cmd tea.Cmd
	switch m.ui.dialog {
	case dialogWorkspace:
		if mouse, ok := msg.(tea.MouseWheelMsg); ok {
			if mouse.Button == tea.MouseWheelDown {
				return m.updateWorkspaceDialog("down")
			}
			if mouse.Button == tea.MouseWheelUp {
				return m.updateWorkspaceDialog("up")
			}
		}
	case dialogFiles:
		m.ui.picker, cmd = m.ui.picker.Update(msg)
		if selected, path := m.ui.picker.DidSelectFile(msg); selected {
			m.closeDialog()
			return m.load(path)
		}
	case dialogCommands:
		before := m.ui.search.Value()
		m.ui.search, cmd = m.ui.search.Update(msg)
		cmd = m.clipboardCommand(msg, cmd)
		if before != m.ui.search.Value() {
			m.ui.selected = 0
		}
	case dialogHelp, dialogCredits, dialogInspect:
		m.ui.page, cmd = m.ui.page.Update(msg)
	}
	return cmd
}

func (m *viewer) dialogContent(width, height int) (string, string, string) {
	s := m.styles
	switch m.ui.dialog {
	case dialogJourney, dialogJourneyLocation, dialogJourneySearch:
		return m.journeyDialogContent(width, height)
	case dialogWorkspace:
		return m.workspaceDialogContent(width, height)
	case dialogCommands:
		m.ui.search.SetWidth(width - 2)
		rows := []string{m.ui.search.View(), ""}
		commands := m.commands()
		start := max(0, m.ui.selected-max(1, height-3)+1)
		for i := start; i < min(len(commands), start+max(1, height-2)); i++ {
			command := commands[i]
			line := pad(command.title, max(1, width-len(command.shortcut)-3)) + "  " + command.shortcut
			style := s.dialogText
			if i == m.ui.selected {
				style = s.selected
			}
			rows = append(rows, style.Render(pad(line, width)))
		}
		if len(commands) == 0 {
			rows = append(rows, s.dialogMuted.Render("No matching commands"))
		}
		return "Commands", strings.Join(rows, "\n"), "↑/↓ choose   enter run   esc close"
	case dialogFiles:
		m.ui.picker.SetHeight(max(1, height-2))
		m.ui.picker.Styles.Selected = m.ui.picker.Styles.Selected.Width(max(1, width-1)).MaxWidth(max(1, width-1))
		m.ui.picker.Styles.File = s.dialogText.Width(max(1, width-2)).MaxWidth(max(1, width-2))
		m.ui.picker.Styles.Directory = s.dialogAccent.Width(max(1, width-2)).MaxWidth(max(1, width-2))
		m.ui.picker.Styles.DisabledFile = s.dialogMuted.Width(max(1, width-2)).MaxWidth(max(1, width-2))
		path := s.dialogMuted.Render(pad(ansi.Truncate(m.ui.picker.CurrentDirectory, width, "…"), width))
		return "Open track file", path + "\n\n" + m.ui.picker.View(), "↑/↓ choose   ←/→ folders   enter open   esc close"
	case dialogSpeed:
		rows := []string{s.dialogMuted.Render(pad("Playback speed multiplier", width)), ""}
		for i, speed := range []int{1, 5, 20, 60, 120} {
			line := fmt.Sprintf("  %dx", speed)
			style := s.dialogText
			if i == m.ui.selected {
				style = s.selected
				line = "› " + strings.TrimSpace(line)
			}
			rows = append(rows, style.Render(pad(line, width)))
		}
		return "Playback speed", strings.Join(rows, "\n"), "↑/↓ choose   enter apply   esc close"
	case dialogThemes:
		var rows []string
		count := height
		if height > 3 {
			rows = append(rows, s.dialogMuted.Render(pad("Terminal colours · ctrl+t cycles", width)), "")
			count -= 2
		}
		start := max(0, m.ui.selected-max(1, count)+1)
		for i := start; i < min(len(m.themes), start+max(1, count)); i++ {
			theme := m.themes[i]
			line := "  " + theme.Name
			if i == m.settings.Theme {
				line += " (current)"
			}
			style := s.dialogText
			if i == m.ui.selected {
				style = s.selected
				line = "> " + strings.TrimSpace(line)
			}
			rows = append(rows, style.Render(pad(line, width)))
		}
		return "Colour theme", strings.Join(rows, "\n"), "↑/↓ choose   enter apply   esc close"
	}
	title, content := "Keyboard shortcuts", ""
	switch m.ui.dialog {
	case dialogHelp:
		groups := shortcutMap{
			freeRoam: m.activePlayback() == nil,
			profiles: len(m.workspace.profileItems()) > 0,
		}.FullHelp()
		m.ui.help.SetWidth(max(width, 50))
		var helpContent strings.Builder
		for i, heading := range []string{"Navigate", "Inspect", "Playback", "Workspace"} {
			if len(groups[i]) == 0 {
				continue
			}
			helpContent.WriteString(m.gradient(heading, lipgloss.Color(m.theme().Surface)) + "\n" +
				m.ui.help.FullHelpView([][]key.Binding{groups[i]}) + "\n\n")
		}
		content = helpContent.String()
	case dialogCredits:
		title = "Provider credits"
		content = m.sourceCreditsContent(m.sources.view())
		if credits := m.journeyCredits(); credits != "" {
			content += "\n\n" + s.dialogHeading.Render("Journey place search") + "\n" + s.dialogText.Render(credits)
		}
	case dialogInspect:
		title = "Selected position"
		content = s.dialogText.Render(strings.Join(m.details(), "\n"))
	}
	m.ui.page.SetWidth(width)
	m.ui.page.SetHeight(height)
	m.ui.page.SetContent(content)
	return title, m.ui.page.View(), "↑/↓ scroll   esc close"
}

func (m *viewer) sourceCreditsContent(sources sourceView) string {
	s := m.styles
	credits, links := sources.attribution()
	var content strings.Builder
	content.WriteString(s.dialogHeading.Render(credits) + "\n\n" + s.dialogText.Render(strings.Join(links, "\n")))
	for _, source := range sources.Sources {
		status := source.Status
		if status == "" {
			status = "Ready"
		}
		if source.Loading {
			status = "Loading · " + status
		}
		content.WriteString("\n\n" + s.dialogHeading.Render(source.Name) + "\n" + s.dialogText.Render(status))
		fmt.Fprintf(
			&content,
			"\n%d / %d / %d requests / hits / errors",
			source.Stats.Requests,
			source.Stats.Hits,
			source.Stats.Errors,
		)
		if source.RefreshAt != nil {
			content.WriteString("\nRefresh after " + source.RefreshAt.Local().Format("15:04:05"))
		}
	}
	return content.String()
}

func (m *viewer) overlay() {
	dialogOpen := m.ui.dialog != dialogNone
	hasRoom := m.width >= 30 && m.height >= 12
	if !dialogOpen || !hasRoom {
		return
	}
	width := min(64, m.width-8)
	if m.ui.dialog == dialogSpeed || m.ui.dialog == dialogThemes {
		width = min(38, m.width-8)
	}
	height := max(1, min(17, m.height-10))
	if m.ui.dialog == dialogSpeed || m.ui.dialog == dialogThemes {
		height = min(height, 7)
	}
	contentHeight := height
	if m.ui.notice != "" {
		contentHeight--
	}
	title, body, hints := m.dialogContent(width, contentHeight)
	t, s := m.theme(), m.styles
	surface := lipgloss.Color(t.Surface)
	if m.ui.notice != "" {
		body = s.dialogMuted.Render(ansi.Truncate(m.ui.notice, width, "…")) + "\n" + body
	}
	bodyStyle := lipgloss.NewStyle().
		Background(surface).
		Width(width).
		MaxWidth(width).
		Height(height).
		MaxHeight(height)
	content := m.gradient(title, surface) + "\n\n" + bodyStyle.Render(body) + "\n\n" +
		s.dialogMuted.Render(ansi.Truncate(hints, width, "…"))
	border := lipgloss.RoundedBorder()
	if m.settings.ASCII {
		border = lipgloss.ASCIIBorder()
	}
	box := lipgloss.NewStyle().
		Width(width+6).
		Padding(1, 2).
		Background(surface).
		Foreground(lipgloss.Color(t.Text)).
		Border(border).
		BorderForegroundBlend(lipgloss.Color(t.Accent), lipgloss.Color(t.Highlight)).
		Render(content)
	x, y := (m.width-lipgloss.Width(box))/2, (m.height-lipgloss.Height(box))/2
	if m.ui.dialog == dialogSpeed {
		x, y = max(1, m.camera.Width-lipgloss.Width(box)), max(1, m.height-lipgloss.Height(box)-3)
	}
	if m.ui.dialog == dialogInspect {
		if pos := m.position(); pos != nil {
			point := m.camera.screen(pos.World)
			x = max(0, min(m.width-lipgloss.Width(box), int(point.X/2)+3))
			y = max(0, min(m.height-lipgloss.Height(box)-3, int(point.Y/4)+2))
		}
	}
	baseStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(t.Dimmed)).
		Background(lipgloss.Color(t.Background))
	base := lipgloss.NewLayer(baseStyle.Render(ansi.Strip(m.content)))
	x, y = max(0, x), max(0, y)
	canvas := lipgloss.NewCanvas(m.width, m.height).Compose(
		lipgloss.NewCompositor(base, lipgloss.NewLayer(box).X(x).Y(y).Z(1)),
	)
	// Nested Bubbles views reset some cells to the terminal's default background.
	for row := y + 1; row < min(m.height, y+lipgloss.Height(box)-1); row++ {
		for column := x + 1; column < min(m.width, x+lipgloss.Width(box)-1); column++ {
			if cell := canvas.CellAt(column, row); cell != nil && cell.Style.Bg == nil {
				cell.Style.Bg = surface
			}
		}
	}
	m.content = canvas.Render()
	if m.ui.dialog == dialogCommands || m.ui.dialog == dialogJourneyLocation {
		if cursor := m.ui.search.Cursor(); cursor != nil {
			cursor.X += max(0, x) + 3
			cursor.Y += max(0, y) + 4
			if m.ui.notice != "" {
				cursor.Y++
			}
			m.cursor = cursor
		}
	}
}
