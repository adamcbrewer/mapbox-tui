package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSettingsRenamedKeysPreserveValuesAndMigrateOnSave(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fields    string
		version   int
		animation *bool
	}{
		{name: "absent"},
		{name: "legacy enabled", fields: `,"mapbox_billing_accepted":1,"no_animation":false`,
			version: 1, animation: new(true)},
		{name: "legacy disabled", fields: `,"mapbox_billing_accepted":0,"no_animation":true`,
			animation: new(false)},
		{name: "current enabled", fields: `,"mapbox_billing_accepted_version":1,"camera_animation":true`,
			version: 1, animation: new(true)},
		{name: "current disabled", fields: `,"mapbox_billing_accepted_version":0,"camera_animation":false`,
			animation: new(false)},
		{name: "new false and zero win", fields: `,"mapbox_billing_accepted":1,"mapbox_billing_accepted_version":0,` +
			`"no_animation":false,"camera_animation":false`, animation: new(false)},
		{name: "new true and version win", fields: `,"mapbox_billing_accepted":0,"mapbox_billing_accepted_version":2,` +
			`"no_animation":true,"camera_animation":true`, version: 2, animation: new(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, _ := appSettingsPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			data := []byte(`{"traffic":true,"theme":"noir",` +
				`"last_view":{"center_lon":10,"center_lat":20,"zoom":8}` + tc.fields + `}`)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			saved, err := loadAppSettings()
			if err != nil || saved.BillingAcceptedVersion != tc.version {
				t.Fatal("billing notice version changed while reading", saved, err)
			}
			if (saved.CameraAnimation == nil) != (tc.animation == nil) {
				t.Fatal("animation presence changed while reading")
			}
			if tc.animation != nil && *saved.CameraAnimation != *tc.animation {
				t.Fatal("animation value was inverted incorrectly")
			}
			m := preferenceViewer(t)
			if tc.animation != nil && m.settings.NoAnimation == *tc.animation {
				t.Fatal("viewer ignored the migrated animation preference")
			}
			if !bytes.Equal(data, settingsBytes(t)) {
				t.Fatal("reading settings or launching the viewer rewrote the file")
			}
			if _, err := updateAppSettings(func(s *appSettings) { s.ASCII = new(true) }); err != nil {
				t.Fatal(err)
			}
			var written map[string]json.RawMessage
			if err := json.Unmarshal(settingsBytes(t), &written); err != nil {
				t.Fatal(err)
			}
			if _, exists := written["mapbox_billing_accepted"]; exists {
				t.Fatal("save retained the old billing key")
			}
			if _, exists := written["no_animation"]; exists {
				t.Fatal("save retained the old animation key")
			}
			saved, err = loadAppSettings()
			if err != nil || saved.BillingAcceptedVersion != tc.version || !saved.Traffic ||
				saved.Theme != "noir" || saved.LastView == nil || *saved.LastView.Zoom != 8 {
				t.Fatal("migration lost existing settings", saved, err)
			}
			if (saved.CameraAnimation == nil) != (tc.animation == nil) {
				t.Fatal("save changed animation presence")
			}
			if tc.animation != nil && *saved.CameraAnimation != *tc.animation {
				t.Fatal("save changed animation value")
			}
		})
	}
}

