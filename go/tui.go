package main

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type loaded struct {
	path     string
	id       uint64
	document *Document
	err      error
}
type pulse time.Time
type viewerSettings struct {
	ASCII, NoAnimation bool
	Theme              int
	Elevation          elevationMode
}
type viewer struct {
	savedWork                 savedWorkspaceUI
	path                      string
	args                      Args
	settings                  viewerSettings
	workspace                 Workspace
	control                   *viewerControl
	pendingPaths              []string
	imports                   importActivity
	confirmation              activityConfirmation
	startupCameraPending      bool
	savedView                 *savedView
	viewChanged               bool
	viewRestoreID             uint64
	windowSizeKnown           bool
	initialised               bool
	provider                  *Provider
	sources                   sourceLoader
	sourceProvider            *Provider
	sourceKind                string
	traffic                   *Provider
	trafficEnabled            bool
	ctx                       context.Context
	camera                    Camera
	width, height             int
	dirty, loading            bool
	content                   string
	dragging, rotating, moved bool
	lastX, lastY              int
	inspected                 *XY
	fatal                     error
	wheel                     wheelZoom
	lastTick                  time.Time
	profileHeight             int
	progress                  *progress.Model
	ui                        uiState
	styles                    uiStyles
	themes                    []presentationTheme
	cursor                    *tea.Cursor
	loadID                    uint64
	following                 bool
	motion                    cameraMotion
	lastMotionTick            time.Time
	favourites                favouritesUI
	searchLocation            *placeLocation
	planner                   journeyUI
	searchPosition            *XY
	searchCredit              string
	searchName                string
	places                    *placeClient
	directions                *directionsClient
	authID                    uint64
	authCancel                context.CancelFunc
	authBusy, authFirstRun    bool
	authVerified              bool
	authMessage               string
	authCLI                   mapboxDependencyStatus
	preferences               appSettings
	settingsNotice            string
	themeNotice               string
	connectionEcho            string
	credential                renderingCredential
	billingIntent             billingIntent
	usage                     usageReport
	usageID                   uint64
	usageCancel               context.CancelFunc
}

