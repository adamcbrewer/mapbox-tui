package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type credentialResult struct {
	id         uint64
	credential renderingCredential
	afterLogin bool
	checked    bool
	access     authSection
}

type mapboxLoginReady struct {
	id  uint64
	ctx context.Context
	cli *mapboxCLI
	err error
}

type mapboxDependencyResult struct {
	id     uint64
	status mapboxDependencyStatus
}

type mapboxLoginDone struct {
	id  uint64
	err error
}

type viewerMapboxLogin struct {
	ctx     context.Context
	cli     *mapboxCLI
	streams authStreams
}

func (m *viewer) connectionNotice() string {
	access := m.preferences.productAccess(m.credential.token)
	if access == mapboxOffline {
		return "Offline maps · account usage checked at startup"
	}
	if access == mapboxNeedsConsent {
		return "Mapbox online features paused · review billing in Mapbox settings"
	}
	if m.loading || m.authBusy {
		return ""
	}
	return providerConnectionNotice(m.provider)
}

func (m *viewer) captureConnectionNotice() {
	notice := m.connectionNotice()
	if notice == "" || m.connectionEcho != "" {
		return
	}
	m.connectionEcho = notice
}

func (c *viewerMapboxLogin) SetStdin(input io.Reader)   { c.streams.in = input }
func (c *viewerMapboxLogin) SetStdout(output io.Writer) { c.streams.out = output }
func (c *viewerMapboxLogin) SetStderr(output io.Writer) { c.streams.err = output }
func (c *viewerMapboxLogin) Run() error {
	file, err := mapboxCredentialFile()
	if err != nil {
		return err
	}
	fmt.Fprintf(c.streams.out, "Mapbox credentials: %s (shared default profile)\n", file)
	err = c.cli.login(c.ctx, c.streams)
	if err == nil {
		fmt.Fprintf(c.streams.out, "Mapbox login saved by the CLI at %s\n", file)
	}
	return err
}

func (m *viewer) cancelAuth() {
	if m.authCancel != nil {
		m.authCancel()
		m.authCancel = nil
	}
	m.authID++
	m.authBusy = false
}

func (m *viewer) discoverCredentials(afterLogin, checkConnection bool) tea.Cmd {
	return m.discoverCredentialsWith(afterLogin, checkConnection, streetsAccess)
}

func (m *viewer) discoverCredentialsWith(
	afterLogin bool,
	checkConnection bool,
	probe func(context.Context, *Provider) authSection,
) tea.Cmd {
	m.cancelAuth()
	ctx, cancel := context.WithCancel(m.ctx)
	m.authCancel, m.authBusy = cancel, true
	id := m.authID
	preferences := m.preferences
	return func() tea.Msg {
		credential := resolveCredential(ctx)
		var access authSection
		if preferences.productAccess(credential.token) == mapboxAllowed && (afterLogin || checkConnection) {
			access = probe(ctx, providerForCredential("mapbox", credential))
		}
		return credentialResult{
			id: id, credential: credential, afterLogin: afterLogin, checked: checkConnection, access: access,
		}
	}
}

