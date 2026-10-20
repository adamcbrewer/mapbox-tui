package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenStdinLoginAndLogoutWithoutCLI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAPBOX_TUI_CLI", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	fakePublicTokenValidation(t, "TokenValid", "sampleaccount")
	if _, err := saveMapboxOffline(true); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(t.TempDir(), "token")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("pk.private-token\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	streams := authStreams{in: file, out: &output, err: &output}
	err = runAuth(t.Context(), cliCommand{AuthAction: "login", AuthMethod: "token", TokenStdin: true}, streams)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := loadAppSettings()
	if err != nil || settings.Offline {
		t.Fatal("successful token login did not enable online mode", err)
	}
	if strings.Contains(output.String(), "pk.private-token") {
		t.Fatal("token login exposed the supplied token")
	}
	if err := runAuth(t.Context(), cliCommand{AuthAction: "logout"}, streams); err != nil {
		t.Fatal(err)
	}
	credentials, err := loadAppCredentials()
	if err != nil || credentials.Method != "none" || credentials.Token != "" {
		t.Fatal("token logout retained the token or enabled fallback", err)
	}
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if credential := resolveCredential(t.Context()); credential.token != "" || credential.source != "none" {
		t.Fatal("token logout fell back to another credential")
	}
}

func TestFailedTokenLoginPreservesCredentialsAndOfflineChoice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fakePublicTokenValidation(t, "TokenRevoked", "sampleaccount")
	if err := saveAppCredentials(appCredentials{Method: "token", Token: "pk.original"}); err != nil {
		t.Fatal(err)
	}
	if _, err := saveMapboxOffline(true); err != nil {
		t.Fatal(err)
	}
	streams := authStreams{in: strings.NewReader("pk.revoked\n"), out: io.Discard, err: io.Discard}
	err := runAuth(t.Context(), cliCommand{AuthAction: "login", AuthMethod: "token", TokenStdin: true}, streams)
	if err == nil {
		t.Fatal("revoked token was saved")
	}
	credentials, err := loadAppCredentials()
	if err != nil || credentials.Token != "pk.original" {
		t.Fatal("failed token login replaced previous credentials", err)
	}
	settings, err := loadAppSettings()
	if err != nil || !settings.Offline {
		t.Fatal("failed token login changed offline mode", err)
	}
}

func TestBrowserLoginExplicitlyReplacesTokenSelection(t *testing.T) {
	fakeMapbox(t, "good")
	if err := saveAppCredentials(appCredentials{Method: "token", Token: "pk.previous"}); err != nil {
		t.Fatal(err)
	}
	streams := authStreams{in: strings.NewReader(""), out: io.Discard, err: io.Discard}
	if err := performMapboxLogin(t.Context(), cliCommand{AuthMethod: "browser"}, streams, nil); err != nil {
		t.Fatal(err)
	}
	credentials, err := loadAppCredentials()
	if err != nil || credentials.Method != "browser" || credentials.Token != "" {
		t.Fatal("browser login did not replace the saved token selection", err)
	}
}

func TestReadAuthInputCancellationAndSecretFormat(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	cancel()
	if _, err := readAuthInput(ctx, authStreams{in: input}, false); err == nil {
		t.Fatal("cancelled input was read")
	}
	for _, token := range []string{"sk.private", "tk.private", "", "pk.", "pk.bad\x1bvalue", "pk.bad value"} {
		if err := checkPublicTokenFormat(token); err == nil {
			t.Fatal("non-public or malformed token was accepted")
		}
	}
}
