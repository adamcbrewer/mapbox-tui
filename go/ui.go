package main

import (
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

var (
	uiInk         = lipgloss.Color("#182431")
	uiSurface     = lipgloss.Color("#223044")
	uiSelected    = lipgloss.Color("#35435D")
	uiViolet      = lipgloss.Color("#B8A1F4")
	uiRose        = lipgloss.Color("#F1A5CB")
	uiText        = lipgloss.NewStyle().Foreground(lipgloss.Color("#D5DFEA"))
	uiMuted       = lipgloss.NewStyle().Foreground(lipgloss.Color("#8294A9"))
	uiHeading     = uiText.Bold(true)
	uiAccent      = lipgloss.NewStyle().Foreground(uiViolet)
	dialogText    = uiText.Background(uiSurface)
	dialogMuted   = uiMuted.Background(uiSurface)
	dialogHeading = uiHeading.Background(uiSurface)
	dialogAccent  = uiAccent.Background(uiSurface)
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
)

type uiState struct {
	dialog         dialogKind
	help           help.Model
	picker         filepicker.Model
	search         textinput.Model
	page, details  pager.Model
	spinner        spinner.Model
	spinning       bool
	selected       int
	detailsFocused bool
	notice         string
}

type shortcutMap struct{ compact, playing, detailsFocused, freeRoam bool }

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
			return []key.Binding{binding("?", "help"), binding("q", "quit"), binding("ctrl+o", "open")}
		}
		return []key.Binding{binding("?", "help"), binding("ctrl+o", "open"), binding("ctrl+p", "commands"), binding("tab", focus), binding("q", "quit")}
	}
	play := "play"
	if k.playing {
		play = "pause"
	}
	if k.compact {
		return []key.Binding{binding("?", "help"), binding("q", "quit"), binding("ctrl+o", "open"), binding("space", play)}
	}
	keys := []key.Binding{binding("?", "help"), binding("ctrl+o", "open"), binding("space", play)}
	keys = append(keys, binding("ctrl+p", "commands"), binding("p", "speed"), binding("tab", focus))
	return append(keys, binding("q", "quit"))
}
func (k shortcutMap) FullHelp() [][]key.Binding {
	if k.freeRoam {
		return [][]key.Binding{
			{binding("arrows/hjkl", "pan"), binding("+/-", "zoom"), binding("ctrl/cmd+wheel", "2x zoom speed"), binding("f/r", "reset world view"), binding(",/.", "rotate"), binding("n", "north-up")},
			{binding("click", "select coordinates"), binding("enter", "inspect location"), binding("tab", "focus map/details")},
			{},
			{binding("ctrl+o", "open GPX"), binding("ctrl+p", "command palette"), binding("a", "ASCII map"), binding("v", "credits"), binding("m", "toggle camera animation"), binding("esc", "close panel"), binding("q", "quit")},
		}
	}
	return [][]key.Binding{
		{binding("arrows/hjkl", "pan; playback continues"), binding("+/-", "zoom"), binding("ctrl/cmd+wheel", "2x zoom speed"), binding("f/r", "fit route"), binding("c", "center and follow"), binding(",/.", "rotate"), binding("n", "north-up")},
		{binding("[/]", "previous/next point"), binding("pgup/pgdown", "seek 100 points"), binding("home/end", "first/last point"), binding("enter", "inspect position"), binding("tab", "focus map/details")},
		{binding("space", "play/pause"), binding("s/S", "next/previous speed"), binding("p", "speed picker"), binding("e", "elevation profile")},
		{binding("ctrl+o", "open GPX"), binding("ctrl+p", "command palette"), binding("a", "ASCII map"), binding("v", "credits"), binding("m", "toggle camera animation"), binding("esc", "close panel"), binding("q", "quit")},
	}
}