func tick() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg { return pulse(t) })
}
func (m *viewer) Init() tea.Cmd {
	usage := m.fetchAccountUsage()
	var credentials tea.Cmd
	_, explicit := environmentCredential()
	if !explicit {
		credentials = m.discoverCredentials(false, false)
	} else if m.preferences.productAccess(m.credential.token) == mapboxNeedsConsent {
		m.openBilling(billingEnableOnline)
	}
	if m.args.WorkspacePath != "" {
		return tea.Batch(tick(), m.fetch(), credentials, usage, m.routePendingJourneys(), m.waitControl())
	}
	if m.path == "" {
		navigation := m.freeRoam()
		return tea.Batch(tick(), navigation, credentials, usage, m.startJourneyFromArgs(), m.waitControl())
	}
	return tea.Batch(tick(), m.load(m.path), credentials, usage, m.waitControl())
}
func (m *viewer) freeRoam() tea.Cmd {
	m.loadID++
	m.workspace.clearSelection()
	m.pendingPaths = nil
	m.startupCameraPending = false
	m.initialised = true
	m.camera = m.destination()
	m.motion.Active = false
	m.loading = false
	m.imports = importActivity{}
	m.confirmation = activityConfirmation{}
	m.setFollowing(false)
	m.inspected = nil
	m.ui.details.GotoTop()
	m.lastMotionTick = time.Now()
	m.layout()
	m.dirty = true
	return m.fetch()
}
func (m *viewer) clearWorkspace() tea.Cmd {
	m.closeDialog()
	m.savedWork = savedWorkspaceUI{request: m.savedWork.request + 1}
	m.workspace.clear()
	m.planner = journeyUI{searchID: m.planner.searchID}
	m.args.Journey, m.args.WorkspacePath = nil, ""
	m.searchLocation, m.searchPosition, m.inspected = nil, nil, nil
	m.searchName, m.searchCredit, m.path = "", "", ""
	m.dragging, m.rotating = false, false
	m.ui.filesSelected, m.ui.filesCollapsed = 0, false
	m.viewRestoreID++
	return m.freeRoam()
}
func (m *viewer) activeTrack() *Track {
	if m.workspace.selection == nil {
		return nil
	}
	return m.workspace.selection.track
}
func (m *viewer) activePlayback() *playback {
	if m.workspace.selection == nil {
		return nil
	}
	return m.workspace.selection.playback
}
func (m *viewer) selectedPoint() int {
	if t := m.activeTrack(); t != nil && len(t.Points) > 0 && m.activePlayback() != nil {
		return m.activePlayback().nearest()
	}
	return -1
}
func (m *viewer) visibleProfile() *elevationProfile {
	if m.profileHeight <= 0 {
		return nil
	}
	return m.workspace.profile(max(1, m.width-2), m.profileHeight-2)
}
func (m *viewer) activeColor() string {
	return m.theme().mapColor(roleActiveRoute, m.activeSwatch())
}
func (m *viewer) activeSwatch() activityColor {
	if m.workspace.selection != nil {
		if entry, _, err := m.workspace.lookup(m.workspace.selection.ref); err == nil {
			return entry.colors[m.workspace.selection.ref.ItemID]
		}
	}
	return 0
}
func (m *viewer) selectionChanged() {
	before := m.destination()
	m.motion.Active = false
	m.setFollowing(false)
	m.recordViewChange(before)
	m.inspected = nil
	m.lastTick = time.Now()
	m.ui.details.GotoTop()
	m.layout()
	m.dirty = true
}
func (m *viewer) applyStartupSelection(ref ItemRef) error {
	if m.args.ItemID != nil {
		ref.ItemID = *m.args.ItemID
		if err := m.workspace.selectItem(ref); err != nil {
			return err
		}
	}
	if m.args.PointIndex == nil {
		return nil
	}
	if m.args.ItemID != nil {
		return m.workspace.seek(*m.args.PointIndex)
	}
	return m.workspace.selectDocumentPoint(ref.DocumentID, *m.args.PointIndex)
}
func (m *viewer) load(path string) tea.Cmd {
	if m.loading {
		m.pendingPaths = nil
	}
	m.imports = importActivity{total: len(m.pendingPaths) + 1}
	m.confirmation = activityConfirmation{}
	return m.loadImport(path)
}
func (m *viewer) loadImport(path string) tea.Cmd {
	m.loadID++
	id := m.loadID
	m.loading = true
	m.imports.path = path
	m.dirty = true
	absolute, err := filepath.Abs(path)
	if err != nil {
		return func() tea.Msg { return loaded{path: path, id: id, err: err} }
	}
	if entry := m.workspace.documentAtPath(absolute); entry != nil {
		return func() tea.Msg { return loaded{path: path, id: id, document: entry.document} }
	}
	return tea.Batch(m.startSpinner(), func() tea.Msg {
		d, e := loadDocument(absolute)
		return loaded{
			path:     path,
			id:       id,
			document: d,
			err:      e,
		}
	})
}
func mapSize(w, h int) (int, int) {
	footer := 2
	if w < 110 {
		footer = 3
	}
	if w < 60 {
		footer = 4
	}
	return max(1, w), max(1, h-footer-1)
}
func (m *viewer) fetch() tea.Cmd {
	return m.fetchAt(time.Now())
}
func (m *viewer) configureSources() {
	if m.sourceProvider != m.provider || m.sourceKind != m.provider.Kind {
		m.sources.set("basemap", m.provider, m.provider.Kind == "mapbox")
		m.sourceProvider, m.sourceKind = m.provider, m.provider.Kind
		m.dirty = true
	}
	m.configureTraffic()
}
func (m *viewer) fetchAt(now time.Time) tea.Cmd {
	m.configureSources()
	p := m.activePlayback()
	requests := m.sources.poll(
		m.ctx,
		m.destination(),
		now,
		p != nil && p.Playing,
	)
	if len(requests) == 0 {
		return nil
	}
	m.dirty = true
	commands := []tea.Cmd{m.startSpinner()}
	for _, request := range requests {
		commands = append(commands, func() tea.Msg { return request.run() })
	}
	return tea.Batch(commands...)
}
func (m *viewer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer m.keepCommandSelection()()
	// Finalise operations after input has recorded its revision changes.
	defer m.reconcileOperations()
	// Only navigation from input marks the view for saving. Startup imports,
	// layout changes and animation frames must not persist launch overrides.
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseMsg:
		before := m.destination()
		restoreID := m.viewRestoreID
		defer func() {
			// Confirming a checkpoint restores a view; it is not map navigation.
			if restoreID == m.viewRestoreID {
				m.recordViewChange(before)
			}
		}()
	}
	switch v := msg.(type) {
	case controlMessage:
		return m, tea.Batch(m.handleControl(v), m.waitControl())
	case controlWorkspaceReady:
		return m, m.acceptControlWorkspace(v)
	case controlWorkspaceSaveStart:
		return m, m.startControlWorkspaceSave(v)
	case controlLoaded:
		return m, m.acceptControlLoad(v)
	case filePickerReady:
		return m, m.acceptFilePicker(v)
	case workspaceLibraryReady:
		return m, m.acceptWorkspaceLibrary(v)
	case savedWorkspaceReady:
		return m, m.acceptSavedWorkspace(v)
	case savedImportReady:
		return m, m.acceptSavedImport(v)
	case accountUsageResult:
		if v.id == m.usageID {
			m.usage = v.report
			m.dirty = true
		}
		return m, nil
	case mapboxLinkResult:
		if v.err != nil {
			m.ui.notice = "Could not open browser; visit " + v.url
			m.dirty = true
		}
		return m, nil
	case mapboxDependencyResult:
		if v.id != m.authID {
			return m, nil
		}
		m.authBusy, m.authCLI = false, v.status
		switch v.status.State {
		case "ready":
			m.authMessage = "Mapbox CLI is ready; checking connection…"
		case "missing":
			m.authMessage = "Check failed: Mapbox CLI not found."
		default:
			m.authMessage = "Check failed: Mapbox CLI needs repair/update."
		}
		m.ui.selected = 0
		m.dirty = true
		if m.authCancel != nil {
			m.authCancel()
			m.authCancel = nil
		}
		credential, _ := configuredCredential()
		if v.status.State == "ready" || credential.token != "" {
			return m, m.discoverCredentials(false, true)
		}
		return m, nil
	case credentialResult:
		return m, m.acceptCredentials(v)
	case mapboxLoginReady:
		if v.id != m.authID {
			return m, nil
		}
		if v.err != nil {
			m.authBusy = false
			m.authMessage = v.err.Error()
			if problem, ok := v.err.(*mapboxDependencyError); ok {
				m.authCLI = problem.status
				m.ui.selected = 0
			}
			m.dirty = true
			return m, nil
		}
		if v.cli != nil {
			m.authCLI = mapboxDependencyStatus{State: "ready", Path: v.cli.path}
		}
		command := &viewerMapboxLogin{ctx: v.ctx, cli: v.cli, tokenOnly: v.tokenOnly}
		return m, tea.Exec(command, func(err error) tea.Msg { return mapboxLoginDone{id: v.id, err: err} })
	case mapboxLoginDone:
		if v.id != m.authID {
			return m, nil
		}
		m.authBusy = false
		m.lastTick, m.lastMotionTick = time.Time{}, time.Now()
		if v.err != nil {
			m.authMessage = v.err.Error()
			m.dirty = true
			return m, nil
		}
		preferences, err := saveMapboxOffline(false)
		if err != nil {
			m.authMessage = "Login succeeded; could not save online mode: " + err.Error()
			m.dirty = true
			return m, m.fetchAccountUsage()
		}
		m.preferences.Offline = preferences.Offline
		m.preferences.BillingAcceptedVersion = preferences.BillingAcceptedVersion
		return m, tea.Batch(m.discoverCredentials(true, false), m.fetchAccountUsage())
	case searchClipboardResult:
		if v.owner == "" || v.owner != m.ui.inputID || v.dialog != m.ui.dialog {
			return m, nil
		}
		return m, m.updateDialog(v.msg)
	case placeSearchReady:
		return m, m.requestPlaceSearch(v.id)
	case placeSearchResult:
		return m, m.acceptPlaceSearch(v)
	case favouriteSearchResult:
		m.acceptSearchFavourite(v)
		return m, nil
	case journeyRouteResult:
		remote := m.isControlRoute(v)
		accepted := m.workspace.acceptJourneyRoute(v)
		m.acceptControlRoute(v, accepted)
		if !accepted {
			return m, nil
		}
		if m.planner.ref == v.ref && !remote {
			j, _ := m.workspace.journey(v.ref)
			for i, frame := range m.ui.parents {
				if frame.kind == dialogJourney {
					m.ui.parents[i].notice = j.Error
				}
			}
			if m.ui.dialog == dialogJourney {
				m.ui.notice = j.Error
				if revision := m.planner.showRevision; revision != nil && *revision == v.revision {
					m.planner.showRevision = nil
					if v.err == nil {
						return m, m.showJourneyOnMap()
					}
				}
			}
		}
		m.layout()
		if v.err == nil && !remote && !v.preserveView {
			m.fitPlannerJourney(v.ref)
		}
		m.dirty = true
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.windowSizeKnown = true
		m.layout()
		if m.ui.dialog == dialogFiles {
			m.ui.picker.SetHeight(max(1, min(17, m.height-10)-2))
			m.ui.picker, _ = m.ui.picker.Update(msg)
		}
		m.dirty = true
		if m.initialised && !m.loading && len(m.pendingPaths) == 0 {
			return m, m.startJourneyFromArgs()
		}
	case loaded:
		if v.id != m.loadID {
			return m, nil
		}
		m.loading = false
		if v.err != nil {
			m.imports.failed = true
			m.pendingPaths = nil
			m.startupCameraPending = false
			m.initialised = true
			m.dirty = true
			cmd := m.openDialog(dialogFiles)
			name := "track file"
			if v.path != "" {
				name = cleanPlaceText(filepath.Base(v.path))
			}
			m.ui.notice = "Could not open " + name + ": " + v.err.Error()
			if m.imports.total > 1 {
				m.ui.notice += fmt.Sprintf(" (%d of %d files opened; queue stopped)", m.imports.completed, m.imports.total)
			}
			return m, cmd
		}
		m.imports.completed++
		initial := !m.initialised
		m.initialised = true
		m.path = v.path
		ref := m.workspace.addDocument(v.document)
		if initial {
			m.startupCameraPending = true
			if err := m.applyStartupSelection(ref); err != nil {
				m.fatal = err
				return m, tea.Quit
			}
		}
		m.selectionChanged()
		a := Args{Aspect: m.args.Aspect}
		startup := m.startupCameraPending
		if m.startupCameraPending && len(m.pendingPaths) == 0 {
			a = m.args
			m.startupCameraPending = false
		}
		w, h := m.camera.Width, m.camera.Height
		vw, vh := max(10, min(300, w)), max(5, min(100, h))
		a.Width = &vw
		a.Height = &vh
		c, e := viewport(m.activeTrack(), a)
		if e != nil {
			m.fatal = e
			return m, tea.Quit
		}
		before := m.destination()
		m.camera = c
		if startup {
			m.camera = m.restoreStartupView(c)
		} else {
			m.recordViewChange(before)
		}
		m.camera.Width, m.camera.Height = w, h
		m.lastTick = time.Now()
		m.lastMotionTick = m.lastTick
		m.layout()
		m.dirty = true
		if len(m.pendingPaths) > 0 {
			path := m.pendingPaths[0]
			m.pendingPaths = m.pendingPaths[1:]
			return m, m.loadImport(path)
		}
		label := "File opened"
		if m.imports.total > 1 {
			label = fmt.Sprintf("%d files opened", m.imports.completed)
		}
		m.confirmActivity(label)
		return m, tea.Batch(m.fetch(), m.startJourneyFromArgs())
	case sourceResult:
		m.configureSources()
		if m.sources.accept(v, m.destination(), time.Now()) {
			if v.name == "basemap" {
				snapshot := v.snapshot
				ready := v.err == nil && !snapshot.Failed && snapshot.Status == ""
				switch {
				case snapshot.AccessDenied:
					m.setMapboxVerified(false)
				case ready && snapshot.AccessVerified:
					m.setMapboxVerified(true)
				}
			}
			m.dirty = true
			return m, m.fetch()
		}
	case pulse:
		now := time.Time(v)
		seconds := now.Sub(m.lastMotionTick).Seconds()
		m.lastMotionTick = now
		m.advancePlayback(now)
		if m.motion.advance(&m.camera, seconds) {
			m.dirty = true
		}
		m.reconcileOperations()
		m.updateControlActivity(time.Now())
		m.expireActivity(now)
		command := m.fetchAt(now)
		m.configureSpinner()
		if !m.activityBusy() || m.settings.NoAnimation {
			m.ui.spinning = false
		} else {
			command = tea.Batch(command, m.startSpinner())
		}
		if m.dirty {
			m.rebuild()
			m.dirty = false
		}
		m.captureConnectionNotice()
		return m, tea.Batch(tick(), command)
	case spinner.TickMsg:
		if m.ui.spinning && !m.settings.NoAnimation {
			var cmd tea.Cmd
			m.ui.spinner, cmd = m.ui.spinner.Update(msg)
			m.dirty = true
			return m, cmd
		}
	case tea.KeyPressMsg:
		key := v.String()
		enteringText := m.textDialogOpen() || m.ui.dialog == dialogFiles || m.ui.dialog == dialogFavouriteDelete
		if m.journeyPanelOpen() && (m.planner.collapsed || m.planner.pickingStop) {
			enteringText = false
		}
		if key == "ctrl+c" || (key == "q" && !enteringText) {
			m.cancelPlaceSearch()
			m.workspace.cancelJourneyRoutes()
			m.cancelAuth()
			return m, tea.Quit
		}
		if key == "ctrl+k" && m.ui.dialog != dialogCommands {
			return m, m.pushDialog(dialogCommands)
		}
		if key == "alt+v" && m.ui.dialog != dialogCredits {
			return m, m.pushDialog(dialogCredits)
		}
		if key == "esc" && !m.planner.pickingStop && len(m.ui.parents) == 0 &&
			(m.ui.dialog == dialogWorkspace || m.ui.dialog == dialogJourney) {
			m.ui.detailsOpen = false
			m.closeDialog()
			return m, nil
		}
		panelInput := m.ui.dialog == dialogNone || m.ui.dialog == dialogWorkspace || m.journeyPanelOpen()
		if panelInput {
			switch key {
			case "ctrl+b":
				return m, m.togglePanel()
			case "tab":
				return m, m.cyclePanel()
			case "shift+tab":
				return m, m.cyclePanelDirection(-1)
			case "ctrl+l":
				return m, m.switchPanel(dialogWorkspace)
			case "ctrl+j":
				return m, m.switchPanel(dialogJourney)
			}
		}
		if m.ui.dialog == dialogWorkspace {
			switch key {
			case "/":
				return m, m.pushDialog(dialogSearch)
			case "?":
				return m, m.pushDialog(dialogHelp)
			case "ctrl+o", "o":
				return m, m.openDialog(dialogFiles)
			}
		}
		if m.journeyPanelOpen() && m.planner.pickingStop {
			switch key {
			case "esc":
				m.planner.pickingStop = false
				m.dirty = true
				if m.ui.dialog == dialogJourneyLocation {
					return m, m.ui.search.Focus()
				}
				return m, nil
			case "enter":
				cmd, _ := m.pickJourneyLocation(unproject(m.camera.Center))
				return m, cmd
			}
		}
		if m.journeyPanelOpen() && m.planner.pickingStop && !journeyMapKey(key) {
			return m, nil
		}
		if m.journeyPanelOpen() && (!m.placeSearchOpen() || m.planner.collapsed) {
			switch key {
			case "/":
				return m, m.pushDialog(dialogSearch)
			case "?":
				return m, m.pushDialog(dialogHelp)
			}
		}

		hiddenPanel := (m.journeyPanelOpen() && m.planner.collapsed) ||
			(m.ui.dialog == dialogWorkspace && m.ui.filesCollapsed)
		mapKey := hiddenPanel || (m.journeyPanelOpen() && m.planner.pickingStop && journeyMapKey(key))
		if hiddenPanel {
			switch key {
			case "enter":
				return m, m.pushDialog(dialogInspect)
			case "esc":
				m.closeDialog()
				return m, nil
			}
		}
		if m.ui.dialog != dialogNone && !mapKey {
			return m, m.updateDialog(msg)
		}
		switch key {
		case "q":
			return m, tea.Quit
		case "/":
			return m, m.pushDialog(dialogSearch)
		case "ctrl+o", "o":
			return m, m.openDialog(dialogFiles)
		case "ctrl+l":
			return m, m.openDialog(dialogWorkspace)
		case "ctrl+j":
			return m, m.openJourney()
		case "?":
			return m, m.openDialog(dialogHelp)
		case "v":
			return m, m.openDialog(dialogCredits)
		case "p":
			if m.activePlayback() != nil {
				return m, m.openDialog(dialogSpeed)
			}
			return m, nil
		case "enter":
			return m, m.openDialog(dialogInspect)

		}
		if m.ui.detailsOpen && m.ui.dialog == dialogNone {
			switch key {
			case "home":
				m.ui.details.GotoTop()
				m.dirty = true
				return m, nil
			case "end":
				m.ui.details.GotoBottom()
				m.dirty = true
				return m, nil
			case "pgup", "pgdown":
				var cmd tea.Cmd
				m.ui.details, cmd = m.ui.details.Update(msg)
				m.dirty = true
				return m, cmd
			case "esc":
				m.ui.detailsOpen = false
				m.dirty = true
				return m, nil
			}
		}
		if key == "esc" {
			m.inspected, m.searchPosition = nil, nil
			m.searchLocation = nil
			m.searchName = ""
			m.ui.details.GotoTop()
			m.dirty = true
			return m, nil
		}
		if key == "e" && len(m.workspace.profileItems()) > 0 {
			return m, m.toggleMenuOption("profile")
		}
		if m.activePlayback() == nil {
			switch key {
			case "[", "]", "pgup", "pgdown", "home", "end", "space", "s", "S":
				return m, nil
			}
		}
		m.advancePlayback(time.Now())
		seeking := false
		selected := m.selectedPoint()
		c := &m.camera
		switch key {
		case "left", "h":
			m.panCamera(8, 0)
		case "right", "l":
			m.panCamera(-8, 0)
		case "up", "k":
			m.panCamera(0, 8)
		case "down", "j":
			m.panCamera(0, -8)
		case "+", "=":
			m.motion.Active = false
			c.zoomAt(XY{float64(c.Width), float64(c.Height * 2)}, zoomStep)
		case "-":
			m.motion.Active = false
			c.zoomAt(XY{float64(c.Width), float64(c.Height * 2)}, -zoomStep)
		case "f", "r":
			m.setFollowing(false)
			m.moveCamera(m.workspace.fitVisible(c.Width, c.Height, c.Aspect))
		case "n":
			m.motion.Active = false
			c.Bearing = 0
		case ",":
			m.motion.Active = false
			c.Bearing -= 10
		case ".":
			m.motion.Active = false
			c.Bearing += 10
		case "[":
			seeking = true
			selected--
			m.inspected = nil
		case "]":
			seeking = true
			selected++
			m.inspected = nil
		case "pgdown":
			seeking = true
			selected += 100
			m.inspected = nil
		case "pgup":
			seeking = true
			selected -= 100
			m.inspected = nil
		case "home":
			seeking = true
			selected = 0
			m.inspected = nil
		case "end":
			seeking = true
			selected = len(m.activeTrack().Points) - 1
			m.inspected = nil
		case "c":
			if m.inspected != nil {
				target := m.destination()
				target.Center = project(m.inspected.X, m.inspected.Y)
				m.setFollowing(false)
				m.moveCamera(target)
			} else if pos := m.position(); pos != nil {
				target := m.destination()
				target.Center = pos.World
				m.setFollowing(true)
				m.moveCamera(target)
			}
		case "space":
			return m, m.toggleMenuOption("play")
		case "s", "S":
			if p := m.activePlayback(); p != nil {
				direction := 1
				if key == "S" {
					direction = -1
				}
				m.advancePlayback(time.Now())
				index := (p.speed + direction + len(playbackSpeeds)) % len(playbackSpeeds)
				if err := m.workspace.setPlaybackSpeed(index); err != nil {
					m.settingsNotice = err.Error()
				}
			}
		case "a":
			return m, m.toggleMenuOption("ascii")
		case "m":
			return m, m.toggleMenuOption("motion")
		default:
			return m, nil
		}
		track := m.activeTrack()
		canSeek := seeking && track != nil
		if canSeek && len(track.Points) > 0 {
			_ = m.workspace.seek(max(0, min(len(track.Points)-1, selected)))
		}
		m.dirty = true
	case tea.MouseMsg:
		mouse := v.Mouse()
		m.advancePlayback(time.Now())
		x, y := mouse.X, mouse.Y
		label, left := m.controlActivityRow(y)
		overActivity := label != "" && x >= left && x < m.width-2
		if overActivity {
			switch v.(type) {
			case tea.MouseReleaseMsg:
				m.dragging, m.rotating = false, false
				return m, nil
			case tea.MouseMotionMsg:
				if !m.dragging && !m.rotating {
					return m, nil
				}
			default:
				return m, nil
			}
		}
		if click, ok := v.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
			panelInput := m.ui.dialog == dialogNone || m.ui.dialog == dialogWorkspace || m.journeyPanelOpen()
			if panelInput {
				if cmd, handled := m.clickPanelTab(x, y); handled {
					return m, cmd
				}
			}
			if y == m.height-2 && m.hasJourneyContext() {
				if m.journeyPanelOpen() {
					m.closeDialog()
					return m, nil
				}
				if m.ui.dialog == dialogNone || m.ui.dialog == dialogWorkspace {
					return m, m.openJourney()
				}
			}
		}
		insideMapX := x >= 0 && x < m.camera.Width
		originY := m.mapOriginY()
		insideMapY := y >= originY && y < m.camera.Height+originY
		inside := insideMapX && insideMapY
		drawerVisible := m.ui.dialog == dialogNone && m.ui.detailsOpen
		panelX := x >= m.journeyPanelX() && x < m.width
		overDetails := drawerVisible && panelX && y >= m.contextDrawerY() && y < m.contextDrawerY()+m.contextDrawerHeight()
		mapGesture := m.dragging || m.rotating
		overPlanner := m.journeyPanelVisible() && panelX && y >= originY && y < m.camera.Height
		if overPlanner && !mapGesture {
			return m, m.updateDialog(msg)
		}
		if m.ui.dialog == dialogWorkspace {
			if !m.ui.filesCollapsed && panelX && y >= 1 && y < m.camera.Height+1 && !mapGesture {
				return m, m.updateFilesPanelMouse(msg)
			}
		} else if m.ui.dialog != dialogNone && !m.journeyPanelOpen() {
			return m, m.updateDialog(msg)
		}
		if m.journeyPanelOpen() && !inside && !mapGesture {
			return m, nil
		}
		if overDetails && !mapGesture && !m.journeyPanelOpen() {
			var cmd tea.Cmd
			m.ui.details, cmd = m.ui.details.Update(msg)
			m.dirty = true
			return m, cmd
		}
		switch v.(type) {
		case tea.MouseClickMsg:
			if inside && (mouse.Button == tea.MouseLeft || mouse.Button == tea.MouseRight) {
				m.motion.Active = false
				m.setFollowing(false)
				m.dragging = mouse.Button == tea.MouseLeft
				m.rotating = mouse.Button == tea.MouseRight
				m.moved = false
				m.lastX, m.lastY = x, y
			}
		case tea.MouseMotionMsg:
			if m.dragging || m.rotating {
				dx, dy := x-m.lastX, y-m.lastY
				if dx != 0 || dy != 0 {
					m.moved = true
					if m.rotating {
						m.camera.Bearing += float64(dx) * 2
					} else {
						m.setFollowing(false)
						m.camera.pan(float64(dx*2), float64(dy*4))
					}
					m.lastX, m.lastY = x, y
					m.dirty = true
				}
			}
		case tea.MouseReleaseMsg:
			if m.dragging && !m.moved && inside {
				p := XY{float64(x*2 + 1), float64((y-originY)*4 + 2)}
				world := unproject(m.camera.world(p))
				m.dragging, m.rotating = false, false
				if cmd, picked := m.pickJourneyLocation(world); picked {
					return m, cmd
				}
				m.pausePlayback()
				m.inspected = &world
				if ref, i := m.workspace.pick(m.camera, p); ref != (ItemRef{}) && !m.journeyPanelOpen() {
					_ = m.workspace.selectItem(ref)
					m.selectionChanged()
					m.inspected = &world
					canSeek := i >= 0 && m.activePlayback() != nil
					if canSeek && m.workspace.seek(i) == nil {
						m.inspected = nil
					}
				}
				m.dirty = true
			}
			m.dragging, m.rotating = false, false
		case tea.MouseWheelMsg:
			if inside && (mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown) {
				direction := -1
				if mouse.Button == tea.MouseWheelUp {
					direction = 1
				}
				m.zoomWheel(
					XY{float64(x*2 + 1), float64((y-originY)*4 + 2)},
					direction,
					mouse.Mod,
					time.Now(),
				)
			}
		}
	default:
		if m.ui.dialog != dialogNone {
			return m, m.updateDialog(msg)
		}
	}
	return m, nil
}