func (m *viewer) acceptCredentials(result credentialResult) tea.Cmd {
	if result.id != m.authID {
		return nil
	}
	selectedAction := ""
	if m.ui.dialog == dialogMapbox {
		actions := m.mapboxActions()
		selectedAction = actions[min(m.ui.selected, len(actions)-1)].action
	}
	m.authBusy = false
	if m.authCancel != nil {
		m.authCancel()
		m.authCancel = nil
	}
	m.credential = result.credential
	access := m.preferences.productAccess(m.credential.token)
	credential := access.effectiveCredential(m.credential)
	switch {
	case credential.token == "":
		m.setMapboxVerified(false)
	case result.checked || result.afterLogin:
		m.setMapboxVerified(result.access.State == "ready")
	case credential.source == "login":
		m.setMapboxVerified(true)
	case credential.token != m.provider.token:
		m.setMapboxVerified(false)
	}
	if credential.dependency.State != "" {
		m.authCLI = credential.dependency
	}
	kind := m.args.Provider
	if !m.args.ProviderSet {
		kind = defaultProvider(credential.token)
	}
	if access == mapboxOffline || access == mapboxNeedsConsent {
		kind = "none"
	}
	if credential.token != m.provider.token || kind != m.provider.Kind {
		m.cancelPlaceSearch()
		if credential.token != m.directions.token {
			if credential.token == "" {
				m.workspace.cancelJourneyRoutes()
			} else {
				j, _ := m.workspace.journey(m.planner.ref)
				revision := m.planner.preserveViewRevision
				preserveView := j != nil && revision != nil && *revision == j.revision
				m.workspace.invalidateJourneyRoutes()
				if preserveView {
					// Credential discovery changes route validity, not the startup stops.
					revision := j.revision
					m.planner.preserveViewRevision = &revision
				}
			}
			m.directions = newDirectionsClient(credential.token)
		}
		m.provider = providerForCredential(kind, credential)
		m.provider.quiet = m.args.ProviderSet && kind == "none"
		m.places = newPlaceClient(credential.token)
		m.planner.session = ""
		m.planner.sessionStarted, m.planner.sessionRequests = time.Time{}, 0
		m.configureSources()
	} else {
		m.provider.guidance = credential.guidance
	}
	if selectedAction != "" {
		m.ui.selected = 0
		for i, action := range m.mapboxActions() {
			if action.action == selectedAction {
				m.ui.selected = i
				break
			}
		}
	}
	if result.checked {
		if m.preferences.Traffic {
			m.traffic = nil
			m.configureTraffic()
		}
		switch {
		case access == mapboxOffline:
			m.authMessage = "You're offline. Choose 'Go online' to use Mapbox."
		case access == mapboxNeedsConsent:
			m.authMessage = access.guidance()
		case credential.token == "":
			m.authMessage = "Check failed: " + credential.guidance
		case result.access.State != "ready":
			m.authMessage = "Check failed: Streets unavailable; " + result.access.Guidance
		default:
			m.authMessage = "Check passed: Mapbox Streets is ready; rendering source: " + credential.source
		}
	}
	m.dirty = true
	firstRun := m.authFirstRun && m.path == "" && len(m.workspace.documents) == 0 && !m.loading
	m.authFirstRun = false
	if result.afterLogin {
		setupOpen := m.ui.dialog == dialogMapbox || slices.ContainsFunc(m.ui.parents, func(frame dialogFrame) bool {
			return frame.kind == dialogMapbox
		})
		if !setupOpen {
			m.openDialog(dialogMapbox)
		}
		file, err := mapboxCredentialFile()
		m.authMessage = "Login succeeded; rendering source: " + credential.source
		if err == nil {
			m.authMessage = "Login saved at " + file + "; rendering source: " + credential.source
		} else {
			m.authMessage += "; " + err.Error()
		}
		if credential.token == "" {
			m.authMessage += "; " + credential.guidance
		} else {
			m.authMessage += "; Streets: " + result.access.State
			if result.access.Guidance != "" {
				m.authMessage += "; " + result.access.Guidance
			}
		}
	} else if firstRun && credential.token == "" && m.ui.dialog == dialogNone {
		m.openDialog(dialogMapbox)
		m.authMessage = credential.guidance
	} else if m.placeSearchOpen() {
		m.ui.notice = credential.guidance
		if credential.token != "" {
			m.ui.notice = "Mapbox ready; place search is automatic"
		}
	}
	var routing tea.Cmd
	if j, err := m.workspace.journey(m.planner.ref); err == nil {
		if m.defaultJourneyMode(j) {
			routing = m.calculateJourneyIfRouted()
		}
	}
	var search tea.Cmd
	if m.placeSearchOpen() && m.places.token != "" {
		search = m.schedulePlaceSearch()
	}
	if access == mapboxNeedsConsent {
		m.openBilling(billingEnableOnline)
	}
	return tea.Batch(m.fetch(), routing, search, m.routePendingJourneys())
}

func (m *viewer) beginMapboxLogin() tea.Cmd {
	if !m.preferences.billingAccepted() {
		return m.openBilling(billingSignIn)
	}
	m.cancelPlaceSearch()
	m.cancelAuth()
	ctx, cancel := context.WithCancel(m.ctx)
	m.authCancel, m.authBusy = cancel, true
	m.authMessage = "Preparing Mapbox login…"
	id := m.authID
	return func() tea.Msg {
		cli, err := findMapboxCLI(ctx)
		return mapboxLoginReady{id: id, ctx: ctx, cli: cli, err: err}
	}
}