func (m *viewer) initUI() {
	m.ui.help = help.New()
	m.ui.help.Styles.ShortKey = uiAccent.Bold(true)
	m.ui.help.Styles.ShortDesc = uiMuted
	m.ui.help.Styles.ShortSeparator = uiMuted
	m.ui.help.Styles.FullKey = dialogAccent.Bold(true)
	m.ui.help.Styles.FullDesc = dialogText
	m.ui.details = pager.New()
	m.ui.details.SoftWrap = true
	m.ui.page = pager.New()
	m.ui.page.SoftWrap = true
	m.ui.search = textinput.New()
	m.ui.search.Prompt = "› "
	m.ui.search.Placeholder = "Type a command…"
	m.ui.search.CharLimit = 80
	m.ui.search.SetVirtualCursor(false)
	inputStyles := m.ui.search.Styles()
	inputStyles.Focused.Text = uiText.Background(uiSelected)
	inputStyles.Focused.Placeholder = uiMuted.Background(uiSelected)
	inputStyles.Focused.Prompt = uiAccent.Background(uiSelected)
	inputStyles.Blurred = inputStyles.Focused
	inputStyles.Cursor.Color = uiRose
	inputStyles.Cursor.Shape = tea.CursorBar
	m.ui.search.SetStyles(inputStyles)
	m.ui.spinner = spinner.New()
	m.ui.spinner.Spinner = spinner.Dot
	m.ui.spinner.Style = uiAccent
}

func (m *viewer) startSpinner() tea.Cmd {
	if m.ui.spinning {
		return nil
	}
	m.ui.spinning = true
	return m.ui.spinner.Tick
}
func gradient(s string, background color.Color) string {
	runes := []rune(s)
	colors := lipgloss.Blend1D(len(runes), uiViolet, uiRose)
	var out strings.Builder
	for i, r := range runes {
		out.WriteString(lipgloss.NewStyle().Foreground(colors[i]).Background(background).Bold(true).Render(string(r)))
	}
	return out.String()
}
func (m *viewer) closeDialog() {
	m.ui.dialog = dialogNone
	m.ui.search.Blur()
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
		m.ui.search.SetValue("")
		return m.ui.search.Focus()
	case dialogFiles:
		picker := filepicker.New()
		picker.CurrentDirectory, _ = filepath.Abs(filepath.Dir(m.path))
		picker.AllowedTypes = []string{".gpx"}
		picker.AutoHeight = false
		picker.SetHeight(max(1, min(17, m.height-10)-2))
		picker.ShowPermissions, picker.ShowSize = false, false
		picker.Cursor = "›"
		picker.Styles.File = dialogText
		picker.Styles.Directory = dialogAccent
		picker.Styles.DisabledFile = dialogMuted
		picker.Styles.DisabledSelected = dialogMuted
		picker.Styles.Symlink = dialogAccent
		picker.Styles.Selected = uiHeading.Foreground(uiRose).Background(uiSelected)
		picker.Styles.Cursor = dialogAccent
		picker.Styles.EmptyDirectory = dialogMuted.SetString("No files in this directory")
		m.ui.picker = picker
		return picker.Init()
	case dialogSpeed:
		if m.playback != nil {
			m.ui.selected = m.playback.speed
		}
	}
	return nil
}

type viewerCommand struct{ title, shortcut, action string }

func (m *viewer) commands() []viewerCommand {
	commands := []viewerCommand{{"Open GPX", "ctrl+o", "open"}, {"Keyboard shortcuts", "?", "help"}, {"Provider credits", "v", "credits"}}
	if m.track != nil {
		commands = append(commands, viewerCommand{"Free roam (clear route)", "", "roam"}, viewerCommand{"Fit route", "f", "fit"}, viewerCommand{"Center and follow position", "c", "center"}, viewerCommand{"Play / pause", "space", "play"}, viewerCommand{"Playback speed", "p", "speed"}, viewerCommand{"Toggle elevation profile", "e", "profile"})
	} else {
		commands = append(commands, viewerCommand{"Reset world view", "f", "fit"})
	}
	commands = append(commands, viewerCommand{"Inspect position", "enter", "inspect"}, viewerCommand{"Toggle camera animation", "m", "motion"}, viewerCommand{"Toggle ASCII map", "a", "ascii"})
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
	case "open":
		return m.openDialog(dialogFiles)
	case "help":
		return m.openDialog(dialogHelp)
	case "credits":
		return m.openDialog(dialogCredits)
	case "speed":
		return m.openDialog(dialogSpeed)
	case "inspect":
		return m.openDialog(dialogInspect)
	}
	keys := map[string]string{"fit": "f", "center": "c", "play": "space", "profile": "e", "motion": "m", "ascii": "a"}
	text := keys[action]
	if text == "space" {
		text = " "
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: []rune(text)[0], Text: text})
	return cmd
}