func (m *viewer) layout() {
	profileVisible := m.settings.Elevation != elevationHide && m.workspace.hasVisibleElevation()
	w, h := mapSize(m.width, m.height)
	if m.hasJourneyContext() {
		h--
	}
	if len(m.workspace.items()) > 1 && profileVisible {
		h--
	}
	m.profileHeight = profileRows(h, profileVisible)
	m.camera.Width, m.camera.Height = max(1, w), max(1, h-m.profileHeight)
	m.motion.Target.Width, m.motion.Target.Height = m.camera.Width, m.camera.Height
}
func (m *viewer) pausePlayback() {
	if p := m.activePlayback(); p != nil {
		if p.Playing {
			m.workspace.edited()
		}
		p.Playing = false
		m.dirty = true
	}
}
func (m *viewer) position() *ridePosition {
	return m.workspace.position()
}
func (m *viewer) advancePlayback(now time.Time) {
	p, t := m.activePlayback(), m.activeTrack()
	if p == nil || t == nil {
		return
	}
	if !m.lastTick.IsZero() && p.advance(t, now.Sub(m.lastTick).Seconds()) {
		m.clearTransientInspection()
		m.dirty = true
		if pos := m.position(); pos != nil && m.following {
			target := m.destination()
			if followPosition(&target, pos.World) {
				m.viewChanged = true
				m.moveCamera(target)
			}
		}
	}
	m.lastTick = now
}

