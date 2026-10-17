package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBillingGuidanceKeepsRedirectedOutputPlain(t *testing.T) {
	var output bytes.Buffer
	if text := billingGuidanceForOutput(&output); text != billingGuidance() || strings.Contains(text, "\x1b") {
		t.Fatal("redirected billing guidance contained terminal escapes")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if text := billingGuidanceForOutput(writer); text != billingGuidance() || strings.Contains(text, "\x1b") {
		t.Fatal("a pipe was mistaken for an interactive terminal")
	}
	linked := linkedBillingGuidance()
	if ansi.Strip(linked) != billingGuidance() {
		t.Fatal("hyperlinks changed the billing notice text")
	}
	for _, url := range []string{billingDocsURL, mapboxUsageURL, mapboxAlertsURL, mapboxPricingURL} {
		if !strings.Contains(linked, ansi.SetHyperlink(url)) {
			t.Fatal("missing billing hyperlink", url)
		}
	}
}

func TestBillingAcknowledgementPrecedesCLILoginAndPersists(t *testing.T) {
	fakeMapbox(t, "good")
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	var output bytes.Buffer
	streams := authStreams{in: strings.NewReader("yes\n"), out: &output, err: io.Discard}
	command := cliCommand{AuthAction: "login"}
	if err := runAuth(t.Context(), command, streams); err == nil || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatal("noninteractive login did not require explicit acknowledgement", err)
	}
	if !strings.Contains(output.String(), billingNotice) || !strings.Contains(output.String(), mapboxAlertsURL) {
		t.Fatal("CLI login did not present the shared guidance")
	}
	if _, err := os.Stat(os.Getenv("MAPBOX_TUI_TEST_LOG")); !os.IsNotExist(err) {
		t.Fatal("CLI login ran before acknowledgement", err)
	}
	command.AcceptBilling = true
	output.Reset()
	if err := runAuth(t.Context(), command, streams); err != nil {
		t.Fatal(err)
	}
	assertLoginBillingGuidance(t, output.String())
	settings, err := loadAppSettings()
	if err != nil || !settings.billingAccepted() || settings.Offline {
		t.Fatal("acceptance did not persist", settings, err)
	}
	output.Reset()
	command.AcceptBilling = false
	if err := runAuth(t.Context(), command, streams); err != nil {
		t.Fatal("repeat login asked for acknowledgement again", err)
	}
	assertLoginBillingGuidance(t, output.String())
	output.Reset()
	if err := saveAppSettings(appSettings{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "1")
	if err := runAuth(t.Context(), command, streams); err != nil {
		t.Fatal(err)
	}
	assertLoginBillingGuidance(t, output.String())
}

func assertLoginBillingGuidance(t *testing.T, output string) {
	t.Helper()
	notice, login := strings.Index(output, billingNotice), strings.Index(output, "Mapbox credentials:")
	if notice < 0 || login <= notice || !strings.Contains(output, mapboxUsageURL) ||
		!strings.Contains(output, mapboxAlertsURL) || strings.Contains(output, "[y/N]") {
		t.Fatal("acknowledged login did not show guidance before login without prompting", output)
	}
}

func TestBillingBlocksCLIAndMCPProductRequests(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings appSettings
	}{
		{name: "unacknowledged"},
		{name: "old acknowledgement", settings: appSettings{BillingAccepted: billingNoticeVersion + 1}},
		{name: "offline", settings: appSettings{BillingAccepted: billingNoticeVersion, Offline: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
			t.Setenv("MAPBOX_ACCESS_TOKEN", "pk.fixture")
			if err := saveAppSettings(test.settings); err != nil {
				t.Fatal(err)
			}
			if err := requireMapboxOnline("pk.fixture"); err == nil {
				t.Fatal("CLI policy allowed a product request")
			}
			p := providerForCredential("mapbox", renderingCredential{token: "pk.fixture"})
			s := &Service{providers: map[string]*Provider{"mapbox": p}}
			if _, err := s.render(t.Context(), Args{}); err == nil {
				t.Fatal("MCP bypassed billing policy")
			}
			if _, err := s.render(t.Context(), Args{Provider: "none"}); err != nil {
				t.Fatal("billing policy prevented offline rendering", err)
			}
			if requireMapboxOnline("") != nil {
				t.Fatal("missing credentials prevented local use")
			}
		})
	}
}

func TestAuthStatusDoesNotProbeBeforeAcknowledgementOrWhileOffline(t *testing.T) {
	fakeMapbox(t, "good")
	t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
	t.Setenv("MAPBOX_ACCESS_TOKEN", "pk.synthetic-login-token")
	probe := func(context.Context, *Provider) authSection {
		t.Fatal("auth status made a billable probe")
		return authSection{}
	}
	for _, settings := range []appSettings{{}, {BillingAccepted: billingNoticeVersion, Offline: true}} {
		if err := saveAppSettings(settings); err != nil {
			t.Fatal(err)
		}
		status, err := collectAuthStatusWith(t.Context(), true, probe)
		if err != nil || status.Rendering.Streets.State != "not_checked" || status.Usage.Account.State != "available" {
			t.Fatal("status did not separate statistics from billable probes", status, err)
		}
	}
}
