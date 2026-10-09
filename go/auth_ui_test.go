package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func authTestViewer(t *testing.T) *viewer {
	t.Helper()
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	m := &viewer{
		ctx: t.Context(), provider: newProvider("none"), width: 120, height: 40,
		camera: fit(nil, 88, 34, 2), showProfile: true,
	}
	if err := m.initUI(); err != nil {
		t.Fatal(err)
	}
	m.layout()
	t.Cleanup(m.cancelAuth)
	t.Cleanup(m.sources.close)
	return m
}

func TestViewerAuthReplacementPreservesWorkspaceCameraAndRejectsOldWork(t *testing.T) {
	m := authTestViewer(t)
	document, err := loadDocument("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	ref := m.workspace.addDocument(document)
	m.path = document.Path
	m.activePlayback().Playing = true
	m.provider = providerForCredential("mapbox", renderingCredential{token: "pk.old"})
	m.configureSources()
	requests := m.sources.poll(m.ctx, m.camera, time.Now(), false)
	if len(requests) != 1 {
		t.Fatal("old provider did not start work")
	}
	oldRequest := requests[0]
	searchCtx, cancel := context.WithCancel(m.ctx)
	m.planner.searchCancel, m.planner.searchBusy = cancel, true
	m.planner.session, m.planner.sessionRequests = "old-session", 5
	oldSearchID := m.planner.searchID
	camera, playback := m.camera, *m.activePlayback()
	m.authID = 7
	m.acceptCredentials(credentialResult{
		id: 7, credential: renderingCredential{token: "pk.new", source: "login"},
		afterLogin: true, access: authSection{State: "ready"},
	})
	if m.camera != camera || len(m.workspace.documents) != 1 || m.workspace.selection.ref != ref {
		t.Fatal("setup reset the camera or imported workspace")
	}
	if !reflect.DeepEqual(*m.activePlayback(), playback) || m.places.token != "pk.new" || m.provider.token != "pk.new" {
		t.Fatal("setup reset playback or failed to replace both clients")
	}
	if oldRequest.ctx.Err() == nil || searchCtx.Err() == nil || m.planner.session != "" {
		t.Fatal("setup did not cancel old map/search requests and session")
	}
	late := sourceResult{name: oldRequest.name, generation: oldRequest.generation, camera: oldRequest.camera}
	if m.sources.accept(late, m.camera, time.Now()) {
		t.Fatal("old basemap response replaced the new source")
	}
	m.acceptPlaceSearch(placeSearchResult{id: oldSearchID})
	if m.planner.searchBusy || m.ui.dialog != dialogMapbox || !strings.Contains(m.authMessage, "credentials.json") {
		t.Fatal("setup did not show saved location or rejected search state")
	}
	m.acceptCredentials(credentialResult{id: 6, credential: renderingCredential{token: "pk.obsolete"}})
	if m.provider.token != "pk.new" {
		t.Fatal("late credential discovery replaced the selected token")
	}
}

func TestViewerFirstRunPromptAndOfflineSetupSuppression(t *testing.T) {
	for _, test := range []struct {
		name, path             string
		firstRun, expectPrompt bool
	}{
		{name: "first run", firstRun: true, expectPrompt: true},
		{name: "file launch", path: "example.gpx"},
		{name: "explicit offline"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := authTestViewer(t)
			m.authFirstRun, m.path = test.firstRun, test.path
			m.acceptCredentials(credentialResult{credential: renderingCredential{source: "none", guidance: mapboxCLIUnavailable}})
			if (m.ui.dialog == dialogMapbox) != test.expectPrompt {
				t.Fatal("unexpected blocking setup prompt")
			}
		})
	}
	m := authTestViewer(t)
	m.args.Provider, m.args.ProviderSet = "none", true
	m.Init()
	if m.authBusy {
		t.Fatal("explicit offline startup queried account credentials")
	}
	setupAvailable := false
	for _, command := range m.commands() {
		setupAvailable = setupAvailable || command.action == "auth"
	}
	if !setupAvailable {
		t.Fatal("offline viewer omitted palette setup")
	}
}

