package main

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestViewerGatesExistingTokenAndStillFetchesStartupUsage(t *testing.T) {
	for _, offline := range []bool{false, true} {
		name := "first use"
		if offline {
			name = "offline"
		}
		t.Run(name, func(t *testing.T) {
			fakeMapbox(t, "good")
			t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
			if err := saveAppSettings(appSettings{Offline: offline, Traffic: true}); err != nil {
				t.Fatal(err)
			}
			m := authTestViewer(t)
			t.Cleanup(m.cancelAccountUsage)
			t.Setenv("MAPBOX_ACCESS_TOKEN", "pk.existing")
			m.credential = renderingCredential{token: "pk.existing", source: "environment"}
			m.acceptCredentials(credentialResult{credential: m.credential})
			if m.provider.token != "" || m.places.token != "" || m.directions.token != "" || m.trafficEnabled {
				t.Fatal("existing token enabled billable services before consent or while offline")
			}
			if !offline && m.ui.dialog != dialogMapboxBilling {
				t.Fatal("first use did not request billing acknowledgement")
			}
			batch := m.Init()().(tea.BatchMsg)
			usageResults := 0
			for _, child := range batch {
				if result, ok := child().(accountUsageResult); ok {
					usageResults++
					m.Update(result)
				}
			}
			if usageResults != 1 || m.usage.State != "available" {
				t.Fatal("startup did not fetch account usage once", usageResults, m.usage)
			}
			calls, err := os.ReadFile(os.Getenv("MAPBOX_TUI_TEST_LOG"))
			if err != nil || strings.Count(string(calls), `"usage"`) != 1 {
				t.Fatal("startup repeated statistics requests", err)
			}
		})
	}
}

func TestBillingDialogPersistsOfflineAndRetainsCoordinates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
	m := authTestViewer(t)
	m.openBilling(billingSignIn)
	if billingActions[m.ui.selected].action != "offline" {
		t.Fatal("confirmation defaulted to accepting risks")
	}
	m.updateBillingDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	settings, err := loadAppSettings()
	if err != nil || !settings.Offline || settings.billingAccepted() {
		t.Fatal("Continue offline accepted risks or did not persist", settings, err)
	}
	m.openDialog(dialogSearch)
	body, _ := m.placeSearchContent(64, 10)
	if !strings.Contains(body, "offline") || !strings.Contains(body, "Go online in Mapbox settings") {
		t.Fatal("offline search did not explain coordinate entry and reconnection", body)
	}
	m.ui.search.SetValue("51.3,-2.5")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.searchPosition == nil || m.searchPosition.X != -2.5 || m.searchPosition.Y != 51.3 {
		t.Fatal("offline confirmation disabled coordinate navigation")
	}
	if restarted := authTestViewer(t); !restarted.preferences.Offline {
		t.Fatal("offline mode did not survive viewer restart")
	}
}

func TestGoOfflineKeepsMapboxSettingsOpen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveAppSettings(appSettings{BillingAccepted: billingNoticeVersion}); err != nil {
		t.Fatal(err)
	}
	m := authTestViewer(t)
	m.acceptCredentials(credentialResult{id: m.authID, credential: renderingCredential{token: "pk.fixture", source: "login"}})
	m.openDialog(dialogCommands)
	m.pushDialog(dialogMapbox)
	selectMapboxAction(t, m, "offline")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.ui.dialog != dialogMapbox || m.mapboxActions()[m.ui.selected].action != "online" {
		t.Fatal("going offline closed settings or lost the connection action")
	}
	settings, err := loadAppSettings()
	if err != nil || !settings.Offline || m.provider.token != "" || m.places.token != "" || m.directions.token != "" {
		t.Fatal("going offline did not persist or disable billable services", err)
	}
	_, body, _ := m.mapboxDialogContent(80, 30)
	if !strings.Contains(ansi.Strip(body), "offline") {
		t.Fatal("settings did not display the offline status")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogCommands {
		t.Fatal("going offline lost the parent menu")
	}
}

func TestBillingAndUsageDialogsFitAndRetainMenuSelection(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}, {30, 12}} {
		m := authTestViewer(t)
		m.width, m.height = size[0], size[1]
		m.layout()
		for _, kind := range []dialogKind{dialogMapboxBilling, dialogMapboxUsage} {
			m.openDialog(kind)
			m.rebuild()
			if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
				t.Fatal("billing/usage dialog overflowed terminal", size, kind)
			}
		}
		m.openDialog(dialogMapbox)
		selectMapboxAction(t, m, "usage")
		m.updateMapboxDialog("enter")
		period := currentUsagePeriod(time.Now())
		m.Update(accountUsageResult{id: m.usageID, report: usageReport{State: "empty", Period: &period}})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.ui.dialog != dialogMapbox || m.mapboxActions()[m.ui.selected].action != "usage" {
			t.Fatal("usage response changed the selected menu action")
		}
	}
}

