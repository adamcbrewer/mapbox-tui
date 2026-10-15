package main

import (
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
)

func TestFreeRoamMapboxRendersWithoutTrackAndCachesTiles(t *testing.T) {
	road := geojson.NewFeature(orb.LineString{{0, 0}, {4096, 4096}})
	data, err := mvt.Marshal(mvt.Layers{&mvt.Layer{Name: "road", Extent: 4096, Version: 2, Features: []*geojson.Feature{road}}})
	if err != nil {
		t.Fatal(err)
	}
	p := newProvider("mapbox")
	p.token = "synthetic-test-token"
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("access_token") != p.token {
			t.Fatal("tile request omitted the token")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": []string{"max-age=60"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	lon, lat, zoom := -0.1, 51.5, 13.0
	a := Args{Lon: &lon, Lat: &lat, Zoom: &zoom}
	cold, err := renderResult(context.Background(), nil, a, p)
	if err != nil {
		t.Fatal(err)
	}
	scene := cold["scene"].(map[string]any)
	stats := scene["tile_stats"].(Stats)
	if strings.TrimSpace(cold["text"].(string)) == "" || stats.Requests == 0 || stats.Errors != 0 || scene["selected_point_index"] != -1 || scene["status"] != "" {
		t.Fatal("basemap did not render without a track", scene)
	}
	if math.Abs(scene["center_lon"].(float64)-lon) > 1e-9 || math.Abs(scene["center_lat"].(float64)-lat) > 1e-9 || scene["zoom"] != zoom {
		t.Fatal("free roam ignored camera options")
	}
	warm, err := renderResult(context.Background(), nil, a, p)
	if err != nil {
		t.Fatal(err)
	}
	warmStats := warm["scene"].(map[string]any)["tile_stats"].(Stats)
	if warmStats.Requests != stats.Requests || warmStats.Hits == 0 || warm["text"] != cold["text"] {
		t.Fatal("free roam failed to reuse cached tiles")
	}
}

func TestFreeRoamNavigationInspectionAndLayout(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {30, 12}} {
		c, err := viewport(nil, Args{})
		if err != nil {
			t.Fatal(err)
		}
		m := &viewer{
			camera:      c,
			provider:    newProvider("none"),
			ctx:         context.Background(),
			args:        Args{NoAnimation: true},
			showProfile: true,
		}
		m.initUI()
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.Init()
		routeStateRequired := m.activeTrack() != nil ||
			m.activePlayback() != nil ||
			m.profileHeight != 0 ||
			m.ui.dialog != dialogNone
		if routeStateRequired {
			t.Fatal("free roam required a route or opened track UI")
		}
		before := m.camera
		m.Update(tea.KeyPressMsg{Code: 'h'})
		if m.camera.Center == before.Center {
			t.Fatal("free roam keyboard pan did not move the camera")
		}
		m.Update(tea.KeyPressMsg{Code: '+'})
		if m.camera.Zoom <= before.Zoom {
			t.Fatal("free roam keyboard zoom did not zoom")
		}
		m.Update(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
		m.Update(tea.MouseMotionMsg{X: 11, Y: 3, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 11, Y: 3, Button: tea.MouseLeft})
		if m.camera.Center == before.Center || m.dragging {
			t.Fatal("free roam mouse drag failed")
		}
		m.Update(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 10, Y: 3, Button: tea.MouseLeft})
		if m.inspected == nil || m.selectedPoint() != -1 {
			t.Fatal("clicking without a route did not select map coordinates")
		}
		for _, code := range []rune{'[', ']', 'e', ' ', 's', 'S', 'p', tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown} {
			m.Update(tea.KeyPressMsg{Code: code})
		}
		routeControlsChangedState := m.ui.dialog != dialogNone ||
			m.activePlayback() != nil ||
			m.selectedPoint() != -1 ||
			m.showProfile
		if routeControlsChangedState {
			t.Fatal("route-only controls changed free roam state")
		}
		m.rebuild()
		if !strings.Contains(m.content, "Selected position") || strings.Contains(m.content, "Elevation profile") || lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
			t.Fatalf("free roam layout invalid at %v", size)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m.rebuild()
		if !strings.Contains(ansi.Strip(m.content), "Selected position") {
			t.Fatalf("free roam inspection is empty at %v", size)
		}
		m.closeDialog()
		m.Update(tea.KeyPressMsg{Code: 'f'})
		if m.camera.Center != project(0, 0) || m.camera.Zoom != 0 {
			t.Fatal("reset world view did not restore the default camera")
		}
		m.provider.Kind = "mapbox"
		m.Update(tea.KeyPressMsg{Code: 'h'})
		m.Update(pulse(time.Now()))
		if !m.sources.busy() {
			t.Fatal("free roam pan did not request new basemap tiles")
		}
	}
}