func (m *viewer) gridRow(g Grid, y int, ascii bool) string {
	var line strings.Builder
	for x := 0; x < g.Width; {
		cell := g.Cells[y*g.Width+x]
		var run strings.Builder
		for x < g.Width {
			next := g.Cells[y*g.Width+x]
			if next.Role != cell.Role || next.Color != cell.Color {
				break
			}
			run.WriteRune(g.glyph(next, ascii))
			x++
		}
		line.WriteString(m.paint(run.String(), cell.Role, cell.Color))
	}
	return line.String()
}

func (m *viewer) zoomWheel(p XY, direction int, mod tea.KeyMod, now time.Time) {
	delta := m.wheel.step(direction, now)
	if delta == 0 {
		return
	}
	if mod&(tea.ModCtrl|tea.ModSuper|tea.ModMeta) != 0 {
		delta *= 2
	}
	m.motion.Active = false
	m.camera.zoomAt(p, delta)
	m.dirty = true
}

func (m *viewer) paint(s string, role mapRole, colors ...activityColor) string {
	activeGeometry := role == roleActiveRoute || role == roleActiveMarker
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme().mapColor(role, colors...))).
		Bold(activeGeometry || role == roleCursor).
		Render(s)
}
func pad(s string, w int) string {
	s = ansi.Truncate(s, max(0, w), "")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}