func TestViewerNoBasemapDiscoversSavedLoginForJourneyServices(t *testing.T) {
	m := authTestViewer(t)
	fakeMapbox(t, "good")
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	m.args.Provider, m.args.ProviderSet = "none", true
	m.provider.quiet = true
	cmd := m.Init()
	if !m.authBusy {
		t.Fatal("no-provider startup skipped saved-login discovery for journey services")
	}
	var discovered bool
	for _, child := range cmd().(tea.BatchMsg) {
		if result, ok := child().(credentialResult); ok {
			discovered = true
			m.Update(result)
		}
	}
	if !discovered || m.places.token != "pk.synthetic-login-token" || m.directions.token != m.places.token {
		t.Fatal("saved login did not enable both journey clients")
	}
	if m.provider.Kind != "none" || !m.provider.quiet || len(m.sources.poll(m.ctx, m.camera, time.Now(), false)) != 0 {
		t.Fatal("journey credential discovery enabled basemap requests")
	}
	m.openJourney()
	j, _ := m.workspace.journey(m.planner.ref)
	if j.Mode != journeyDriving {
		t.Fatal("saved-login journey did not default to Driving without a basemap")
	}
}

func TestViewerSetupFitsCompactAndWideTerminals(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}, {30, 12}} {
		m := authTestViewer(t)
		m.width, m.height = size[0], size[1]
		m.layout()
		m.openDialog(dialogCommands)
		m.ui.search.SetValue("Mapbox")
		m.rebuild()
		if !strings.Contains(ansi.Strip(m.content), "Mapbox account") {
			t.Fatal("setup was not visible in the command palette", size)
		}
		m.openDialog(dialogMapbox)
		m.authCLI = mapboxDependencyStatus{State: "missing", Checked: "PATH"}
		m.rebuild()
		plain := ansi.Strip(m.content)
		if !strings.Contains(plain, "Mapbox setup") || !strings.Contains(plain, "Installation help") {
			t.Fatal("setup actions disappeared in compact layout", size)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.ui.dialog != dialogNone {
			t.Fatal("Escape did not continue offline")
		}
	}
}

func TestViewerMapboxSetupActionsAndInstallationHelp(t *testing.T) {
	m := authTestViewer(t)
	m.authCLI = mapboxDependencyStatus{State: "missing", Checked: "PATH", Guidance: mapboxCLIUnavailable}
	m.openDialog(dialogMapbox)
	_, content, _ := m.mapboxDialogContent(64, 17)
	plain := ansi.Strip(content)
	if strings.Contains(plain, "Sign in") || strings.Contains(plain, "https://") ||
		!strings.Contains(plain, "Actions") || !strings.Contains(plain, "> Installation help") {
		t.Fatal("missing CLI setup mixed inert instructions with actions")
	}
	m.updateMapboxDialog("enter")
	if m.ui.dialog != dialogMapboxHelp {
		t.Fatal("installation help action did not open instructions")
	}
	_, help, _ := m.mapboxHelpContent(120, 30)
	if !strings.Contains(ansi.Strip(help), mapboxInstallURL) || strings.Contains(ansi.Strip(help), "curl ") {
		t.Fatal("installation help did not point to official documentation")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogMapbox {
		t.Fatal("Escape did not return to setup actions")
	}
	m.updateMapboxDialog("down")
	if m.mapboxActions()[m.ui.selected].action != "check" {
		t.Fatal("Check again was not selectable")
	}
	m.updateMapboxDialog("down")
	m.updateMapboxDialog("down")
	m.updateMapboxDialog("enter")
	if m.ui.dialog != dialogNone {
		t.Fatal("Continue offline did not close setup")
	}
	m.authCLI.State = "ready"
	m.openDialog(dialogMapbox)
	if m.mapboxActions()[0].action != "login" {
		t.Fatal("validated CLI did not enable sign-in")
	}
	m.provider.token = "pk.connected"
	title, content, _ := m.mapboxDialogContent(52, 8)
	if title != "Mapbox settings" || !strings.Contains(ansi.Strip(content), "Mapbox: connected") ||
		strings.Contains(ansi.Strip(content), "Sign in") || m.mapboxActions()[0].action != "check" {
		t.Fatal("connected account was still prompted to sign in")
	}
}

func TestViewerDependencyCheckRejectsLateResults(t *testing.T) {
	m := authTestViewer(t)
	m.authID = 10
	m.Update(mapboxDependencyResult{id: 9, status: mapboxDependencyStatus{State: "ready"}})
	if m.authCLI.State != "" {
		t.Fatal("obsolete dependency check replaced current state")
	}
	m.Update(mapboxDependencyResult{id: 10, status: mapboxDependencyStatus{State: "missing"}})
	if m.authCLI.State != "missing" || m.authBusy {
		t.Fatal("current dependency check was not accepted")
	}
	_, content, _ := m.mapboxDialogContent(52, 8)
	if !strings.Contains(ansi.Strip(content), "Check failed:") {
		t.Fatal("dependency recheck did not display explicit failure")
	}
}

func TestViewerDependencyRecheckReusesSavedLogin(t *testing.T) {
	m := authTestViewer(t)
	fakeMapbox(t, "good")
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	m.openDialog(dialogMapbox)
	check := m.checkMapboxSetup()
	_, discover := m.Update(check())
	if !strings.Contains(m.authMessage, "checking connection") {
		t.Fatal("dependency recheck did not wait for the connection result")
	}
	if discover == nil {
		t.Fatal("validated dependency did not discover the saved login")
	}
	discover = m.discoverCredentialsWith(false, true, func(context.Context, *Provider) authSection {
		return authSection{State: "ready"}
	})
	m.Update(discover())
	if m.provider.Kind != "mapbox" || m.provider.token != "pk.synthetic-login-token" || m.ui.dialog != dialogMapbox {
		t.Fatal("dependency recheck failed to reuse login in the current viewer")
	}
	calls, err := os.ReadFile(os.Getenv("SHOE_MAPBOX_TEST_LOG"))
	if err != nil || strings.Contains(string(calls), `"auth","login"`) {
		t.Fatal("dependency recheck started browser login", err)
	}
	if !strings.Contains(m.authMessage, "Check passed: Mapbox Streets is ready") {
		t.Fatal("connection recheck did not report the Streets result")
	}
}

func TestViewerConnectionCheckReportsFinalCredentialsAndStreets(t *testing.T) {
	for _, test := range []struct {
		name, token, access, guidance, expected string
	}{
		{name: "removed login", expected: "Check failed:"},
		{name: "environment ready", token: "pk.environment", access: "ready", expected: "Check passed:"},
		{name: "environment rejected", token: "pk.environment", access: "unavailable", guidance: "HTTP 403", expected: "HTTP 403"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := authTestViewer(t)
			fakeMapbox(t, "loggedout")
			if test.token == "" {
				if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("MAPBOX_ACCESS_TOKEN", test.token)
			}
			m.openDialog(dialogMapbox)
			probes := 0
			check := m.discoverCredentialsWith(false, true, func(_ context.Context, p *Provider) authSection {
				probes++
				if p.token != test.token {
					t.Fatal("connection check probed the wrong rendering token")
				}
				return authSection{State: test.access, Guidance: test.guidance}
			})
			m.Update(check())
			if !strings.Contains(m.authMessage, test.expected) || m.authBusy {
				t.Fatal("connection check did not report its final result", m.authMessage)
			}
			if (probes == 1) != (test.token != "") {
				t.Fatal("connection check skipped Streets or probed without credentials")
			}
		})
	}
}