func TestFreeRoamRouteTransitionsAndFailedLoad(t *testing.T) {
	document, err := loadDocument("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	lon, lat, zoom := 100.0, -20.0, 5.0
	a := Args{Lon: &lon, Lat: &lat, Zoom: &zoom, NoAnimation: true}
	c, err := viewport(nil, a)
	if err != nil {
		t.Fatal(err)
	}
	m := &viewer{
		camera:      c,
		args:        a,
		provider:    newProvider("none"),
		ctx:         context.Background(),
		width:       120,
		height:      40,
		showProfile: true,
	}
	m.initUI()
	m.Init()
	m.Update(loaded{path: "../fixtures/example.gpx", id: m.loadID, document: document})
	track := m.activeTrack()
	want := fit(
		track,
		m.camera.Width,
		m.camera.Height,
		m.camera.Aspect,
	)
	importNotInitialised := m.activeTrack() != track ||
		m.activePlayback() == nil ||
		m.visibleProfile() == nil ||
		m.following ||
		m.camera.Center != want.Center ||
		m.camera.Zoom != want.Zoom
	if importNotInitialised {
		t.Fatal("opening a route from free roam did not fit and initialise it")
	}
	m.activePlayback().toggle()
	m.runCommand("roam")
	freeRoamLostWorkspace := m.activeTrack() != nil ||
		m.activePlayback() != nil ||
		m.visibleProfile() == nil ||
		m.profileHeight == 0 ||
		m.camera.Center != want.Center ||
		len(m.workspace.documents) != 1
	if freeRoamLostWorkspace {
		t.Fatal("clearing the route did not preserve the map and stop playback")
	}
	for _, command := range m.commands() {
		if command.action == "play" || command.action == "speed" {
			t.Fatal("free roam offered a route-only command")
		}
	}
	m.Update(loaded{id: m.loadID - 1, document: document})
	if m.activeTrack() != nil {
		t.Fatal("an outdated load restored a cleared route")
	}
	m.Update(loaded{id: m.loadID, err: io.EOF})
	failedImportNotRecovered := m.activeTrack() != nil ||
		m.ui.dialog != dialogFiles ||
		!strings.Contains(m.ui.notice, "Could not open track file")
	if failedImportNotRecovered {
		t.Fatal("failed import did not recover to the file picker")
	}
	m.closeDialog()
	m.rebuild()
	if !strings.Contains(m.content, "Free roam") {
		t.Fatal("failed import lost the free roam map")
	}
}

func TestFreeRoamMissingTokenWarnsAndRejectsTrackSelectors(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	for _, kind := range []string{"none", "mapbox"} {
		result, err := renderResult(context.Background(), nil, Args{}, newProvider(kind))
		if err != nil {
			t.Fatal("missing token prevented browsing the empty canvas", err)
		}
		scene := result["scene"].(map[string]any)
		if !strings.Contains(scene["status"].(string), "MAPBOX_ACCESS_TOKEN") || scene["tile_stats"].(Stats).Requests != 0 || strings.TrimSpace(result["text"].(string)) != "" {
			t.Fatal("empty map did not provide setup guidance without inventing features", scene)
		}
	}
	i := 0
	if _, err := renderResult(context.Background(), nil, Args{PointIndex: &i}, newProvider("none")); err == nil {
		t.Fatal("free roam accepted a track point selector")
	}
	if err := openViewer("", Args{Provider: "none", PointIndex: &i}); err == nil {
		t.Fatal("viewer accepted a track point selector without a track")
	}
}

func TestFreeRoamCameraStaysWithinMercatorBounds(t *testing.T) {
	c, err := viewport(nil, Args{})
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []XY{{10000, 10000}, {-10000, -10000}} {
		c.pan(delta.X, delta.Y)
		c.zoomAt(XY{-10000, 10000}, 1)
		if c.Center.X < 0 || c.Center.X > 1 || c.Center.Y < 0 || c.Center.Y > 1 || len(visible(c)) == 0 {
			t.Fatal("free roam moved beyond the map's valid tile coverage")
		}
	}
}

func TestCenterSelectedLocationWithoutGeometry(t *testing.T) {
	for _, animate := range []bool{false, true} {
		name := "instant"
		if animate {
			name = "animated"
		}
		t.Run(name, func(t *testing.T) {
			m := journeyViewer(t)
			m.settings.NoAnimation = !animate
			before := m.camera
			m.Update(tea.KeyPressMsg{Code: 'c'})
			if m.camera != before || m.motion.Active {
				t.Fatal("centering without a selection moved the camera")
			}
			m.Update(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
			m.Update(tea.MouseReleaseMsg{X: 10, Y: 3, Button: tea.MouseLeft})
			if m.inspected == nil {
				t.Fatal("map click did not select a location")
			}
			selected := *m.inspected
			m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			before = m.destination()
			m.Update(tea.KeyPressMsg{Code: 'c'})
			want := before
			want.Center = project(selected.X, selected.Y)
			if m.destination() != want || m.following {
				t.Fatal("centering failed to preserve zoom, bearing and viewport without following")
			}
			if m.inspected == nil || *m.inspected != selected || !m.ui.detailsOpen {
				t.Fatal("centering discarded the selected location or closed its context")
			}
			found := false
			for _, command := range m.commands() {
				found = found || command.action == "center"
			}
			if !found {
				t.Fatal("selected location has no center command")
			}
			m.Update(tea.KeyPressMsg{Code: 'f'})
			want = m.workspace.fitVisible(m.camera.Width, m.camera.Height, m.camera.Aspect)
			if m.destination() != want {
				t.Fatal("fit no longer returns to the world view without geometry")
			}
		})
	}
}
