package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakePublicTokenValidation(t *testing.T, verdict, account string) {
	t.Helper()
	previous := mapboxTokenClient
	mapboxTokenClient = &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.mapbox.com" || r.URL.Path != "/tokens/v2" {
			t.Error("token validation used an unexpected endpoint")
		}
		if !strings.HasPrefix(r.URL.Query().Get("access_token"), "pk.") {
			t.Error("token validation did not send the supplied public token")
		}
		body, _ := json.Marshal(map[string]any{"code": verdict,
			"token": map[string]string{"user": account, "authorization": "public-id"}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	t.Cleanup(func() { mapboxTokenClient = previous })
}

func TestAppCredentialsArePrivateAndSeparateFromCLILogin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAPBOX_CONFIG_DIR", t.TempDir())
	cliFile, err := mapboxCredentialFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliFile, []byte("original CLI credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveAppCredentials(appCredentials{Method: "token", Token: "pk.private-test-token"}); err != nil {
		t.Fatal(err)
	}
	path, err := appCredentialsPath()
	if err != nil || path != filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "mapbox-tui", "credentials.json") {
		t.Fatal("saved token ignored the platform config directory", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("saved token is not owner-only", err)
	}
	stored, err := loadAppCredentials()
	if err != nil || stored.Method != "token" || stored.Token != "pk.private-test-token" {
		t.Fatal("saved token did not round-trip", err)
	}
	cliData, err := os.ReadFile(cliFile)
	if err != nil || string(cliData) != "original CLI credentials" {
		t.Fatal("saving a public token modified CLI credentials", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "settings.json")); !os.IsNotExist(err) {
		t.Fatal("token storage wrote ordinary settings", err)
	}
}

func TestSavedTokenSelectionAndEnvironmentOverride(t *testing.T) {
	fakeMapbox(t, "good")
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := saveAppCredentials(appCredentials{Method: "token", Token: "pk.saved-token"}); err != nil {
		t.Fatal(err)
	}
	credential := resolveCredential(t.Context())
	if credential.source != "saved_token" || credential.token != "pk.saved-token" {
		t.Fatal("saved token did not take precedence over CLI login")
	}
	if _, err := os.Stat(os.Getenv("MAPBOX_TUI_TEST_LOG")); !os.IsNotExist(err) {
		t.Fatal("saved token discovery depended on the CLI", err)
	}
	t.Setenv("MAPBOX_ACCESS_TOKEN", "pk.environment-token")
	if credential := resolveCredential(t.Context()); credential.source != "environment" || credential.token != "pk.environment-token" {
		t.Fatal("environment token did not override saved token")
	}
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	if credential := resolveCredential(t.Context()); credential.source != "disabled" || credential.token != "" {
		t.Fatal("empty environment override did not disable saved token")
	}
}

func TestBrokenSavedCredentialsDoNotFallBackOrLeakSecrets(t *testing.T) {
	for _, test := range []struct{ name, data string }{
		{name: "malformed", data: `{"method":"token","public_token":"pk.never-print"`},
		{name: "missing token", data: `{"method":"token"}`},
		{name: "wrong method", data: `{"method":"pk.never-print"}`},
		{name: "secret token", data: `{"method":"token","public_token":"sk.never-print"}`},
		{name: "unknown field", data: `{"method":"browser","extra":"pk.never-print"}`},
		{name: "trailing JSON", data: `{"method":"browser"} {"public_token":"pk.never-print"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakeMapbox(t, "good")
			t.Setenv("MAPBOX_ACCESS_TOKEN", "")
			if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
				t.Fatal(err)
			}
			path, _ := appCredentialsPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			credential := resolveCredential(t.Context())
			if credential.token != "" || credential.guidance == "" || strings.Contains(credential.guidance, "never-print") {
				t.Fatal("broken credential was used, fell back or leaked a token")
			}
			if _, err := os.Stat(os.Getenv("MAPBOX_TUI_TEST_LOG")); !os.IsNotExist(err) {
				t.Fatal("broken saved token fell back to CLI login", err)
			}
		})
	}
}

func TestSavedTokenStatusWithoutCLIAndForeignAccountUsage(t *testing.T) {
	for _, cliAvailable := range []bool{false, true} {
		name := "without CLI"
		if cliAvailable {
			name = "with different CLI account"
		}
		t.Run(name, func(t *testing.T) {
			fakeMapbox(t, "good")
			fakePublicTokenValidation(t, "TokenValid", "tokenaccount")
			t.Setenv("MAPBOX_ACCESS_TOKEN", "")
			if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
				t.Fatal(err)
			}
			if !cliAvailable {
				t.Setenv("MAPBOX_TUI_CLI", filepath.Join(t.TempDir(), "missing"))
			}
			if err := saveAppCredentials(appCredentials{Method: "token", Token: "pk.private-token"}); err != nil {
				t.Fatal(err)
			}
			status, err := collectAuthStatusWith(t.Context(), true, func(context.Context, *Provider) authSection {
				return authSection{State: "ready"}
			})
			if err != nil || status.Rendering.Validity != "valid" || status.Rendering.Account != "tokenaccount" {
				t.Fatal("saved token was not validated independently of CLI", err)
			}
			if cliAvailable && (status.Usage.Account.Account != "sampleaccount" || status.Usage.Token.State != "unavailable") {
				t.Fatal("account usage was attributed to the supplied token's different account")
			}
			data, _ := json.Marshal(status)
			if strings.Contains(string(data)+status.text(), "pk.private-token") {
				t.Fatal("status exposed the saved token")
			}
		})
	}
}

func TestPublicTokenValidationRejectsRevokedAndUnknownVerdicts(t *testing.T) {
	for _, verdict := range []string{"TokenRevoked", "TokenExpired", "unexpected"} {
		t.Run(verdict, func(t *testing.T) {
			fakePublicTokenValidation(t, verdict, "sampleaccount")
			status, err := validatePublicToken(t.Context(), "pk.private-token")
			if err == nil || status.Validity == "valid" || strings.Contains(err.Error(), "pk.private-token") {
				t.Fatal("non-valid HTTP 200 verdict was accepted or leaked a token")
			}
		})
	}
}