func (m *viewer) openMapboxSetup() tea.Cmd {
	m.pushDialog(dialogMapbox)
	return m.checkMapboxSetup()
}

func (m *viewer) checkMapboxSetup() tea.Cmd {
	m.cancelAuth()
	ctx, cancel := context.WithCancel(m.ctx)
	m.authCancel, m.authBusy = cancel, true
	m.authMessage, m.ui.selected = "", 0
	m.dirty = true
	id := m.authID
	return func() tea.Msg {
		_, status := checkMapboxCLI(ctx)
		return mapboxDependencyResult{id: id, status: status}
	}
}

func (m *viewer) mapboxConnected() bool {
	return m.credential.token != "" || (m.provider != nil && m.provider.token != "")
}

func (m *viewer) mapboxVerified() bool {
	token := m.credential.token
	if token == "" && m.provider != nil {
		token = m.provider.token
	}
	return m.preferences.productAccess(token) == mapboxAllowed && m.authVerified
}

func (m *viewer) setMapboxVerified(verified bool) {
	m.authVerified = verified
	if verified && m.ui.notice == trafficAuthGuidance {
		m.ui.notice = ""
	}
	m.dirty = true
}

func (m *viewer) mapboxActions() []viewerCommand {
	telemetryDisabled := mapboxTelemetryOptOut()
	actions := []viewerCommand{
		{title: "View usage on Mapbox", action: "usage-web"},
		{title: "Usage details", action: "usage"},
	}
	if m.preferences.Offline || !m.preferences.billingAccepted() {
		actions = append(actions, viewerCommand{title: "Go online", action: "online"})
	} else {
		actions = append(actions, viewerCommand{title: "Go offline", action: "offline"})
	}
	if !m.authBusy {
		first := viewerCommand{title: "Check again", action: "check"}
		if m.authCLI.State == "ready" {
			first = viewerCommand{title: "Sign in to Mapbox", action: "login"}
			if m.mapboxConnected() {
				first = viewerCommand{title: "Check connection", action: "check"}
			}
		}
		actions = append(actions, first)
	}
	return append(actions,
		viewerCommand{title: "Billing guidance", action: "billing"},
		viewerCommand{title: "Installation help", action: "install"},
		viewerCommand{title: "CLI telemetry", action: "telemetry", toggle: true, disabled: telemetryDisabled,
			checked: m.preferences.MapboxCLITelemetry && !telemetryDisabled},
		viewerCommand{title: "Map credits", shortcut: "v", action: "credits"},
	)
}

func (m *viewer) updateMapboxDialog(key string) tea.Cmd {
	actions := m.mapboxActions()
	m.ui.selected = min(m.ui.selected, len(actions)-1)
	switch key {
	case "home":
		m.ui.selected = 0
	case "end":
		m.ui.selected = len(actions) - 1
	case "v":
		return m.openMapboxCredits()
	case "up", "k":
		m.ui.selected = menuStep(m.ui.selected, -1, len(actions))
	case "down", "j":
		m.ui.selected = menuStep(m.ui.selected, 1, len(actions))
	case "tab":
		m.ui.selected = (m.ui.selected + 1) % len(actions)
	case "space", "enter":
		if key == "space" && !actions[m.ui.selected].toggle {
			return nil
		}
		switch actions[m.ui.selected].action {
		case "usage-web":
			return openMapboxLink(mapboxUsageURL)
		case "usage":
			return m.pushDialog(dialogMapboxUsage)
		case "billing":
			return m.openBilling(billingShowGuidance)
		case "online":
			if !m.preferences.billingAccepted() {
				return m.openBilling(billingEnableOnline)
			}
			return m.setMapboxOffline(false)
		case "offline":
			return m.setMapboxOffline(true)
		case "install":
			return m.pushDialog(dialogMapboxHelp)
		case "check":
			return m.checkMapboxSetup()
		case "login":
			return m.beginMapboxLogin()
		case "credits":
			return m.openMapboxCredits()
		case "telemetry":
			if mapboxTelemetryOptOut() {
				m.authMessage = "Telemetry disabled by MAPBOX_CLI_NO_TELEMETRY"
				return nil
			}
			preferences, err := updateAppSettings(func(settings *appSettings) {
				settings.MapboxCLITelemetry = !m.preferences.MapboxCLITelemetry
			})
			if err != nil {
				m.authMessage = "Could not save telemetry setting: " + err.Error()
				return nil
			}
			m.preferences.MapboxCLITelemetry = preferences.MapboxCLITelemetry
			m.authMessage = "Mapbox CLI telemetry disabled"
			if preferences.MapboxCLITelemetry {
				m.authMessage = "Opted in to Mapbox CLI telemetry"
			}
		}
	}
	return nil
}