func TestViewerCredentialDiscoveryPreservesSelectedSetupAction(t *testing.T) {
	for _, action := range []string{"install", "telemetry", "continue"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			m := authTestViewer(t)
			m.openDialog(dialogMapbox)
			m.authBusy = true
			for i, command := range m.mapboxActions() {
				if command.action == action {
					m.ui.selected = i
				}
			}
			m.acceptCredentials(credentialResult{credential: renderingCredential{
				source: "none", dependency: mapboxDependencyStatus{State: "ready"},
			}})
			if m.mapboxActions()[m.ui.selected].action != action {
				t.Fatal("credential discovery changed the selected action")
			}
			if action == "continue" {
				m.updateMapboxDialog("enter")
				if m.ui.dialog != dialogNone || m.preferences.MapboxCLITelemetry {
					t.Fatal("Continue offline opted into telemetry instead of closing")
				}
			}
		})
	}
}

func TestPaletteReflectsOfflineAndConnectedFeatures(t *testing.T) {
	m := authTestViewer(t)
	commands := m.commands()
	if commands[0].action != "help" || commands[1].action != "auth" {
		t.Fatal("shortcuts and disconnected account setup were not first")
	}
	for _, command := range commands {
		if command.action == "credits" {
			t.Fatal("offline palette showed irrelevant provider credits")
		}
	}
	m.provider.token = "pk.connected"
	commands = m.commands()
	var settings, planning bool
	for _, command := range commands {
		if command.action == "auth" {
			settings = command.title == "Mapbox settings"
		}
		planning = planning || command.action == "journey"
	}
	if !settings || !planning || commands[0].action != "help" {
		t.Fatal("connected palette lost settings or offline-capable planning")
	}
	m.sources.set("basemap", m.provider, true)
	m.sources.states[0].snapshot.Credits = providerCredits("mapbox")
	var credits bool
	for _, command := range m.commands() {
		credits = credits || command.action == "credits"
	}
	if !credits {
		t.Fatal("contributing map data lost its credits action")
	}
}
