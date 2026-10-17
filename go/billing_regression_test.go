package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestBillingRegressionUnsuccessfulCLILoginPreservesOffline(t *testing.T) {
	for _, scenario := range []string{"failure", "hang"} {
		t.Run(scenario, func(t *testing.T) {
			fakeMapbox(t, scenario)
			t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
			t.Setenv("MAPBOX_ACCESS_TOKEN", "")
			if err := saveAppSettings(appSettings{BillingAccepted: billingNoticeVersion, Offline: true}); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			streams := authStreams{in: strings.NewReader(""), out: &output, err: io.Discard}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- runAuth(ctx, cliCommand{AuthAction: "login"}, streams) }()
			if scenario == "hang" {
				deadline := time.Now().Add(3 * time.Second)
				for {
					data, _ := os.ReadFile(os.Getenv("MAPBOX_TUI_TEST_LOG"))
					if strings.Contains(string(data), `"login"`) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("fake login did not start")
					}
					time.Sleep(5 * time.Millisecond)
				}
				cancel()
			}
			if err := <-done; err == nil {
				t.Fatal("fake login unexpectedly succeeded")
			}
			settings, err := loadAppSettings()
			if err != nil {
				t.Fatal(err)
			}
			if !settings.Offline {
				t.Fatalf("unsuccessful login cleared saved offline mode; notice shown=%v", strings.Contains(output.String(), billingNotice))
			}
		})
	}
}

func TestBillingRegressionViewerTogglesPreserveSavedOffline(t *testing.T) {
	for _, action := range []string{"telemetry", "traffic"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
			t.Setenv("MAPBOX_CLI_NO_TELEMETRY", "")
			if err := saveAppSettings(appSettings{BillingAccepted: billingNoticeVersion, Traffic: true}); err != nil {
				t.Fatal(err)
			}
			m := authTestViewer(t)
			if err := runAuth(t.Context(), cliCommand{AuthAction: "offline"}, authStreams{out: io.Discard}); err != nil {
				t.Fatal(err)
			}
			m.openDialog(dialogMapbox)
			if action == "traffic" {
				m.toggleTraffic()
			} else {
				selectMapboxAction(t, m, action)
				m.updateMapboxDialog("enter")
			}
			settings, err := loadAppSettings()
			if err != nil {
				t.Fatal(err)
			}
			if !settings.Offline {
				t.Fatalf("an unrelated preference toggle restored online mode from the viewer's old snapshot: %+v", settings)
			}
		})
	}
}

func TestBillingRegressionClosingSetupDoesNotLosePostLoginUsage(t *testing.T) {
	fakeMapbox(t, "good")
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
	if err := saveAppSettings(appSettings{BillingAccepted: billingNoticeVersion}); err != nil {
		t.Fatal(err)
	}
	m := authTestViewer(t)
	t.Cleanup(m.cancelAccountUsage)
	m.usage = usageReport{State: "unavailable", Guidance: "No saved login at startup"}
	m.openDialog(dialogMapbox)
	ready := m.beginMapboxLogin()().(mapboxLoginReady)
	if ready.err != nil {
		t.Fatal(ready.err)
	}
	if err := ready.cli.login(ready.ctx, authStreams{in: strings.NewReader(""), out: io.Discard, err: io.Discard}); err != nil {
		t.Fatal(err)
	}
	_, discovery := m.Update(mapboxLoginDone{id: ready.id})
	if discovery == nil {
		t.Fatal("successful login did not start credential discovery")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	for _, command := range discovery().(tea.BatchMsg) {
		m.Update(command())
	}
	if m.usage.State != "available" {
		t.Fatal("successful login lost account usage after closing setup during credential discovery", m.usage)
	}
	calls, err := os.ReadFile(os.Getenv("MAPBOX_TUI_TEST_LOG"))
	if err != nil || strings.Count(string(calls), `"usage"`) != 1 {
		t.Fatal("post-login statistics were missing or repeated", err)
	}
}

func TestBillingRegressionStartupJourneyDoesNotRetainAcceptedBillingDialog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
	m := authTestViewer(t)
	m.args.Journey, _ = parseStartupJourney("51.3,-2.5", "51.4,-2.6", nil, "manual")
	m.windowSizeKnown = true
	m.openBilling("online")
	m.startJourneyFromArgs()
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.ui.dialog != dialogJourney || !m.preferences.billingAccepted() {
		t.Fatal("acceptance did not return to the startup journey")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog == dialogMapboxBilling {
		t.Fatal("Escape from the journey reopened the already accepted billing confirmation")
	}
}

func TestBillingRegressionOfflineStopsDespitePersistenceError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
	if err := saveAppSettings(appSettings{BillingAccepted: billingNoticeVersion}); err != nil {
		t.Fatal(err)
	}
	m := authTestViewer(t)
	m.acceptCredentials(credentialResult{id: m.authID, credential: renderingCredential{token: "pk.fixture", source: "login"}})
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", blocked)
	m.setMapboxOffline(true)
	if m.provider.token != "" || m.places.token != "" || m.directions.token != "" {
		t.Fatal("choosing offline retained billable clients when saving the preference failed")
	}
	if !strings.Contains(m.authMessage, "Offline for this session") || m.ui.dialog != dialogMapbox {
		t.Fatal("session-only offline fallback did not explain the persistence failure", m.authMessage)
	}
}