func (m *viewer) openMapboxCredits() tea.Cmd {
	return m.pushDialog(dialogCredits)
}

func (m *viewer) mapboxDialogContent(width, height int) (string, string, string) {
	var rows []string
	actions := m.mapboxActions()
	showMessage := m.authMessage != "" && m.authMessage != m.authCLI.Guidance
	feedbackRows := 0
	if showMessage && height > 1 {
		feedbackRows = min(2, height-1)
	}
	if height >= 7 {
		connection := "Mapbox: not connected"
		if m.mapboxConnected() {
			connection = "Mapbox: connected"
		}
		if m.preferences.Offline {
			connection += " · offline"
		}
		rows = append(rows, m.styles.dialogHeading.Render(ansi.Truncate(connection, width, "…")))
		for _, row := range m.usageSummary() {
			if len(rows) >= height-4-feedbackRows {
				break
			}
			rows = append(rows, m.styles.dialogMuted.Render(ansi.Truncate(row, width, "…")))
		}
		rows = append(rows, "")
	}
	count := max(1, height-len(rows)-feedbackRows)
	selected := min(m.ui.selected, len(actions)-1)
	start := max(0, selected-count+1)
	for i := start; i < min(len(actions), start+count); i++ {
		text := "  " + actions[i].label()
		style := m.styles.dialogText
		if i == selected {
			style = m.styles.selected
			text = "> " + actions[i].label()
		}
		if actions[i].disabled {
			style = style.Foreground(m.styles.dialogMuted.GetForeground())
		}
		text = ansi.Truncate(text, width, "…")
		if actions[i].action == "usage-web" {
			text = terminalHyperlink(text, mapboxUsageURL)
		}
		rows = append(rows, style.Render(pad(text, width)))
	}
	if showMessage && height > len(rows) {
		if height > len(rows)+1 {
			rows = append(rows, "")
		}
		rows = append(rows, m.styles.dialogMuted.Render(ansi.Truncate(m.authMessage, width, "…")))
	}
	title := "Mapbox setup"
	if m.mapboxConnected() {
		title = "Mapbox settings"
	}
	return title, strings.Join(rows, "\n"), "↑/↓ actions   space toggle   enter select   esc close"
}

func (m *viewer) mapboxHelpContent(width, height int) (string, string, string) {
	var content strings.Builder
	content.WriteString("Follow Mapbox's official installation instructions:\n\n")
	content.WriteString(mapboxInstallURL + "\n\n")
	content.WriteString("The separate Tilesets CLI is not needed for mapbox-tui.\n")
	content.WriteString("After installation, return and choose Check again.\n\n")
	content.WriteString("Check: mapbox-tui auth status\nSign in: mapbox-tui auth login\n\n")
	content.WriteString("Executable lookup: PATH or MAPBOX_TUI_CLI.\n")
	content.WriteString("Installing the CLI preserves any saved Mapbox login.\n\n")
	content.WriteString("CLI telemetry is off by default in mapbox-tui. Toggle it in Mapbox settings.\n")
	content.WriteString("Opt-in sends CLI usage information to Mapbox: https://github.com/mapbox/mapbox-cli#privacy\n\n")
	file, err := mapboxCredentialFile()
	if err == nil {
		fmt.Fprintf(&content, "Credential location: %s\n\n", file)
	}
	if m.authCLI.Guidance != "" {
		content.WriteString("Dependency check: " + m.authCLI.Guidance + "\n\n")
	}
	m.ui.page.SetWidth(width)
	m.ui.page.SetHeight(max(1, height))
	m.ui.page.SetContent(content.String())
	return "Installation", m.ui.page.View(), "↑/↓ scroll   esc close"
}