func TestSavedViewCoordinateNamesMigrateOnSave(t *testing.T) {
	for _, tc := range []struct {
		name, view string
		lon, lat   float64
	}{
		{name: "legacy", view: `{"zoom":8,"center_lat":20,"center_lon":10}`, lon: 10, lat: 20},
		{name: "current", view: `{"zoom":8,"lat":20,"long":10}`, lon: 10, lat: 20},
		{name: "mixed", view: `{"zoom":8,"lat":20,"center_lon":10}`, lon: 10, lat: 20},
		{name: "new zero wins", view: `{"zoom":8,"lat":0,"long":0,"center_lat":20,"center_lon":10}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, _ := appSettingsPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte(`{"traffic":true,"last_view":` + tc.view + `}`)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			m := preferenceViewer(t)
			if m.camera.Center != project(tc.lon, tc.lat) || m.camera.Zoom != 8 {
				t.Fatal("saved view changed during migration", m.camera)
			}
			if !bytes.Equal(original, settingsBytes(t)) {
				t.Fatal("reading the view rewrote settings")
			}
			if _, err := updateAppSettings(func(s *appSettings) { s.Theme = "noir" }); err != nil {
				t.Fatal(err)
			}
			var written struct {
				Traffic bool               `json:"traffic"`
				View    map[string]float64 `json:"last_view"`
			}
			if err := json.Unmarshal(settingsBytes(t), &written); err != nil {
				t.Fatal(err)
			}
			view := written.View
			_, hasLat := view["lat"]
			_, hasLong := view["long"]
			if !written.Traffic || len(view) != 3 || !hasLat || !hasLong ||
				view["zoom"] != 8 || view["lat"] != tc.lat || view["long"] != tc.lon {
				t.Fatal("save lost coordinates or retained legacy keys", written)
			}
		})
	}
}

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
		t.Fatal("mapbox-tui overrode an explicit environment opt-out")
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

func TestSettingsUpdateDoesNotOverwriteConcurrentWriter(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveAppSettings(appSettings{Offline: true}); err != nil {
		t.Fatal(err)
	}
	path, err := appSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockLocalFile(lock); err != nil {
		t.Fatal(err)
	}
	defer unlockLocalFile(lock)
	called := false
	_, err = updateAppSettings(func(settings *appSettings) {
		called = true
		settings.Offline = false
	})
	if err == nil || called {
		t.Fatal("settings update bypassed another writer's lock", err)
	}
	settings, err := loadAppSettings()
	if err != nil || !settings.Offline {
		t.Fatal("blocked settings update changed the saved preference", settings, err)
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
	m.updateMapboxDialog("space")
	settings, err := loadAppSettings()
	if err != nil || !settings.MapboxCLITelemetry || !strings.Contains(m.authMessage, "Opted in") {
		t.Fatal("settings action did not persist explicit telemetry consent", err)
	}
	if label := m.mapboxActions()[m.ui.selected].label(); label != "CLI telemetry ✓" {
		t.Fatal("enabled telemetry did not show a trailing tick", label)
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
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
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

func TestInvalidSettingsAreNeverReplacedByViewerChanges(t *testing.T) {
	for name, data := range map[string]string{
		"malformed":         `{"traffic":true,`,
		"oversized":         strings.Repeat(" ", 65537),
		"incompatible":      `{"version":99,"ascii":true}`,
		"unknown field":     `{"future_preference":true}`,
		"null":              `null`,
		"wrong type":        `{"ascii":"true"}`,
		"invalid elevation": `{"elevation":"sometimes"}`,
		"trailing data":     `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, err := appSettingsPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			m := preferenceViewer(t)
			if m.settings.ASCII || m.settingsNotice == "" {
				t.Fatal("invalid settings did not use defaults with feedback")
			}
			m.toggleMenuOption("ascii")
			if !m.settings.ASCII || !strings.Contains(m.settingsNotice, "Preference not saved") {
				t.Fatal("failed save did not leave viewer usable with feedback")
			}
			if string(settingsBytes(t)) != data {
				t.Fatal("viewer replaced invalid settings")
			}
		})
	}
}

func TestSettingsFailedWritesPreserveValidFile(t *testing.T) {
	for _, failure := range []string{"validation", "unwritable directory", "unreadable file", "busy"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := saveAppSettings(appSettings{Traffic: true}); err != nil {
				t.Fatal(err)
			}
			before := settingsBytes(t)
			path, _ := appSettingsPath()
			// Create the lock before restricting the directory so failure occurs at replacement preparation.
			if err := os.WriteFile(path+".lock", nil, 0600); err != nil {
				t.Fatal(err)
			}
			var restore func()
			switch failure {
			case "unwritable directory", "unreadable file":
				target, mode := filepath.Dir(path), os.FileMode(0500)
				if failure == "unreadable file" {
					target, mode = path, 0000
				}
				info, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(target, mode); err != nil {
					t.Fatal(err)
				}
				restore = func() { _ = os.Chmod(target, info.Mode().Perm()) }
				t.Cleanup(restore)
			case "busy":
				lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				if err := lockLocalFile(lock); err != nil {
					t.Fatal(err)
				}
				defer unlockLocalFile(lock)
			}
			_, err := updateAppSettings(func(s *appSettings) {
				s.Traffic = false
				if failure == "validation" {
					s.Elevation = "invalid"
				}
			})
			if restore != nil {
				restore()
			}
			if err == nil || string(settingsBytes(t)) != string(before) {
				t.Fatal("failed write lost previous valid settings", err)
			}
		})
	}
}
