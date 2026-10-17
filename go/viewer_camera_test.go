package main

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func testSavedView(lon, lat, zoom float64) *savedView {
	return &savedView{Lon: &lon, Lat: &lat, Zoom: &zoom}
}

func TestSavedViewValidation(t *testing.T) {
	for _, view := range []*savedView{
		{}, {Lon: new(float64), Lat: new(float64)},
		testSavedView(181, 0, 0), testSavedView(0, 86, 0),
		testSavedView(0, 0, -9), testSavedView(0, 0, 21),
		testSavedView(math.NaN(), 0, 0), testSavedView(0, 0, math.Inf(1)),
	} {
		if err := (appSettings{LastView: view}).validate(); err == nil {
			t.Fatalf("invalid saved view accepted: %+v", view)
		}
	}
	if err := (appSettings{LastView: testSavedView(0, 0, 0)}).validate(); err != nil {
		t.Fatal("zero is a valid coordinate and zoom", err)
	}
}

func TestSavedViewStartupPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name           string
		flags          []string
		lon, lat, zoom float64
	}{
		{name: "saved", lon: 10, lat: 20, zoom: 8},
		{name: "zoom override", flags: []string{"--zoom=0"}, lon: 10, lat: 20},
		{name: "centre override", flags: []string{"--center-lon=0", "--center-lat=0"}, zoom: 8},
		{name: "reset", flags: []string{"--reset-view"}},
		{name: "reset false", flags: []string{"--reset-view=false"}, lon: 10, lat: 20, zoom: 8},
		{name: "reset and override", flags: []string{"--reset-view", "--zoom=5"}, zoom: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := saveAppSettings(appSettings{Theme: "noir", Traffic: true, LastView: testSavedView(10, 20, 8)}); err != nil {
				t.Fatal(err)
			}
			before := settingsBytes(t)
			m := preferenceViewer(t, tc.flags...)
			if m.camera.Center != project(tc.lon, tc.lat) || m.camera.Zoom != tc.zoom {
				t.Fatal("startup precedence incorrect", m.camera)
			}
			if m.args.ResetView {
				saved, err := loadAppSettings()
				if err != nil || saved.LastView != nil || saved.Theme != "noir" || !saved.Traffic {
					t.Fatal("reset did not remove only the saved view", err)
				}
				before = settingsBytes(t)
			}
			if err := m.saveViewOnExit(); err != nil || !bytes.Equal(before, settingsBytes(t)) {
				t.Fatal("launch without navigation changed settings", err)
			}
		})
	}
	if _, err := parseCLI([]string{"render", "--reset-view"}, io.Discard); err == nil {
		t.Fatal("viewer-only reset flag accepted by static render")
	}
}

func TestResetViewPersistsUntilNewNavigation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveAppSettings(appSettings{LastView: testSavedView(10, 20, 8)}); err != nil {
		t.Fatal(err)
	}
	m := preferenceViewer(t, "--reset-view", "--zoom=5")
	if err := m.saveViewOnExit(); err != nil {
		t.Fatal(err)
	}
	restarted := preferenceViewer(t)
	if restarted.camera.Center != project(0, 0) || restarted.camera.Zoom != 0 {
		t.Fatal("reopening after reset restored the old view or the launch override")
	}
	m.Update(tea.KeyPressMsg{Code: '+'})
	if err := m.saveViewOnExit(); err != nil {
		t.Fatal(err)
	}
	if preferenceViewer(t).camera.Zoom != m.camera.Zoom {
		t.Fatal("reset prevented subsequent navigation from being saved")
	}
}

func TestResetViewFailsWithoutOverwritingSettings(t *testing.T) {
	for _, failure := range []string{"malformed", "incompatible", "busy"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := saveAppSettings(appSettings{LastView: testSavedView(10, 20, 8)}); err != nil {
				t.Fatal(err)
			}
			path, _ := appSettingsPath()
			switch failure {
			case "malformed", "incompatible":
				data := []byte(`{"future_field":true}`)
				if failure == "malformed" {
					data = []byte(`{`)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
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
			before := settingsBytes(t)
			m := &viewer{args: Args{ResetView: true}}
			if err := m.initUI(); err == nil || !strings.Contains(err.Error(), "could not reset saved view") {
				t.Fatal("failed reset was not reported", err)
			}
			if !bytes.Equal(before, settingsBytes(t)) {
				t.Fatal("failed reset overwrote settings")
			}
		})
	}
}

