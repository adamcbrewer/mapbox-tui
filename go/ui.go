package main

import (
	"crypto/rand"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"slices"
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
	dialogMapbox
	dialogMapboxHelp
	dialogMapboxUsage
	dialogMapboxBilling
	dialogSearch
	dialogFavourites
	dialogFavouriteName
	dialogFavouriteDelete
	dialogSavedWorkspace
	dialogJourney
	dialogJourneyLocation
	dialogJourneyRouting
	dialogJourneyRoutingHelp
)

type uiState struct {
	dialog         dialogKind
	parents        []dialogFrame
	help           help.Model
	picker         filepicker.Model
	pickerRequest  uint64
	pickerBusy     bool
	pickerError    string
	search         textinput.Model
	inputID        string
	page, details  pager.Model
	spinner        spinner.Model
	spinning       bool
	selected       int
	themeBefore    int
	menuNavigated  bool
	detailsOpen    bool
	filesCollapsed bool
	filesSelected  int
	filesVisited   bool
	notice         string
	feedback       string
}

type dialogFrame struct {
	kind                        dialogKind
	selected, searchPosition    int
	menuNavigated, collapsed    bool
	filesCollapsed              bool
	searchCharLimit             int
	searchPlaceholder           string
	searchValue, notice, action string
	feedback                    string
	page                        pager.Model
	picker                      *filepicker.Model
	journey                     *journeyDialogFrame
	favouriteEdit               *favouritesUI
	placeSearch                 *placeSearchDialogFrame
}

type placeSearchDialogFrame struct {
	resumeSearch    bool
	client          *placeClient
	query, session  string
	sessionStarted  time.Time
	sessionRequests int
	suggestions     []placeSuggestion
	result          int
}

type journeyDialogFrame struct {
	ref                         ItemRef
	row, stopID                 int
	addingWaypoint, pickingStop bool
}

type shortcutMap struct{ compact, playing, detailsOpen, freeRoam, profiles bool }

type viewerCommand struct {
	title, shortcut, action   string
	disabled, toggle, checked bool
}

func (command viewerCommand) label() string {
	if command.checked {
		return command.title + " ✓"
	}
	return command.title
}

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
	aliases := []string{keys}
	if keys != "/" {
		aliases = strings.Split(keys, "/")
	}
	return key.NewBinding(key.WithKeys(aliases...), key.WithHelp(keys, description))
}

func menuStep(selected, direction, count int) int {
	if count == 0 {
		return 0
	}
	return (selected + direction + count) % count
}

func (k shortcutMap) ShortHelp() []key.Binding {
	focus := "details"
	if k.detailsOpen {
		focus = "close details"
	}
	if k.freeRoam {
		if k.compact {
			return []key.Binding{
				binding("?", "help"),
				binding("/", "search"),
				binding("q", "quit"),
				binding("ctrl+o", "open"),
				binding("ctrl+l", "files"),
			}
		}
		return []key.Binding{
			binding("?", "help"),
			binding("/", "search"),
			binding("ctrl+o", "open"),
			binding("ctrl+l", "files"),
			binding("ctrl+k", "commands"),
			binding("ctrl+b", focus),
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
			binding("/", "search"),
			binding("q", "quit"),
			binding("ctrl+o", "open"),
			binding("ctrl+l", "files"),
			binding("space", play),
		}
	}
	keys := []key.Binding{
		binding("?", "help"),
		binding("/", "search"),
		binding("ctrl+o", "open"),
		binding("ctrl+l", "files"),
		binding("space", play),
	}
	keys = append(
		keys,
		binding("ctrl+k", "commands"),
		binding("p", "speed"),
		binding("ctrl+b", focus),
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
				binding("c", "center selected location"),
				binding(",/.", "rotate"),
				binding("n", "north-up"),
			},
			{
				binding("click", "select coordinates"),
				binding("enter", "inspect location"),
				binding("ctrl+b", "show/hide sidebar"),
				binding("tab", "next sidebar tab"),
			},
			elevation,
			{
				binding("/", "quick place search"),
				binding("ctrl+o", "add file"),
				binding("ctrl+l", "loaded files"),
				binding("ctrl+j", "plan journey"),
				binding("ctrl+k", "command palette"),
				binding("a", "ASCII map"),
				binding("v", "credits"),
				binding("m", "toggle animations"),
				binding("esc", "close panel / clear location"),
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
			binding("c", "center selection / follow track"),
			binding(",/.", "rotate"),
			binding("n", "north-up"),
		},
		{
			binding("[/]", "previous/next point"),
			binding("pgup/pgdown", "seek 100 points"),
			binding("home/end", "first/last point"),
			binding("enter", "inspect position"),
			binding("ctrl+b", "show/hide sidebar"),
			binding("tab", "next sidebar tab"),
		},
		{
			binding("space", "play/pause selected activity"),
			binding("s/S", "next/previous speed"),
			binding("p", "speed picker"),
			binding("e", "visible elevation profiles"),
		},
		{
			binding("/", "quick place search"),
			binding("ctrl+o", "add file"),
			binding("ctrl+l", "loaded files"),
			binding("ctrl+j", "plan journey"),
			binding("ctrl+k", "command palette"),
			binding("a", "ASCII map"),
			binding("v", "credits"),
			binding("m", "toggle animations"),
			binding("esc", "close panel / clear location"),
			binding("q", "quit"),
		},
	}
}