func (m *viewer) updateDialog(msg tea.Msg) tea.Cmd {
	m.dirty = true
	if v, ok := msg.(tea.KeyPressMsg); ok {
		pressed := v.String()
		if pressed == "esc" || (m.ui.dialog == dialogCredits && pressed == "v") || (m.ui.dialog == dialogHelp && pressed == "?") {
			m.closeDialog()
			return nil
		}
		switch m.ui.dialog {
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
		case dialogSpeed:
			switch pressed {
			case "down", "j":
				m.ui.selected = min(4, m.ui.selected+1)
			case "up", "k":
				m.ui.selected = max(0, m.ui.selected-1)
			case "enter":
				if m.playback != nil {
					m.advancePlayback(time.Now())
					m.playback.speed = m.ui.selected
				}
				m.closeDialog()
			}
			return nil
		}
	}
	var cmd tea.Cmd
	switch m.ui.dialog {
	case dialogFiles:
		m.ui.picker, cmd = m.ui.picker.Update(msg)
		if selected, path := m.ui.picker.DidSelectFile(msg); selected {
			m.closeDialog()
			return m.load(path)
		}
	case dialogCommands:
		before := m.ui.search.Value()
		m.ui.search, cmd = m.ui.search.Update(msg)
		if before != m.ui.search.Value() {
			m.ui.selected = 0
		}
	case dialogHelp, dialogCredits, dialogInspect:
		m.ui.page, cmd = m.ui.page.Update(msg)
	}
	return cmd
}

func (m *viewer) dialogContent(width, height int) (string, string, string) {
	switch m.ui.dialog {
	case dialogCommands:
		m.ui.search.SetWidth(width - 2)
		rows := []string{m.ui.search.View(), ""}
		commands := m.commands()
		start := max(0, m.ui.selected-max(1, height-3)+1)
		for i := start; i < min(len(commands), start+max(1, height-2)); i++ {
			command := commands[i]
			line := pad(command.title, max(1, width-len(command.shortcut)-3)) + "  " + command.shortcut
			style := dialogText
			if i == m.ui.selected {
				style = uiHeading.Foreground(uiRose).Background(uiSelected)
			}
			rows = append(rows, style.Render(pad(line, width)))
		}
		if len(commands) == 0 {
			rows = append(rows, dialogMuted.Render("No matching commands"))
		}
		return "Commands", strings.Join(rows, "\n"), "↑/↓ choose   enter run   esc close"
	case dialogFiles:
		m.ui.picker.SetHeight(max(1, height-2))
		m.ui.picker.Styles.Selected = m.ui.picker.Styles.Selected.Width(max(1, width-1)).MaxWidth(max(1, width-1))
		m.ui.picker.Styles.File = dialogText.Width(max(1, width-2)).MaxWidth(max(1, width-2))
		m.ui.picker.Styles.Directory = dialogAccent.Width(max(1, width-2)).MaxWidth(max(1, width-2))
		m.ui.picker.Styles.DisabledFile = dialogMuted.Width(max(1, width-2)).MaxWidth(max(1, width-2))
		path := dialogMuted.Render(pad(ansi.Truncate(m.ui.picker.CurrentDirectory, width, "…"), width))
		return "Open GPX", path + "\n\n" + m.ui.picker.View(), "↑/↓ choose   ←/→ folders   enter open   esc close"
	case dialogSpeed:
		rows := []string{dialogMuted.Render(pad("Playback speed multiplier", width)), ""}
		for i, speed := range []int{1, 5, 20, 60, 120} {
			line := fmt.Sprintf("  %dx", speed)
			style := dialogText
			if i == m.ui.selected {
				style = uiHeading.Foreground(uiRose).Background(uiSelected)
				line = "› " + strings.TrimSpace(line)
			}
			rows = append(rows, style.Render(pad(line, width)))
		}
		return "Playback speed", strings.Join(rows, "\n"), "↑/↓ choose   enter apply   esc close"
	}
	title, content := "Keyboard shortcuts", ""
	switch m.ui.dialog {
	case dialogHelp:
		groups := shortcutMap{freeRoam: m.track == nil}.FullHelp()
		m.ui.help.SetWidth(max(width, 50))
		for i, heading := range []string{"Navigate", "Inspect", "Playback", "Workspace"} {
			if len(groups[i]) == 0 {
				continue
			}
			content += gradient(heading, uiSurface) + "\n" + m.ui.help.FullHelpView([][]key.Binding{groups[i]}) + "\n\n"
		}
	case dialogCredits:
		title = "Provider credits"
		content = dialogHeading.Render(attribution(m.provider.Kind)) + "\n\n" + dialogText.Render("https://www.mapbox.com/about/maps/\nhttps://www.openstreetmap.org/copyright\nhttps://apps.mapbox.com/feedback/") + "\n\n" + dialogMuted.Render("Tile requests / hits / errors") + fmt.Sprintf("\n%d / %d / %d", m.stats.Requests, m.stats.Hits, m.stats.Errors)
	case dialogInspect:
		title = "Selected position"
		content = dialogText.Render(strings.Join(m.details(), "\n"))
	}
	m.ui.page.SetWidth(width)
	m.ui.page.SetHeight(height)
	m.ui.page.SetContent(content)
	return title, m.ui.page.View(), "↑/↓ scroll   esc close"
}