func TestSavedViewSurvivesStartupImportsAndRoutes(t *testing.T) {
	for _, flags := range [][]string{nil, {"--reset-view"}, {"--zoom=0", "--bearing=30"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := saveAppSettings(appSettings{LastView: testSavedView(10, 20, 8)}); err != nil {
				t.Fatal(err)
			}
			m := preferenceViewer(t, flags...)
			m.windowSizeKnown = true
			m.args.Journey, _ = parseStartupJourney("51.3,-2.5", "51.4,-2.6", nil, "cycling")
			m.pendingPaths = []string{"../fixtures/waypoints.gpx"}
			m.directions.token = "test"
			m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 100, 60)), nil
			})
			runLoadCommand(m, m.load("../fixtures/example.gpx"))
			// runLoadCommand delivers imports, but leaves the route result for us.
			wantZoom := 8.0
			if m.args.Zoom != nil {
				wantZoom = *m.args.Zoom
			}
			if !m.args.ResetView && (m.camera.Center != project(10, 20) || m.camera.Zoom != wantZoom) {
				t.Fatal("startup geometry replaced restored camera", m.camera)
			}
			if m.args.ResetView && m.destination().Center == project(10, 20) {
				t.Fatal("reset did not fit startup geometry")
			}
			before := m.destination()
			cmd := m.calculateJourneyRoute()
			m.Update(cmd())
			if !m.args.ResetView && m.destination() != before {
				t.Fatal("late startup route result refitted restored camera")
			}
			if m.viewChanged {
				t.Fatal("startup marked the view as user navigation")
			}
			m.closeDialog()
			m.Update(tea.KeyPressMsg{Code: 'f'})
			if !m.viewChanged || m.destination().Center == project(10, 20) {
				t.Fatal("explicit fit no longer navigates")
			}
		})
	}
}

func TestViewSavedOnlyOnExitAfterNavigation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := preferenceViewer(t, "--zoom=8")
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: '+'})
	path, _ := appSettingsPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("navigation wrote settings before exit", err)
	}
	// Another viewer changes an unrelated field before this viewer exits.
	if _, err := updateAppSettings(func(s *appSettings) { s.Theme = "noir"; s.Traffic = true }); err != nil {
		t.Fatal(err)
	}
	want := m.destination()
	if err := m.saveViewOnExit(); err != nil {
		t.Fatal(err)
	}
	saved, err := loadAppSettings()
	if err != nil || saved.LastView == nil || saved.Theme != "noir" || !saved.Traffic {
		t.Fatal("exit lost view or concurrent settings change", err)
	}
	restarted := preferenceViewer(t)
	if math.Abs(restarted.camera.Center.X-want.Center.X) > 1e-12 ||
		math.Abs(restarted.camera.Center.Y-want.Center.Y) > 1e-12 || restarted.camera.Zoom != want.Zoom {
		t.Fatal("restart did not restore the final view")
	}
	before := settingsBytes(t)
	restarted.Update(tea.KeyPressMsg{Code: 'a'})
	if restarted.viewChanged {
		t.Fatal("presentation toggle counted as camera navigation")
	}
	if err := os.WriteFile(path, []byte(`{"last_view":{"zoom":4}}`), 0600); err != nil {
		t.Fatal(err)
	}
	invalid := settingsBytes(t)
	if err := m.saveViewOnExit(); err == nil || !bytes.Equal(invalid, settingsBytes(t)) {
		t.Fatal("exit overwrote incompatible settings")
	}
	if err := os.WriteFile(path, before, 0600); err != nil {
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
	if err := m.saveViewOnExit(); err == nil || !bytes.Equal(before, settingsBytes(t)) {
		t.Fatal("busy exit save overwrote settings or hid the failure")
	}
}

func TestSavedViewSurvivesDeferredStartupCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveAppSettings(appSettings{LastView: testSavedView(10, 20, 8)}); err != nil {
		t.Fatal(err)
	}
	m := preferenceViewer(t)
	m.windowSizeKnown = true
	m.args.Journey, _ = parseStartupJourney("51.3,-2.5", "51.4,-2.6", nil, "cycling")
	m.startJourneyFromArgs()
	before := m.destination()
	m.acceptCredentials(credentialResult{id: m.authID, credential: renderingCredential{token: "test"}})
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 100, 60)), nil
	})
	cmd := m.calculateJourneyRoute()
	m.Update(cmd())
	if m.destination() != before {
		t.Fatal("credentials arriving after startup moved the restored view")
	}
}
