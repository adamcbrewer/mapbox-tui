package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapboxTelemetryPreferenceIsOptInAndPersistent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MAPBOX_CLI_NO_TELEMETRY", "")
	settings, err := loadAppSettings()
	if err != nil || settings.MapboxCLITelemetry {
		t.Fatal("telemetry wasn't off by default", err)
	}
	if err := saveAppSettings(appSettings{MapboxCLITelemetry: true}); err != nil {
		t.Fatal(err)
	}
	settings, err = loadAppSettings()
	if err != nil || !settings.MapboxCLITelemetry {
		t.Fatal("explicit telemetry opt-in wasn't persisted", err)
	}
	if env := strings.Join(mapboxEnvironment(nil, settings.MapboxCLITelemetry), "\n"); !strings.Contains(env, "MAPBOX_CLI_NO_TELEMETRY=0") {
		t.Fatal("opt-in didn't reach Mapbox CLI children")
	}
	t.Setenv("MAPBOX_CLI_NO_TELEMETRY", "1")
	if env := strings.Join(mapboxEnvironment(nil, settings.MapboxCLITelemetry), "\n"); !strings.Contains(env, "MAPBOX_CLI_NO_TELEMETRY=1") {
		t.Fatal("shoe overrode an explicit environment opt-out")
	}
	path, err := appSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("settings file didn't retain private permissions", err)
	}
}

func TestMapboxTelemetryToggleSavesSelection(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := authTestViewer(t)
	t.Setenv("MAPBOX_CLI_NO_TELEMETRY", "")
	m.openDialog(dialogMapbox)
	for i, action := range m.mapboxActions() {
		if action.action == "telemetry" {
			m.ui.selected = i
		}
	}
	m.updateMapboxDialog("enter")
	settings, err := loadAppSettings()
	if err != nil || !settings.MapboxCLITelemetry || !strings.Contains(m.authMessage, "Opted in") {
		t.Fatal("settings action did not persist explicit telemetry consent", err)
	}
	m.updateMapboxDialog("enter")
	settings, err = loadAppSettings()
	if err != nil || settings.MapboxCLITelemetry {
		t.Fatal("settings action did not turn telemetry back off", err)
	}
}

func TestSettingsReplacementPreservesOpenReaders(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveAppSettings(appSettings{MapboxCLITelemetry: true}); err != nil {
		t.Fatal(err)
	}
	path, err := appSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := saveAppSettings(appSettings{}); err != nil {
		t.Fatal(err)
	}
	previous, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(previous), `"mapbox_cli_telemetry": true`) {
		t.Fatal("saving settings overwrote an in-progress reader's snapshot", err)
	}
	settings, err := loadAppSettings()
	if err != nil || settings.MapboxCLITelemetry {
		t.Fatal("new readers did not see the replacement settings", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Fatal("saving settings left temporary files behind", err)
	}
}

func TestBrokenTelemetrySettingsDoNotBlockOfflineViewer(t *testing.T) {
	for _, test := range []struct {
		name, data string
		directory  bool
	}{
		{name: "malformed", data: `{"mapbox_cli_telemetry":true,`},
		{name: "oversized", data: strings.Repeat(" ", 65537)},
		{name: "unreadable", directory: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, err := appSettingsPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if test.directory {
				err = os.Mkdir(path, 0700)
			} else {
				err = os.WriteFile(path, []byte(test.data), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			m := authTestViewer(t)
			m.args.Provider, m.args.ProviderSet = "none", true
			m.provider.quiet = true
			runLoadCommand(m, m.load("../fixtures/example.gpx"))
			m.rebuild()
			if !strings.Contains(m.content, "Point 1 / 10") || m.preferences.MapboxCLITelemetry {
				t.Fatal("broken telemetry settings prevented offline geometry or enabled telemetry")
			}
			if !strings.Contains(m.authMessage, "telemetry is off") {
				t.Fatal("viewer did not retain the settings warning")
			}
		})
	}
}

func TestBrokenTelemetrySettingsDoNotBlockMapboxCLI(t *testing.T) {
	fakeMapbox(t, "good")
	path, err := appSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mapbox_cli_telemetry":true,`), 0600); err != nil {
		t.Fatal(err)
	}
	cli, status := checkMapboxCLI(t.Context())
	if cli == nil || status.State != "ready" || cli.telemetry {
		t.Fatal("broken optional settings blocked the CLI or enabled telemetry")
	}
}