func (m *viewer) initUI() error {
	var preferences appSettings
	var err error
	switch {
	case m.args.SafeStart:
		// Bypass even malformed settings without changing the original file.
	case m.args.ResetView:
		preferences, err = updateAppSettings(func(s *appSettings) { s.LastView = nil })
		if err != nil {
			return fmt.Errorf("could not reset saved view: %w", err)
		}
	default:
		preferences, err = loadAppSettings()
	}
	if err != nil {
		preferences = appSettings{}
		m.authMessage = "Could not load settings; telemetry is off and traffic is off: " + err.Error()
		m.settingsNotice = "Settings unavailable; using defaults: " + err.Error()
	}
	m.preferences = preferences
	if m.provider != nil {
		if m.credential.source == "" {
			m.credential = renderingCredential{token: m.provider.token, guidance: m.provider.guidance}
		}
		access := preferences.productAccess(m.credential.token)
		if access == mapboxOffline || access == mapboxNeedsConsent {
			m.provider = providerForCredential("none", access.effectiveCredential(m.credential))
		}
	}
	if m.places == nil {
		token := ""
		if m.provider != nil {
			token = m.provider.token
		}
		m.places = newPlaceClient(token)
	}
	if m.directions == nil {
		m.directions = newDirectionsClient(m.places.token)
	}
	selected, err := m.initViewerPreferences(preferences)
	if err != nil {
		return err
	}
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
	m.configureSpinner()
	m.applyTheme(selected)
	return nil
}

