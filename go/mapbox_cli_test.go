package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeMapboxSchema = `{"schema_version":1,"global_options":[{"flag":"--profile"},{"flag":"--use-login"},{"flag":"--timeout"},{"flag":"--output","values":["text","json"]}],"commands":[{"command":"mapbox auth login","arguments":[]},{"command":"mapbox auth logout","arguments":[]},{"command":"mapbox auth whoami","arguments":[]},{"command":"mapbox accounts list-tokens","arguments":[{"flag":"--default"},{"flag":"--usage","values":["pk","sk"]}]},{"command":"mapbox accounts retrieve-token","arguments":[]},{"command":"mapbox usage","arguments":[{"flag":"--token-id"},{"flag":"--period-start"},{"flag":"--period-end"}]}]}`

func fakeMapbox(t *testing.T, scenario string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	directory := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "mapbox")
	script := "#!/bin/sh\nexec \"" + binary + "\" -test.run=^TestMapboxCLIProcess$ -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPBOX_TUI_TEST", scenario)
	// Each fake CLI call exits a race-instrumented process; avoid its default one-second exit delay.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	t.Setenv("MAPBOX_TUI_TEST_LOG", filepath.Join(directory, "calls.jsonl"))
	t.Setenv("MAPBOX_TUI_CLI", path)
	return path
}