func (m *viewer) details() []string {
	if m.workspace.selection != nil {
		if lines := m.journeyDetails(); lines != nil {
			return lines
		}
	}
	t := m.activeTrack()
	stats := m.sources.view().Stats
	if t == nil {
		center := unproject(m.camera.Center)
		lines := []string{
			"Map details",
			"",
			"Free roam",
			"No route loaded",
			"ctrl+o to open a track file",
			"",
			"Map center",
			fmt.Sprintf("Lat %.6f", center.Y),
			fmt.Sprintf("Lon %.6f", center.X),
		}
		if len(m.workspace.documents) > 0 {
			lines[3], lines[4] = "No active item", "ctrl+l for loaded files"
		}
		if m.inspected != nil {
			lines = append(
				lines,
				"",
				"Selected position",
				fmt.Sprintf("Lat %.6f", m.inspected.Y),
				fmt.Sprintf("Lon %.6f", m.inspected.X),
			)
		}
		return append(
			lines,
			"",
			fmt.Sprintf("Zoom %.2f", m.camera.Zoom),
			fmt.Sprintf("Bearing %.0f°", math.Mod(m.camera.Bearing+360, 360)),
			"",
			"Tiles · req / hit / err",
			fmt.Sprintf(
				"%d / %d / %d",
				stats.Requests,
				stats.Hits,
				stats.Errors,
			),
		)
	}
	lines := []string{
		"Route details",
		"",
		fmt.Sprintf("%.2f km", t.Distance/1000),
		fmt.Sprintf("%d points / %d parts", len(t.Points), len(t.Segments)),
		fmt.Sprintf("%d waypoints", len(t.Waypoints)),
		"",
		"Selected position",
	}
	if m.inspected != nil {
		lines = append(
			lines,
			fmt.Sprintf("Lat %.6f", m.inspected.Y),
			fmt.Sprintf("Lon %.6f", m.inspected.X),
			"Off-track location",
		)
	} else if pos := m.position(); pos != nil {
		p := t.Points[pos.Index]
		ele, stamp := "unavailable", "unavailable"
		if pos.Elevation != nil {
			ele = fmt.Sprintf("%.1f m", *pos.Elevation)
		}
		if pos.Time != nil {
			stamp = *pos.Time
		}
		label := fmt.Sprintf("Point %d / %d", pos.Index+1, len(t.Points))
		lon, lat := p.Lon, p.Lat
		if pos.Blend > 0 {
			label = fmt.Sprintf(
				"Between %d–%d / %d",
				pos.Index+1,
				pos.Index+2,
				len(t.Points),
			)
			geo := unproject(pos.World)
			lon, lat = geo.X, geo.Y
			if pos.Elevation != nil {
				ele = "~" + ele
			}
			if pos.Time != nil {
				stamp = "~" + stamp
			}
		}
		lines = append(
			lines,
			label,
			fmt.Sprintf("Part %d (%s)", p.Segment+1, t.Segments[p.Segment].Kind),
			fmt.Sprintf("%.2f km along", pos.Distance/1000),
			fmt.Sprintf("Lat %.6f", lat),
			fmt.Sprintf("Lon %.6f", lon),
			"Elevation: "+ele,
			stamp,
		)
	} else {
		lines = append(lines, "No track points")
		if len(t.Waypoints) > 0 {
			p := t.Waypoints[0]
			lines = append(
				lines,
				p.Name,
				fmt.Sprintf("Lat %.6f", p.Lat),
				fmt.Sprintf("Lon %.6f", p.Lon),
			)
		}
	}
	mode := "Free camera · c to follow"
	if m.following {
		mode = "Following position"
	}
	lines = append(
		lines,
		"",
		fmt.Sprintf("Zoom %.2f", m.camera.Zoom),
		fmt.Sprintf("Bearing %.0f°", math.Mod(m.camera.Bearing+360, 360)),
		mode,
		"",
		"S start · E end",
		"W waypoint · @ position",
		"",
		"Tiles · req / hit / err",
		fmt.Sprintf(
			"%d / %d / %d",
			stats.Requests,
			stats.Hits,
			stats.Errors,
		),
	)
	if entry, _, err := m.workspace.lookup(m.workspace.selection.ref); err == nil {
		lines = append(
			lines,
			"",
			"File: "+clean(entry.document.Filename),
			fmt.Sprintf("%d files loaded · ctrl+l", len(m.workspace.documents)),
		)
	}
	return lines
}
func (m *viewer) rebuild() {
	// The native textinput cursor uses the absolute character index, ignoring
	// horizontal scrolling. Workspace paths need the rendered, scroll-aware cursor.
	m.ui.search.SetVirtualCursor(m.ui.dialog == dialogSavedWorkspace)
	m.cursor = nil
	if m.width < 30 || m.height < 12 {
		m.content = "Enlarge terminal to 30×12; q quits"
		return
	}
	c := m.camera
	var cursor *XY
	pos := m.position()
	if pos != nil {
		cursor = &pos.World
	}
	sources := m.sources.view()
	g := renderScene(
		c,
		sources.Features,
		m.workspace.sceneItems(),
		cursor,
	)
	if m.inspected != nil {
		// Inspection belongs to the viewer, independently of the playback cursor.
		g.owner = ItemRef{}
		g.label(c.screen(project(m.inspected.X, m.inspected.Y)), "*", roleCursor, false)
	}
	t, s := m.theme(), m.styles
	var rows []string
	mapRows := []string{}
	for y := 0; y < c.Height; y++ {
		mapRows = append(mapRows, m.gridRow(g, y, m.settings.ASCII))
	}
	rows = append(rows, mapRows...)
	rows = append(rows, m.activityRow())
	profile := m.visibleProfile()
	if len(m.workspace.items()) > 1 && m.profileHeight > 0 {
		rows = append(rows, m.workspaceLegend(m.width, profile))
	}

	if profile != nil {
		header, chart, axis := profile.frame(pos)
		rows = append(rows, s.heading.Foreground(lipgloss.Color(t.Accent)).Render(pad(header, m.width)))
		for y := 0; y < chart.Height; y++ {
			rows = append(rows, " "+m.gridRow(chart, y, m.settings.ASCII)+" ")
		}
		rows = append(rows, s.text.Render(pad(" "+axis, m.width)))
	}
	status := "Left-drag pan · wheel zoom · click inspect · right-drag rotate"
	playback := m.activePlayback()
	if playback != nil {
		status = playback.status()
	}
	if m.control != nil && m.control.server != nil {
		identity := m.control.server.info.Name
		if identity == "" {
			identity = m.control.server.info.ID[:8]
		}
		status = "Session " + identity + " | " + status
	}
	sourceStatus := sources.status()
	if m.provider.token == "" && !m.preferences.Traffic {
		sourceStatus = ""
	}
	if m.preferences.Traffic {
		status = m.trafficLegend() + " | " + status
		if !m.mapboxVerified() {
			sourceStatus = "Traffic paused; verify Mapbox in account settings"
		}
	}
	if sourceStatus != "" {
		status = sourceStatus + " | " + status
	}
	if m.sources.busy() || m.loading {
		var pending []string
		for _, source := range sources.Sources {
			if source.Loading {
				pending = append(pending, source.Name)
			}
		}
		label := "Loading " + strings.Join(pending, ", ") + "…"
		if m.loading {
			label = "Loading track file…"
		}
		status = m.busyMarker() + " " + label + " " + status
	}
	contextStatus := status
	if playback != nil {
		if m.progress == nil {
			bar := progress.New(progress.WithoutPercentage(), progress.WithFillCharacters('█', '░'))
			m.progress = &bar
		}
		m.progress.FullColor = lipgloss.Color(m.activeColor())
		m.progress.EmptyColor = lipgloss.Color(t.ProgressEmpty)
		m.progress.SetWidth(max(6, min(14, m.width/6)))
		m.progress.Full, m.progress.Empty = '█', '░'
		if m.settings.ASCII {
			m.progress.Full, m.progress.Empty = '#', '-'
		}
		if len(m.workspace.items()) > 1 {
			ref := m.workspace.selection.ref
			label := m.workspace.itemLabels()[ref]
			status = ansi.Truncate(label, min(24, m.width/4), "…") + " | " + status
		}
		status = m.progress.ViewAs(playback.ratio()) + "  " + m.paint(status, roleActiveRoute, m.activeSwatch())
	}
	m.ui.help.SetWidth(m.width)
	credits, _ := sources.attribution()
	journeyCredits := strings.ReplaceAll(m.journeyCredits(sources.Credits...), "\n", " ")
	notice := m.connectionNotice()
	if notice != "" && journeyCredits == "" {
		credits = notice
	}
	if journeyCredits != "" {
		if credits == "No basemap" {
			credits = journeyCredits
		} else {
			credits += " " + journeyCredits
		}
	}
	rows = append(rows, m.contextFooter()...)
	rows = append(rows, s.muted.Render(pad(credits, m.width)))
	m.content = s.text.Background(lipgloss.Color(t.Background)).Width(m.width).Render(strings.Join(rows, "\n"))
	if m.ui.detailsOpen && !m.journeyPanelOpen() && m.ui.dialog != dialogWorkspace {
		m.contextDrawer(contextStatus)
	}
	m.overlayFilesPanel()
	m.overlayJourneyPanel()
	m.overlay()
	m.overlayControlActivity()
}
func (m *viewer) View() tea.View {
	v := tea.NewView(m.content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.BackgroundColor = lipgloss.Color(m.theme().Background)
	v.Cursor = m.cursor
	return v
}
func openViewer(path string, a Args) error {
	var paths []string
	if path != "" {
		paths = []string{path}
	}
	return openViewerFiles(paths, a)
}
func openViewerFiles(paths []string, a Args) error {
	c, e := viewport(nil, a)
	if e != nil {
		return e
	}
	needsTrack := a.PointIndex != nil || a.ItemID != nil
	if len(paths) == 0 && needsTrack {
		return fmt.Errorf("point_index requires a track file")
	}
	credential, explicit := environmentCredential()
	p := providerForCredential(a.Provider, credential)
	p.quiet = a.ProviderSet && a.Provider == "none"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &viewer{
		args:         a,
		provider:     p,
		credential:   credential,
		ctx:          ctx,
		camera:       c,
		width:        120,
		height:       40,
		dirty:        true,
		authFirstRun: len(paths) == 0 && !explicit && !p.quiet,
	}
	if len(paths) > 0 {
		m.path, m.pendingPaths = paths[0], paths[1:]
	}
	if err := m.initUI(); err != nil {
		return err
	}
	if a.WorkspacePath != "" {
		candidate, camera, err := prepareWorkspace(a.WorkspacePath)
		if err != nil {
			return err
		}
		m.workspace.replace(candidate)
		m.planner.ref = m.workspace.restoredPlannerRef()
		m.camera = workspaceLaunchCamera(camera, a)
		m.initialised = true
		if len(m.workspace.unresolved) > 0 {
			m.openSavedWorkspaceDialog("repair")
		}
	}
	m.layout()
	defer m.sources.close()
	defer m.cancelPlaceSearch()
	defer m.workspace.cancelJourneyRoutes()
	defer m.cancelAuth()
	defer m.cancelAccountUsage()
	server, err := startViewerSession(a)
	if err != nil {
		return err
	}
	if server != nil {
		defer server.close()
		m.control = &viewerControl{server: server}
	}
	defer m.cancelControlOperations()
	_, e = tea.NewProgram(m).Run()
	if m.connectionEcho != "" {
		fmt.Println(m.connectionEcho)
	}
	if e != nil {
		return e
	}
	if m.fatal != nil {
		return m.fatal
	}
	return m.saveViewOnExit()
}