func (m *viewer) startSpinner() tea.Cmd {
	if m.ui.spinning || m.settings.NoAnimation {
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
	if m.ui.dialog == dialogSavedWorkspace {
		m.savedWork.request++
	}
	if m.ui.dialog == dialogMapboxBilling {
		m.billingIntent = billingNoIntent
	}
	wasPlanner := m.journeyPanelOpen()
	if m.ui.dialog == dialogWorkspace {
		m.ui.filesSelected = m.ui.selected
	}
	setupOpen := m.ui.dialog == dialogMapbox || slices.ContainsFunc(m.ui.parents, func(frame dialogFrame) bool {
		return frame.kind == dialogMapbox
	})
	if setupOpen {
		m.cancelAuth()
	}
	m.restoreThemePreview()
	m.cancelPlaceSearch()
	m.planner.showRevision = nil
	m.planner.pickingStop = false
	m.ui.dialog = dialogNone
	m.ui.parents = nil
	m.ui.search.Blur()
	m.ui.inputID = ""
	m.ui.notice = ""
	m.ui.feedback = ""

	if wasPlanner {
		m.layout()
	}
	m.dirty = true
}
func (m *viewer) openDialog(kind dialogKind) tea.Cmd {
	m.ui.pickerRequest++
	m.ui.pickerBusy, m.ui.pickerError = false, ""
	wasPlanner := m.journeyPanelOpen()
	if m.ui.dialog == dialogWorkspace {
		m.ui.filesSelected = m.ui.selected
	}
	m.restoreThemePreview()
	if m.placeSearchOpen() || kind == dialogSearch {
		m.cancelPlaceSearch()
	}
	m.planner.showRevision = nil
	m.planner.collapsed = false
	m.ui.dialog = kind
	if kind == dialogJourney {
		m.planner.addingWaypoint = false
	}
	m.ui.parents = nil
	m.ui.search.Blur()
	m.ui.inputID = ""
	m.ui.selected = 0
	m.ui.menuNavigated = false
	m.ui.notice = ""
	m.ui.feedback = ""
	m.dragging, m.rotating = false, false
	m.dirty = true
	m.ui.page.GotoTop()
	if wasPlanner != m.journeyPanelOpen() {
		m.layout()
	}
	switch kind {
	case dialogFavourites, dialogFavouriteName:
		m.ui.inputID = rand.Text()
		m.ui.search.CharLimit = 256
		m.ui.search.Placeholder = "Name"
		m.ui.search.SetValue("")
		if kind == dialogFavourites {
			m.ui.search.Placeholder = "Find a favourite…"
			m.loadFavourites()
		}
		return m.ui.search.Focus()
	case dialogCommands, dialogSearch:
		m.ui.inputID = rand.Text()
		m.ui.search.CharLimit = 80
		m.ui.search.Placeholder = "Type a command…"
		if kind == dialogSearch {
			m.planner.session = rand.Text()
			m.planner.sessionStarted, m.planner.sessionRequests = time.Time{}, 0
			m.planner.query = ""
			m.ui.search.CharLimit = 256
			m.ui.search.Placeholder = "Place or latitude, longitude"
		}
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
		return m.readFilePicker(picker.Init())
	case dialogThemes:
		m.ui.themeBefore = m.settings.Theme
		m.ui.selected = m.settings.Theme
	case dialogSpeed:
		if p := m.activePlayback(); p != nil {
			m.ui.selected = p.speed
		}
	case dialogWorkspace:
		m.ui.filesCollapsed = false
		if m.ui.filesVisited {
			m.ui.selected = m.ui.filesSelected
			break
		}
		m.ui.filesVisited = true
		for i, row := range m.workspaceRows() {
			if m.workspace.selection != nil && row.ref == m.workspace.selection.ref {
				m.ui.selected = i
				break
			}
		}
	}
	return nil
}

func (m *viewer) pushDialog(kind dialogKind) tea.Cmd {
	parents := m.ui.parents
	if m.ui.dialog != dialogNone && m.ui.dialog != kind {
		if m.ui.dialog == dialogSavedWorkspace && m.savedWork.busy {
			m.savedWork.request++
			m.savedWork.busy = false
			m.ui.notice = "Workspace operation cancelled; retry after returning."
		}
		frame := dialogFrame{
			kind:              m.ui.dialog,
			selected:          m.ui.selected,
			menuNavigated:     m.ui.menuNavigated,
			searchValue:       m.ui.search.Value(),
			searchPosition:    m.ui.search.Position(),
			searchCharLimit:   m.ui.search.CharLimit,
			searchPlaceholder: m.ui.search.Placeholder,
			notice:            m.ui.notice,
			feedback:          m.ui.feedback,
			page:              m.ui.page,
			collapsed:         m.planner.collapsed,
			filesCollapsed:    m.ui.filesCollapsed,
		}
		if m.ui.dialog == dialogFavouriteName || m.ui.dialog == dialogFavouriteDelete {
			edit := m.favourites
			frame.favouriteEdit = &edit
		}
		if m.placeSearchOpen() {
			frame.placeSearch = &placeSearchDialogFrame{
				resumeSearch: m.planner.searchPending,
				client:       m.places, query: m.planner.query, session: m.planner.session,
				sessionStarted: m.planner.sessionStarted, sessionRequests: m.planner.sessionRequests,
				suggestions: slices.Clone(m.planner.suggestions), result: m.planner.result,
			}
			if m.planner.savingFavourite {
				frame.feedback = ""
			}
		}
		if m.journeyPanelOpen() {
			frame.journey = &journeyDialogFrame{
				ref:            m.planner.ref,
				row:            m.planner.row,
				stopID:         m.planner.stopID,
				addingWaypoint: m.planner.addingWaypoint,
				pickingStop:    m.planner.pickingStop,
			}
		}
		if m.filePickerOpen() {
			picker := m.ui.picker
			frame.picker = &picker
		}
		if frame.kind == dialogMapbox {
			actions := m.mapboxActions()
			frame.action = actions[min(frame.selected, len(actions)-1)].action
		}
		parents = append(parents, frame)
	}
	if kind == dialogFavourites {
		// Visiting favourites stays lookup-free, including through Commands.
		for i := range parents {
			if parents[i].placeSearch != nil {
				parents[i].placeSearch.resumeSearch = false
			}
		}
	}
	command := m.openDialog(kind)
	m.ui.parents = parents
	return command
}

func (m *viewer) backDialog() tea.Cmd {
	if m.ui.dialog == dialogMapboxBilling {
		m.billingIntent = billingNoIntent
	}
	if len(m.ui.parents) == 0 {
		m.closeDialog()
		return nil
	}
	if m.ui.dialog == dialogMapbox {
		m.cancelAuth()
	}
	last := len(m.ui.parents) - 1
	frame := m.ui.parents[last]
	parents := m.ui.parents[:last]
	command := m.openDialog(frame.kind)
	m.ui.parents = parents
	m.ui.selected, m.ui.menuNavigated = frame.selected, frame.menuNavigated
	m.ui.notice, m.ui.page = frame.notice, frame.page
	m.ui.feedback = frame.feedback
	m.ui.page.Style = m.styles.dialogText
	m.planner.collapsed = frame.collapsed
	m.ui.filesCollapsed = frame.filesCollapsed
	if frame.journey != nil {
		m.cancelPlaceSearch()
		m.planner.ref = frame.journey.ref
		m.planner.row, m.planner.stopID = frame.journey.row, frame.journey.stopID
		m.planner.addingWaypoint = frame.journey.addingWaypoint
		m.planner.pickingStop = frame.journey.pickingStop
	}
	if frame.favouriteEdit != nil {
		m.favourites.pending = frame.favouriteEdit.pending
		m.favourites.expected = frame.favouriteEdit.expected
	}
	if frame.kind == dialogThemes {
		m.applyTheme(frame.selected)
	}
	placeSearch := m.placeSearchOpen()
	if m.textDialogOpen() {
		m.ui.search.CharLimit = frame.searchCharLimit
		m.ui.search.Placeholder = frame.searchPlaceholder
		m.ui.search.SetValue(frame.searchValue)
		m.ui.search.SetCursor(frame.searchPosition)
		if frame.kind == dialogSavedWorkspace {
			m.ui.inputID = rand.Text()
			switch m.savedWork.mode {
			case "save", "save-as", "open", "locate", "rename", "duplicate", "library-search":
				command = m.ui.search.Focus()
			default:
				m.ui.search.Blur()
			}
		}
	}
	if placeSearch {
		m.ui.inputID = rand.Text()
		m.ui.search.Blur()
		m.ui.notice = frame.notice
		if saved := frame.placeSearch; saved != nil {
			m.planner.query = saved.query
			validSession := saved.session != "" && time.Since(saved.sessionStarted) < 170*time.Second
			if saved.client == m.places && validSession {
				m.planner.session = saved.session
				m.planner.sessionStarted, m.planner.sessionRequests = saved.sessionStarted, saved.sessionRequests
				m.planner.suggestions, m.planner.result = saved.suggestions, saved.result
			} else {
				m.planner.session = ""
				m.planner.sessionStarted, m.planner.sessionRequests = time.Time{}, 0
				if len(saved.suggestions) > 0 && m.places.token != "" {
					m.ui.notice = "Search session changed or expired; ctrl+s to refresh"
				}
			}
		}
		if !m.planner.collapsed && !m.planner.pickingStop {
			var search tea.Cmd
			if frame.placeSearch == nil || frame.placeSearch.resumeSearch {
				search = m.schedulePlaceSearch()
				m.ui.notice = frame.notice
			}
			command = tea.Batch(command, m.ui.search.Focus(), search)
		}
	}
	if frame.picker != nil {
		m.ui.picker = *frame.picker
		m.ui.picker.SetHeight(max(1, min(17, m.height-10)-2))
		m.stylePicker()
		command = m.readFilePicker(m.ui.picker.Init())
	}
	if frame.kind == dialogMapbox {
		for i, action := range m.mapboxActions() {
			if action.action == frame.action {
				m.ui.selected = i
				break
			}
		}
	}
	m.layout()
	return command
}

func (m *viewer) dialogHints(hints string) string {
	billingConfirmation := m.ui.dialog == dialogMapboxBilling && m.billingIntent != billingShowGuidance
	if len(m.ui.parents) == 0 || m.ui.dialog == dialogFavouriteDelete || billingConfirmation {
		return hints
	}
	lines := strings.Split(hints, "\n")
	last := len(lines) - 1
	before, _, _ := strings.Cut(lines[last], "esc ")
	lines[last] = "esc back / " + strings.TrimRight(before, " /·")
	return strings.Join(lines, "\n")
}

func (m *viewer) commands() []viewerCommand {
	commands := []viewerCommand{
		{title: "Keyboard shortcuts", shortcut: "?", action: "help"},
		{title: "Quick place search", shortcut: "/", action: "search"},
		{title: "Favourite places", action: "favourites"},
		{title: "Save selected location as favourite", action: "save-favourite", disabled: m.inspected == nil},
		{title: "Save workspace", action: "save-workspace"},
		{title: "Save workspace as", action: "save-workspace-as"},
		{title: "Open saved workspace", action: "open-workspace"},
		{title: "Repair missing workspace files", action: "repair-workspace", disabled: len(m.workspace.unresolved) == 0},
	}
	_, journeyErr := m.workspace.journey(m.planner.ref)
	commands = append(commands,
		viewerCommand{title: "Start clean (empty workspace)", action: "clear"},
		viewerCommand{title: "Open track file", shortcut: "ctrl+o", action: "open"},
		viewerCommand{title: "Loaded files and routes", shortcut: "ctrl+l", action: "workspace"},
		viewerCommand{title: "Journey Planner", shortcut: "ctrl+j", action: "journey"},
		viewerCommand{title: "Clear journey", action: "clear-journey", disabled: journeyErr != nil},
		viewerCommand{title: "Colour theme", action: "theme"},
	)
	toggles := []viewerCommand{
		m.trafficCommand(),
		{title: "Toggle animations", shortcut: "m", action: "motion", toggle: true,
			checked: !m.settings.NoAnimation},
		{title: "Toggle ascii", shortcut: "a", action: "ascii", toggle: true, checked: m.settings.ASCII},
	}
	if len(m.workspace.documents) > 0 {
		commands = append(commands,
			viewerCommand{title: "Fit all visible routes", shortcut: "f", action: "fit"},
		)
	}
	if len(m.workspace.profileItems()) > 0 {
		toggles = append(toggles, viewerCommand{
			title: "Toggle elevation profiles", shortcut: "e", action: "profile", toggle: true, checked: m.profileEnabled(),
		})
	}
	if m.activeTrack() != nil {
		commands = append(commands, viewerCommand{title: "Free roam (clear selection)", action: "roam"})
	}
	if m.inspected != nil || m.activePlayback() != nil {
		title := "Center and follow position"
		if m.inspected != nil {
			title = "Center selected location"
		}
		commands = append(commands, viewerCommand{title: title, shortcut: "c", action: "center"})
	}
	if m.activePlayback() != nil {
		commands = append(
			commands,
			viewerCommand{title: "Playback speed", shortcut: "p", action: "speed"},
		)
		toggles = append(toggles, viewerCommand{
			title: "Play / pause", shortcut: "space", action: "play", toggle: true,
			checked: m.activePlayback().Playing,
		})
	}
	commands = append(commands, toggles...)
	authTitle := "Mapbox account setup"
	if m.mapboxConnected() {
		authTitle = "Mapbox settings"
	}
	commands = append(commands, viewerCommand{title: authTitle, action: "auth"})
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
	if action == "traffic" {
		previous := m.preferences.Traffic
		command := m.toggleTraffic()
		if m.preferences.Traffic != previous {
			m.closeDialog()
		}
		return command
	}
	switch action {
	case "save-workspace":
		return m.openSavedWorkspaceDialog("save")
	case "save-workspace-as":
		return m.openSavedWorkspaceDialog("save-as")
	case "open-workspace":
		return m.openSavedWorkspaceDialog("open")
	case "clear":
		return m.startCleanWorkspace()
	case "repair-workspace":
		return m.openSavedWorkspaceDialog("repair")
	case "favourites":
		return m.pushDialog(dialogFavourites)
	case "save-favourite":
		return m.saveFavouritePrompt()
	case "search":
		return m.pushDialog(dialogSearch)
	case "workspace":
		return m.pushDialog(dialogWorkspace)
	case "clear-journey":
		return m.clearJourney()
	case "journey":
		return m.openJourney()
	case "open":
		return m.pushDialog(dialogFiles)
	case "help":
		return m.pushDialog(dialogHelp)
	case "credits":
		return m.pushDialog(dialogCredits)
	case "speed":
		return m.pushDialog(dialogSpeed)
	case "theme":
		return m.pushDialog(dialogThemes)
	case "auth":
		return m.openMapboxSetup()
	}
	m.closeDialog()
	switch action {
	case "roam":
		return m.freeRoam()
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

func (m *viewer) toggleMenuOption(action string) tea.Cmd {
	switch action {
	case "traffic":
		return m.toggleTraffic()
	case "ascii":
		m.settings.ASCII = !m.settings.ASCII
		m.saveViewerPreference(func(s *appSettings) { s.ASCII = new(m.settings.ASCII) })
	case "motion":
		m.setCameraAnimation(m.settings.NoAnimation)
		m.saveViewerPreference(func(s *appSettings) { s.CameraAnimation = new(!m.settings.NoAnimation) })
	case "profile":
		choice, mode := "hide", elevationHide
		if !m.profileEnabled() {
			choice = "show"
			mode = elevationShow
		}
		m.settings.Elevation = mode
		m.saveViewerPreference(func(s *appSettings) { s.Elevation = choice })
		m.layout()
	case "play":
		m.advancePlayback(time.Now())
		if playback := m.activePlayback(); playback != nil {
			m.workspace.setPlaybackPlaying(!playback.Playing)
			m.clearTransientInspection()
		}
	}
	m.dirty = true
	return nil
}

func (m *viewer) updateDialog(msg tea.Msg) tea.Cmd {
	m.dirty = true
	if m.ui.dialog == dialogSavedWorkspace {
		return m.updateSavedWorkspaceDialog(msg)
	}
	if m.favouritesDialogOpen() {
		return m.updateFavouritesDialog(msg)
	}
	if m.ui.dialog == dialogSearch {
		if pressed, ok := msg.(tea.KeyPressMsg); ok {
			return m.updatePlaceLocation(pressed.String(), msg)
		}
		return m.updatePlaceSearchInput(msg)
	}
	if m.ui.dialog >= dialogJourney {
		return m.updateJourneyDialog(msg)
	}
	if v, ok := msg.(tea.KeyPressMsg); ok {
		pressed := v.String()
		if m.ui.dialog == dialogMapboxBilling {
			return m.updateBillingDialog(msg)
		}
		if pressed == "esc" {
			return m.backDialog()
		}
		switch m.ui.dialog {
		case dialogMapbox:
			return m.updateMapboxDialog(pressed)
		case dialogMapboxUsage:
			if pressed == "enter" {
				return openMapboxLink(mapboxUsageURL)
			}
		case dialogWorkspace:
			return m.updateWorkspaceDialog(pressed)
		case dialogCommands:
			commands := m.commands()
			switch pressed {
			case "space":
				if len(commands) > 0 {
					selected := commands[min(m.ui.selected, len(commands)-1)]
					if selected.toggle && m.ui.menuNavigated {
						return m.toggleMenuOption(selected.action)
					}
				}
			case "down", "ctrl+n":
				m.ui.selected = menuStep(m.ui.selected, 1, len(commands))
				m.ui.menuNavigated = true
				return nil
			case "up", "ctrl+p":
				m.ui.selected = menuStep(m.ui.selected, -1, len(commands))
				m.ui.menuNavigated = true
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
				m.ui.selected = menuStep(m.ui.selected, 1, last+1)
			case "up", "k":
				m.ui.selected = menuStep(m.ui.selected, -1, last+1)
			case "enter":
				if m.ui.dialog == dialogThemes {
					m.ui.themeBefore = m.ui.selected
					m.applyTheme(m.ui.selected)
					m.saveThemePreference()
				} else if p := m.activePlayback(); p != nil {
					m.advancePlayback(time.Now())
					if err := m.workspace.setPlaybackSpeed(m.ui.selected); err != nil {
						m.ui.notice = err.Error()
						return nil
					}
				}
				m.closeDialog()
			}
			previewChanged := m.ui.dialog == dialogThemes && m.ui.selected != m.settings.Theme
			if previewChanged {
				m.applyTheme(m.ui.selected)
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
		cmd = m.updateFilePicker(msg)
		if selected, path := m.ui.picker.DidSelectFile(msg); selected && !m.ui.pickerBusy && m.ui.pickerError == "" {
			m.closeDialog()
			return m.load(path)
		}
	case dialogCommands:
		before := m.ui.search.Value()
		m.ui.search, cmd = m.ui.search.Update(msg)
		cmd = m.clipboardCommand(msg, cmd)
		if before != m.ui.search.Value() {
			m.ui.selected = 0
			m.ui.menuNavigated = false
		}
	case dialogHelp, dialogCredits, dialogInspect, dialogMapboxHelp, dialogMapboxUsage, dialogMapboxBilling:
		m.ui.page, cmd = m.ui.page.Update(msg)
	}
	return cmd
}

func (m *viewer) keepCommandSelection() func() {
	if m.ui.dialog != dialogCommands {
		return func() {}
	}
	commands := m.commands()
	selected, query := m.ui.selected, m.ui.search.Value()
	if selected >= len(commands) {
		return func() {}
	}
	action := commands[selected].action
	return func() {
		if m.ui.dialog != dialogCommands || m.ui.selected != selected || m.ui.search.Value() != query {
			return
		}
		index := slices.IndexFunc(m.commands(), func(command viewerCommand) bool { return command.action == action })
		if index >= 0 {
			m.ui.selected = index
		}
	}
}

func (m *viewer) dialogContent(width, height int) (string, string, string) {
	s := m.styles
	switch m.ui.dialog {
	case dialogSavedWorkspace:
		return m.savedWorkspaceContent(width, height)
	case dialogFavourites, dialogFavouriteName, dialogFavouriteDelete:
		return m.favouritesContent(width, height)
	case dialogSearch:
		body, hints := m.placeSearchContent(width, height)
		return "Quick search", body, hints
	case dialogMapbox:
		return m.mapboxDialogContent(width, height)
	case dialogMapboxHelp:
		return m.mapboxHelpContent(width, height)
	case dialogMapboxUsage:
		return m.usageDialogContent(width, height)
	case dialogMapboxBilling:
		return m.billingDialogContent(width, height)
	case dialogJourney, dialogJourneyLocation, dialogJourneyRouting, dialogJourneyRoutingHelp:
		return m.journeyDialogContent(width, height)
	case dialogWorkspace:
		return m.workspaceDialogContent(width, height)
	case dialogCommands:
		m.ui.search.SetWidth(width - 2)
		rows := []string{m.ui.search.View()}
		if height > 2 {
			rows = append(rows, "")
		}
		commands := m.commands()
		count := max(1, height-len(rows))
		start := max(0, m.ui.selected-count+1)
		for i := start; i < min(len(commands), start+count); i++ {
			command := commands[i]
			style := s.dialogText
			if i == m.ui.selected {
				style = s.selected
			}
			if command.disabled {
				style = style.Foreground(lipgloss.Color(m.theme().Dimmed))
			}
			label := command.title
			prefix := "  "
			if i == m.ui.selected {
				prefix = "> "
			}
			if command.checked {
				tick := style.Foreground(lipgloss.Color(m.theme().Accent)).Render("✓")
				label += " " + tick
			}
			line := prefix + label
			shortcutWidth := ansi.StringWidth(command.shortcut)
			if shortcutWidth > 0 && ansi.StringWidth(line)+shortcutWidth+3 <= width {
				line = pad(line, width-shortcutWidth-3) + "  " + command.shortcut
			}
			rows = append(rows, style.Render(pad(line, width)))
		}
		if len(commands) == 0 {
			rows = append(rows, s.dialogMuted.Render("No matching commands"))
		}
		return "Commands", strings.Join(rows, "\n"), "↑/↓ choose   space toggle   enter run   esc close"
	case dialogFiles:
		hints := m.filePickerHints("↑/↓ choose   ←/→ folders   enter open   esc close")
		return "Open track file", m.filePickerContent(width, height), hints
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
			rows = append(rows, s.dialogMuted.Render(pad("Terminal colours · live preview", width)), "")
			count -= 2
		}
		start := max(0, m.ui.selected-max(1, count)+1)
		for i := start; i < min(len(m.themes), start+max(1, count)); i++ {
			theme := m.themes[i]
			line := "  " + theme.Name
			if i == m.ui.themeBefore {
				line += " (current)"
			}
			style := s.dialogText
			if i == m.ui.selected {
				style = s.selected
				line = "> " + strings.TrimSpace(line)
			}
			rows = append(rows, style.Render(pad(line, width)))
		}
		return "Colour theme", strings.Join(rows, "\n"), "↑/↓ preview   enter keep   esc close"
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
		title = "Map credits"
		sources := m.sources.view()
		content = m.sourceCreditsContent(sources)
		if credits := m.journeyCredits(sources.Credits...); credits != "" {
			content += "\n\n" + s.dialogHeading.Render("Search and routing credits") + "\n" + s.dialogText.Render(credits)
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
		if source.Stale {
			status = "Stale · " + status
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
		if source.FetchedAt != nil {
			content.WriteString("\nFetched " + source.FetchedAt.Local().Format("15:04:05"))
		}
		if source.Name == "traffic" {
			content.WriteString("\n" + m.trafficLegend())
		}
		for _, state := range m.sources.states {
			if state.enabled && state.name == source.Name && state.snapshot.Failed {
				guidance := "Check Mapbox settings, account access and your connection."
				if source.RefreshAt != nil {
					guidance = "Retry scheduled automatically. " + guidance
				}
				content.WriteString("\n" + state.activityFailure(false) + "\n" + guidance)
			}
		}
	}
	return content.String()
}

// Keep success/progress feedback in the footer, leaving errors beside the input.
func (m *viewer) dialogFooter(hints string, width int) []string {
	lines := strings.Split(hints, "\n")
	for i, line := range lines {
		lines[i] = m.styles.dialogMuted.Render(ansi.Truncate(line, width, "…"))
	}
	if m.ui.feedback == "" {
		return lines
	}
	feedback := ansi.Truncate(m.ui.feedback, width, "…")
	feedbackWidth := ansi.StringWidth(feedback)
	last := len(lines) - 1
	gap := width - ansi.StringWidth(lines[last]) - feedbackWidth
	if gap >= 2 {
		lines[last] += strings.Repeat(" ", gap) + m.styles.dialogAccent.Render(feedback)
		return lines
	}
	return append(lines, strings.Repeat(" ", width-feedbackWidth)+m.styles.dialogAccent.Render(feedback))
}

func (m *viewer) overlay() {
	m.favourites.deleteButtons = [2]image.Rectangle{}
	m.savedWork.confirmButtons = [3]image.Rectangle{}
	workspacePrompt := m.ui.dialog == dialogSavedWorkspace && m.savedWork.mode == "unsaved"
	workspaceActions := m.ui.dialog == dialogSavedWorkspace && m.savedWork.mode == "library-actions"
	compactDialog := m.ui.dialog == dialogFavouriteDelete || workspacePrompt || workspaceActions
	dialogOpen := m.ui.dialog != dialogNone
	hasRoom := m.width >= 30 && m.height >= 12
	if !dialogOpen || !hasRoom || m.journeyPanelOpen() || m.ui.dialog == dialogWorkspace {
		return
	}
	width := min(64, m.width-8)
	if m.ui.dialog == dialogSpeed || m.ui.dialog == dialogThemes {
		width = min(38, m.width-8)
	}
	if m.ui.dialog == dialogMapbox || m.ui.dialog == dialogFavouriteDelete {
		width = min(52, m.width-8)
	}
	if workspacePrompt {
		width = min(54, m.width-8)
	}
	if workspaceActions {
		width = min(40, m.width-8)
	}
	height := max(1, min(17, m.height-10, m.height-8))
	if m.ui.dialog == dialogSpeed || m.ui.dialog == dialogThemes {
		height = min(height, 7)
	}
	if m.ui.dialog == dialogFavouriteDelete {
		height = min(6, m.height-8)
	}
	compactSearchNotice := m.placeSearchOpen() && height == 2 && m.ui.notice != "" && len(m.planner.suggestions) > 0
	if compactSearchNotice {
		// Use the footer spacer for feedback, keeping the input and selection visible.
		height++
	}
	contentHeight := height
	if m.ui.notice != "" {
		contentHeight--
	}
	title, body, hints := m.dialogContent(width, contentHeight)
	hints = m.dialogHints(hints)
	t, s := m.theme(), m.styles
	surface := lipgloss.Color(t.Surface)
	hintLines := m.dialogFooter(hints, width)
	if extra := max(0, len(hintLines)-2); extra > 0 {
		height = max(1, height-extra)
		contentHeight = max(1, contentHeight-extra)
		title, body, _ = m.dialogContent(width, contentHeight)
	}
	if m.ui.notice != "" {
		body = s.dialogMuted.Render(ansi.Truncate(m.ui.notice, width, "…")) + "\n" + body
	}
	if compactDialog {
		height = lipgloss.Height(body)
	}
	bodyStyle := lipgloss.NewStyle().
		Background(surface).
		Width(width).
		MaxWidth(width).
		Height(height).
		MaxHeight(height)
	gap := "\n\n"
	if !compactDialog && (len(hintLines) > 1 || compactSearchNotice) {
		gap = "\n"
	}
	content := bodyStyle.Render(body) + gap + strings.Join(hintLines, "\n")
	border := m.boxBorder()
	box := t.borderStyle().
		Width(width+6).
		Padding(1, 2).
		Background(surface).
		Foreground(lipgloss.Color(t.Text)).
		Border(border).
		Render(content)
	box = m.titleBox(box, title, m.dialogAncestors())
	boxWidth, boxHeight := lipgloss.Width(box), lipgloss.Height(box)
	x, y := (m.width-boxWidth)/2, (m.height-boxHeight)/2
	if m.ui.dialog == dialogSpeed {
		x, y = max(1, m.camera.Width-boxWidth), max(1, m.height-boxHeight-3)
	}
	if m.ui.dialog == dialogInspect {
		if pos := m.position(); pos != nil {
			point := m.camera.screen(pos.World)
			x = max(0, min(m.width-boxWidth, int(point.X/2)+3))
			y = max(0, min(m.height-boxHeight-3, int(point.Y/4)+2))
		}
	}
	x = max(0, min(m.width-boxWidth, x))
	y = max(0, min(m.height-boxHeight, y))
	if m.ui.dialog == dialogFavouriteDelete {
		origin := image.Pt(x+3, y+2)
		if m.ui.notice != "" {
			origin.Y++
		}
		for i := range m.favourites.deleteButtons {
			m.favourites.deleteButtons[i] = m.favourites.deleteButtons[i].Add(origin)
		}
	}
	if workspacePrompt {
		origin := image.Pt(x+3, y+2)
		if m.ui.notice != "" {
			origin.Y++
		}
		for i := range m.savedWork.confirmButtons {
			m.savedWork.confirmButtons[i] = m.savedWork.confirmButtons[i].Add(origin)
		}
	}
	base := lipgloss.NewLayer(m.content)
	canvas := lipgloss.NewCanvas(m.width, m.height).Compose(
		lipgloss.NewCompositor(base, lipgloss.NewLayer(box).X(x).Y(y).Z(1)),
	)
	// Bubbles views can reset cells to the terminal's default background.
	for row := y + 1; row < y+boxHeight-1; row++ {
		for column := x + 1; column < x+boxWidth-1; column++ {
			if cell := canvas.CellAt(column, row); cell != nil && cell.Style.Bg == nil {
				cell.Style.Bg = surface
			}
		}
	}
	m.content = canvas.Render()
	if m.textDialogOpen() {
		if cursor := m.ui.search.Cursor(); cursor != nil {
			cursor.X += max(0, x) + 3
			cursor.Y += max(0, y) + 2
			if m.ui.notice != "" {
				cursor.Y++
			}
			m.cursor = cursor
		}
	}
}
