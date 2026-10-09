package main

import (
	"os"
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