func TestMapboxCLIProcess(t *testing.T) {
	scenario := os.Getenv("MAPBOX_TUI_TEST")
	if scenario == "" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("mapbox 0.3.0")
		os.Exit(0)
	}
	if len(args) == 1 && args[0] == "--schema" {
		if scenario == "incompatible" {
			fmt.Println(`{"schema_version":2}`)
		} else if scenario == "no-public-filter" {
			fmt.Println(strings.ReplaceAll(fakeMapboxSchema, `["pk","sk"]`, `["sk"]`))
		} else {
			fmt.Println(fakeMapboxSchema)
		}
		os.Exit(0)
	}
	log, err := os.OpenFile(os.Getenv("MAPBOX_TUI_TEST_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	entry := map[string]any{"args": args, "username": os.Getenv("MAPBOX_USERNAME"), "debug": os.Getenv("MAPBOX_DEBUG")}
	if json.NewEncoder(log).Encode(entry) != nil || log.Close() != nil {
		os.Exit(2)
	}
	joined := strings.Join(args, " ")
	fail := func(code string) {
		fmt.Fprintln(os.Stderr, "Warning: refresh rejected tk.never-print-this")
		fmt.Fprintf(os.Stderr, `{"code":%q,"message":"pk.never-print-this","extra":"sk.never-print-this"}`+"\n", code)
		os.Exit(1)
	}
	if scenario == "failure" {
		fail("unauthorized")
	}
	if scenario == "hang" {
		time.Sleep(time.Hour)
	}
	if scenario == "oversized" {
		fmt.Print(strings.Repeat("x", 3*1024*1024))
		os.Exit(0)
	}
	if scenario == "malformed" {
		fmt.Print("pk.never-print-this not JSON")
		os.Exit(0)
	}
	switch {
	case strings.Contains(joined, "auth whoami"):
		if scenario == "loggedout" {
			fail("not_authenticated")
		}
		fmt.Println(`{"account":"sampleaccount","expires_at":1}`)
	case strings.Contains(joined, "accounts list-tokens"):
		if scenario == "loggedout" {
			fail("not_authenticated")
		}
		if scenario == "no-public-token" {
			fmt.Println(`[]`)
		} else {
			fmt.Println(`[{"id":"public-id","token":"pk.synthetic-login-token"}]`)
		}
	case strings.Contains(joined, "accounts retrieve-token"):
		account := "sampleaccount"
		if os.Getenv("MAPBOX_ACCESS_TOKEN") == "pk.override" {
			account = "otheraccount"
		}
		code := "TokenValid"
		if scenario == "invalid-token" {
			code = "TokenExpired"
		}
		fmt.Printf(`{"code":%q,"token":{"user":%q,"authorization":"public-id"}}`+"\n", code, account)
	case strings.Contains(joined, "auth logout"):
		fmt.Println(`{"logged_out":true}`)
	case strings.Contains(joined, "auth login"):
		fmt.Println("Logged in successfully.")
	case strings.Contains(joined, "usage"):
		if scenario == "usage-forbidden" {
			fail("forbidden")
		}
		flag := func(name string) string {
			for i, arg := range args {
				if arg == name && i+1 < len(args) {
					return args[i+1]
				}
			}
			return ""
		}
		products := map[string]any{}
		if scenario != "no-usage" {
			products["Vector Tiles API"] = map[string]any{"daily": []any{
				map[string]any{"date": flag("--period-end"), "usage": 42},
			}}
		}
		data := map[string]any{"period": usagePeriod{Start: flag("--period-start"), End: flag("--period-end")},
			"token_id": flag("--token-id"), "products": products}
		if scenario == "wrong-period" {
			data["period"] = usagePeriod{Start: "2020-01-01", End: "2020-01-02"}
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"data": data, "generated_at": "2026-10-09T00:00:00Z"})
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestMapboxCLIContractAndSafeFailures(t *testing.T) {
	for _, scenario := range []string{"good", "incompatible", "no-public-filter", "failure", "malformed", "oversized", "hang"} {
		t.Run(scenario, func(t *testing.T) {
			fakeMapbox(t, scenario)
			t.Setenv("MAPBOX_USERNAME", "wrong-account")
			t.Setenv("MAPBOX_DEBUG", "true")
			t.Setenv("MAPBOX_ACCESS_TOKEN", "pk.environment")
			cli, err := findMapboxCLI(t.Context())
			if scenario == "incompatible" || scenario == "no-public-filter" {
				if err == nil {
					t.Fatal("incompatible CLI was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if scenario == "hang" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			}
			credential, err := cli.publicCredential(ctx)
			if scenario == "good" {
				if err != nil || credential.source != "login" || credential.token != "pk.synthetic-login-token" {
					t.Fatal("public login token was not retrieved", err)
				}
				data, err := os.ReadFile(os.Getenv("MAPBOX_TUI_TEST_LOG"))
				if err != nil || strings.Contains(string(data), "wrong-account") || strings.Contains(string(data), "\"debug\":\"true\"") {
					t.Fatal("child inherited account/debug overrides", err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "never-print") {
				t.Fatal("CLI failure lost its safe boundary", err)
			}
		})
	}
}

func TestCredentialOptOutAndEnvironmentSkipCLI(t *testing.T) {
	t.Setenv("MAPBOX_TUI_CLI", "/missing/mapbox")
	for _, token := range []string{"", "pk.explicit"} {
		t.Run("token="+token, func(t *testing.T) {
			t.Setenv("MAPBOX_ACCESS_TOKEN", token)
			credential := resolveCredential(t.Context())
			if credential.token != token || strings.Contains(credential.guidance, "CLI not found") {
				t.Fatal("explicit environment consulted the external CLI")
			}
		})
	}
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	credential := resolveCredential(t.Context())
	if credential.token != "" || !strings.Contains(credential.guidance, mapboxInstallURL) {
		t.Fatal("missing optional CLI did not produce install guidance")
	}
}

func TestMapboxSystemCheckUsesPATHAndExplicitOverride(t *testing.T) {
	t.Setenv("MAPBOX_TUI_CLI", "")
	t.Setenv("PATH", t.TempDir())
	cli, status := checkMapboxCLI(t.Context())
	if cli != nil || status.State != "missing" || status.Checked != "PATH" || status.InstallURL != mapboxInstallURL {
		t.Fatal("system check did not report missing PATH dependency")
	}
	path := fakeMapbox(t, "good")
	t.Setenv("MAPBOX_TUI_CLI", "")
	t.Setenv("PATH", filepath.Dir(path))
	cli, status = checkMapboxCLI(t.Context())
	if cli == nil || status.State != "ready" || status.Path != path || status.Checked != "PATH" {
		t.Fatal("system check did not find and validate the PATH executable")
	}
	if _, err := os.Stat(os.Getenv("MAPBOX_TUI_TEST_LOG")); !os.IsNotExist(err) {
		t.Fatal("dependency check made an account request")
	}
	t.Setenv("MAPBOX_TUI_CLI", filepath.Join(t.TempDir(), "missing"))
	cli, status = checkMapboxCLI(t.Context())
	if cli != nil || status.State != "missing" || status.Checked != "MAPBOX_TUI_CLI" {
		t.Fatal("system check ignored a broken explicit executable selection")
	}
}

func TestAuthCommandReportsCredentialLocation(t *testing.T) {
	fakeMapbox(t, "good")
	directory := filepath.Join(t.TempDir(), "Mapbox credentials")
	t.Setenv("MAPBOX_CONFIG_DIR", directory)
	file, err := mapboxCredentialFile()
	if err != nil || file != filepath.Join(directory, "credentials.json") {
		t.Fatal("custom store did not resolve", err)
	}
	var output bytes.Buffer
	err = runAuth(t.Context(), cliCommand{AuthAction: "logout"}, authStreams{in: strings.NewReader(""), out: &output, err: io.Discard})
	if err != nil || !strings.Contains(output.String(), file) || !strings.Contains(output.String(), "shared default profile") {
		t.Fatal("auth command omitted the credential location", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("mapbox-tui created or read a credential store")
	}
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	output.Reset()
	err = runAuth(t.Context(), cliCommand{AuthAction: "login"}, authStreams{in: strings.NewReader(""), out: &output, err: io.Discard})
	if err != nil || !strings.Contains(output.String(), "login saved by the CLI at "+file) {
		t.Fatal("successful login omitted the saved credential path", err)
	}
}
