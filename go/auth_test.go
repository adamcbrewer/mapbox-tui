package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestAuthStatusSeparatesLoginRenderingAndUsage(t *testing.T) {
	for _, test := range []struct {
		name, scenario, override, validity, usage string
	}{
		{name: "refreshed login", scenario: "good", validity: "valid", usage: "available"},
		{name: "no usage", scenario: "no-usage", validity: "valid", usage: "empty"},
		{name: "statistics denied", scenario: "usage-forbidden", validity: "valid", usage: "unavailable"},
		{name: "wrong report period", scenario: "wrong-period", validity: "valid", usage: "unavailable"},
		{name: "token verdict in HTTP 200", scenario: "invalid-token", validity: "invalid", usage: "available"},
		{name: "foreign rendering account", scenario: "good", override: "pk.override", validity: "valid", usage: "available"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakeMapbox(t, test.scenario)
			t.Setenv("MAPBOX_ACCESS_TOKEN", "")
			if test.override == "" {
				if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("MAPBOX_ACCESS_TOKEN", test.override)
			}
			probes := 0
			status, err := collectAuthStatusWith(t.Context(), true, func(_ context.Context, p *Provider) authSection {
				probes++
				if p.token == "" {
					t.Fatal("Streets probe had no selected token")
				}
				return authSection{State: "ready"}
			})
			if err != nil || status.Login.State != "usable" || status.Rendering.Validity != test.validity || probes != 1 {
				t.Fatal("status conflated login or token readiness", err)
			}
			if status.Usage.Account.State != test.usage {
				t.Fatal("unexpected account usage state", status.Usage.Account)
			}
			if test.override != "" || test.validity != "valid" {
				if status.Usage.Token.State != "unavailable" {
					t.Fatal("unverified/foreign token received logged-in account statistics")
				}
				calls, err := os.ReadFile(os.Getenv("MAPBOX_TUI_TEST_LOG"))
				if err != nil || strings.Contains(string(calls), "\"--token-id\"") {
					t.Fatal("ineligible token usage was requested", err)
				}
			} else if test.usage == "available" {
				if status.Usage.Token.Requests != 42 || *status.Usage.Account.Period != *status.Usage.Token.Period {
					t.Fatal("usage reports did not share dates or sum daily requests")
				}
			}
			data, err := json.Marshal(status)
			if err != nil || strings.Contains(string(data)+status.text(), "pk.") || strings.Contains(string(data), "never-print") {
				t.Fatal("status exposed token-bearing JSON", err)
			}
		})
	}
}

func TestAuthStatusLoggedOutAndMissingCLIStillReports(t *testing.T) {
	fakeMapbox(t, "loggedout")
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	probe := func(context.Context, *Provider) authSection {
		t.Fatal("logged-out status made a tile request")
		return authSection{}
	}
	status, err := collectAuthStatusWith(t.Context(), false, probe)
	if err != nil || status.Login.State != "logged_out" || status.Rendering.Present {
		t.Fatal("logged-out report failed", err)
	}
	t.Setenv("MAPBOX_TUI_CLI", "/missing/mapbox")
	status, err = collectAuthStatusWith(t.Context(), true, probe)
	if err != nil || status.CLI.State != "missing" || status.Usage.Account.State != "unavailable" {
		t.Fatal("missing optional dependency prevented status reporting", err)
	}
	if status.CLI.Checked != "MAPBOX_TUI_CLI" || status.CLI.InstallURL != mapboxInstallURL {
		t.Fatal("missing CLI report omitted the system check or installation documentation")
	}
	text := status.text()
	if !strings.Contains(text, "Mapbox CLI: not found") || !strings.Contains(text, "Install Mapbox CLI using the official instructions:") ||
		strings.Count(text, status.CLI.Guidance) != 1 {
		t.Fatal("missing CLI report did not clearly suggest installation once")
	}
}

func TestStreetsProbeDistinguishesActualTileAccess(t *testing.T) {
	p := newProvider("mapbox")
	p.token = "pk.test"
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("pk.do-not-leak"))}, nil
	})
	access := streetsAccess(t.Context(), p)
	if access.State != "unavailable" || !strings.Contains(access.Guidance, "403") || strings.Contains(access.Guidance, "pk.") {
		t.Fatal("Streets access was inferred from a token instead of checked")
	}
}