func (m *viewer) overlay() {
	if m.ui.dialog == dialogNone || m.width < 30 || m.height < 12 {
		return
	}
	width := min(64, m.width-8)
	if m.ui.dialog == dialogSpeed {
		width = min(38, m.width-8)
	}
	height := max(1, min(17, m.height-10))
	if m.ui.dialog == dialogSpeed {
		height = min(height, 7)
	}
	title, body, hints := m.dialogContent(width, height)
	if m.ui.notice != "" {
		body = dialogMuted.Render(ansi.Truncate(m.ui.notice, width, "…")) + "\n" + body
	}
	content := gradient(title, uiSurface) + "\n\n" + lipgloss.NewStyle().Background(uiSurface).Width(width).MaxWidth(width).Height(height).MaxHeight(height).Render(body) + "\n\n" + dialogMuted.Render(ansi.Truncate(hints, width, "…"))
	border := lipgloss.RoundedBorder()
	if m.args.ASCII {
		border = lipgloss.ASCIIBorder()
	}
	box := lipgloss.NewStyle().Width(width+6).Padding(1, 2).Background(uiSurface).Foreground(lipgloss.Color("#D5DFEA")).Border(border).BorderForegroundBlend(uiViolet, uiRose).Render(content)
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
	base := lipgloss.NewLayer(lipgloss.NewStyle().Foreground(lipgloss.Color("#46576B")).Render(ansi.Strip(m.content)))
	x, y = max(0, x), max(0, y)
	canvas := lipgloss.NewCanvas(m.width, m.height).Compose(lipgloss.NewCompositor(base, lipgloss.NewLayer(box).X(x).Y(y).Z(1)))
	// Nested Bubbles views reset some cells to the terminal's default background.
	for row := y + 1; row < min(m.height, y+lipgloss.Height(box)-1); row++ {
		for column := x + 1; column < min(m.width, x+lipgloss.Width(box)-1); column++ {
			if cell := canvas.CellAt(column, row); cell != nil && cell.Style.Bg == nil {
				cell.Style.Bg = uiSurface
			}
		}
	}
	m.content = canvas.Render()
	if m.ui.dialog == dialogCommands {
		if cursor := m.ui.search.Cursor(); cursor != nil {
			cursor.X += max(0, x) + 3
			cursor.Y += max(0, y) + 4
			m.cursor = cursor
		}
	}
}