func TestUsageIdentifiesSavedAccountAndPreservesEnvironmentProvenance(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
	credential := renderingCredential{token: "pk.environment", source: "environment"}
	m := &viewer{
		ctx: t.Context(), provider: providerForCredential("mapbox", credential), credential: credential,
		width: 120, height: 40, camera: fit(nil, 88, 34, 2),
	}
	if err := m.initUI(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.sources.close)
	if m.credential.source != "environment" || m.provider.token != "" {
		t.Fatal("startup lost the environment source or enabled products without consent")
	}
	period := currentUsagePeriod(time.Now())
	m.usage = usageReport{State: "available", Account: "saved-account", Period: &period,
		Products: []productUsage{{Name: "Vector Tiles API", Unit: "requests", Total: 123}}}
	summary := strings.Join(m.usageSummary(), "\n")
	for _, text := range []string{"Usage: saved-account (saved login)", "account may differ", "123 requests"} {
		if !strings.Contains(summary, text) {
			t.Fatal("usage summary lost account identity, scope or counts", summary)
		}
	}
	for _, size := range [][2]int{{120, 40}, {70, 24}, {30, 12}} {
		m.width, m.height = size[0], size[1]
		m.layout()
		m.openDialog(dialogMapbox)
		m.rebuild()
		if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
			t.Fatal("account identity overflowed the settings dialog", size)
		}
	}
	m.credential = renderingCredential{source: "login", token: "pk.saved"}
	if strings.Contains(strings.Join(m.usageSummary(), "\n"), "may differ") {
		t.Fatal("saved-login rendering showed an unsupported account mismatch")
	}
}

func TestViewerAndCLIBillingUseSameNotice(t *testing.T) {
	m := authTestViewer(t)
	m.openDialog(dialogMapbox)
	m.openBilling(billingSignIn)
	if hints := m.dialogHints("tab action   esc offline"); !strings.Contains(hints, "esc offline") {
		t.Fatal("nested confirmation mislabelled Escape as back", hints)
	}
	_, body, _ := m.billingDialogContent(200, 60)
	plain := ansi.Strip(body)
	for _, text := range []string{"personal use", "free use is not guaranteed", "bugs", "responsible", "Alerts do not stop"} {
		if !strings.Contains(plain, text) || !strings.Contains(billingGuidance(), text) {
			t.Fatal("billing guidance differs across login paths", text)
		}
	}
}

func TestBillingAndUsageLinksSurviveViewerRendering(t *testing.T) {
	for _, test := range []struct {
		name string
		kind dialogKind
	}{
		{name: "billing", kind: dialogMapboxBilling},
		{name: "usage details", kind: dialogMapboxUsage},
		{name: "Mapbox menu", kind: dialogMapbox},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := authTestViewer(t)
			m.openDialog(test.kind)
			m.rebuild()
			if test.kind == dialogMapboxBilling {
				m.ui.page.GotoBottom()
				m.dirty = true
				m.rebuild()
			}
			if !strings.Contains(m.content, ansi.SetHyperlink(mapboxUsageURL)) {
				t.Fatal("viewer composition dropped the account usage hyperlink")
			}
		})
	}
}

func TestViewerAcknowledgementPreparesLoginAndPreservesParent(t *testing.T) {
	fakeMapbox(t, "good")
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
	m := authTestViewer(t)
	m.openDialog(dialogCommands)
	m.pushDialog(dialogMapbox)
	m.beginMapboxLogin()
	if m.ui.dialog != dialogMapboxBilling || m.authBusy {
		t.Fatal("login began without confirmation")
	}
	m.updateBillingDialog(tea.KeyPressMsg{Code: tea.KeyTab})
	command := m.updateBillingDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil || m.ui.dialog != dialogMapbox || len(m.ui.parents) != 1 {
		t.Fatal("acknowledgement lost the login's parent menu")
	}
	settings, err := loadAppSettings()
	if err != nil || !settings.billingAccepted() || settings.Offline {
		t.Fatal("viewer acknowledgement was not persisted", settings, err)
	}
	ready, ok := command().(mapboxLoginReady)
	if !ok || ready.err != nil {
		t.Fatal("acknowledged login was not prepared", ready.err)
	}
}
