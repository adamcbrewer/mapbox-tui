package main

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
)

func trafficTile(t *testing.T) []byte {
	t.Helper()
	var features []*geojson.Feature
	for i, properties := range []geojson.Properties{
		{"congestion": "moderate", "class": "primary"},
		{"congestion": "severe", "closed": "yes"},
		{"congestion": "unknown"},
	} {
		road := geojson.NewFeature(orb.LineString{{0, float64(i) * 512}, {4096, 4096}})
		road.Properties = properties
		features = append(features, road)
	}
	data, err := mvt.Marshal(mvt.Layers{&mvt.Layer{
		Name: "traffic", Extent: 4096, Version: 2, Features: features,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTrafficTileDecodesCongestionClosuresAndMissingCoverage(t *testing.T) {
	features, err := decodeTile(trafficTile(t), Tile{Z: 2, X: 1, Y: 1})
	if err != nil {
		t.Fatal(err)
	}
	rolesWrong := len(features) != 2 || features[0].Role != roleTrafficModerate || features[1].Role != roleTrafficClosed
	if rolesWrong {
		t.Fatal("traffic decoding lost congestion/closure information or invented unknown traffic", features)
	}
	if features[0].Parts[0][1] != (XY{.5, .5}) {
		t.Fatal("traffic geometry did not use vector tile coordinates")
	}
}

func TestTrafficRadiusClipsCrossingRoadsAtDifferentLatitudesAndBearings(t *testing.T) {
	for _, latitude := range []float64{0, 51.5, 75} {
		for _, bearing := range []float64{0, 90} {
			zoom := 7 + math.Log2(math.Cos(latitude*math.Pi/180))
			c := Camera{Center: project(0, latitude), Zoom: zoom, Bearing: bearing, Aspect: 2, Width: 120, Height: 40}
			line := [][]XY{{project(-10, latitude), project(10, latitude)}}
			features := []Feature{{Parts: line, Role: roleMajorRoad}, {Parts: line, Role: roleTrafficHeavy}}
			g := renderScene(
				c,
				features,
				nil,
				nil,
			)
			var traffic, road, outer int
			bits := [2][4]uint8{{1, 2, 4, 64}, {8, 16, 32, 128}}
			for index, cell := range g.Cells {
				if cell.Role == roleMajorRoad {
					road++
				}
				if cell.Role != roleTrafficHeavy {
					continue
				}
				traffic++
				if g.glyph(cell, true) != '3' {
					t.Fatal("ASCII traffic lost its severity symbol")
				}
				for x := range 2 {
					for y := range 4 {
						if cell.Bits&bits[x][y] == 0 {
							continue
						}
						point := unproject(c.world(XY{float64(index%g.Width*2 + x), float64(index/g.Width*4 + y)}))
						distance := haversine(Point{Lat: latitude}, Point{Lon: point.X, Lat: point.Y})
						if distance > 32186.88+.001 {
							t.Fatalf("traffic exceeded 20 miles at latitude %v bearing %v: %v m", latitude, bearing, distance)
						}
						if distance > 16093.44 {
							outer++
						}
					}
				}
			}
			if traffic == 0 || road == 0 || outer == 0 {
				t.Fatal("circle clipping lost crossing traffic or clipped the basemap", latitude, bearing)
			}
		}
	}
}

func TestTrafficRadiusFollowsCameraWithoutRefetchingUnchangedTiles(t *testing.T) {
	c := Camera{Center: project(0, 0), Zoom: 8, Aspect: 2, Width: 120, Height: 40}
	moved := c
	moved.Center = project(.05, 0)
	p := newTrafficProvider("synthetic-token")
	if !p.sameRequest(c, moved) {
		t.Fatal("small centre movement unnecessarily invalidated traffic tile coverage")
	}
	features := []Feature{{Parts: [][]XY{{project(.3, 0), project(.4, 0)}}, Role: roleTrafficSevere}}
	before := renderScene(
		c,
		features,
		nil,
		nil,
	)
	after := renderScene(
		moved,
		features,
		nil,
		nil,
	)
	if strings.Contains(before.plain(true), "4") || !strings.Contains(after.plain(true), "4") {
		t.Fatal("traffic radius remained attached to the last requested camera")
	}
}

func TestTrafficOverlapKeepsWorstSeverityAndRouteOwnership(t *testing.T) {
	c := Camera{Center: project(0, 0), Zoom: 12, Aspect: 2, Width: 60, Height: 20}
	line := [][]XY{{project(-.01, 0), project(.01, 0)}}
	for _, roles := range [][]mapRole{
		{roleTrafficSevere, roleTrafficLow, roleTrafficModerate},
		{roleTrafficModerate, roleTrafficLow, roleTrafficSevere},
		{roleTrafficClosed, roleTrafficSevere, roleTrafficLow},
	} {
		var features []Feature
		for _, role := range roles {
			features = append(features, Feature{Parts: line, Role: role})
		}
		g := renderScene(
			c,
			features,
			nil,
			nil,
		)
		cell := g.Cells[c.Height/2*g.Width+c.Width/2]
		want := roleTrafficSevere
		if roles[0] == roleTrafficClosed {
			want = roleTrafficClosed
			if g.glyph(cell, false) != 'X' {
				t.Fatal("closure was indistinguishable from congestion in Braille mode")
			}
		}
		if cell.Role != want {
			t.Fatal("traffic overlap depended on feature order", cell.Role, want)
		}
		var workspace Workspace
		ref := openWorkspaceFile(t, &workspace, importFile(t, "route.gpx",
			[]byte(`<gpx><rte><rtept lat="0" lon="-.01"/><rtept lat="0" lon=".01"/></rte></gpx>`)))
		g = renderScene(
			c,
			features,
			workspace.sceneItems(),
			nil,
		)
		cell = g.Cells[c.Height/2*g.Width+c.Width/2]
		if cell.Role != roleActiveRoute || cell.owner != ref {
			t.Fatal("traffic replaced the route colour or picking ownership")
		}
	}
}

func TestTrafficTileCoverageStaysLocalAndBounded(t *testing.T) {
	for _, latitude := range []float64{0, 51.5, 85} {
		for _, zoom := range []float64{0, 8, 14, 20} {
			c := Camera{Center: project(0, latitude), Zoom: zoom, Aspect: 2, Width: 160, Height: 50}
			tiles := trafficTiles(c)
			if len(tiles) == 0 || len(tiles) > 64 {
				t.Fatal("traffic coverage was empty or exceeded the request budget", latitude, zoom, len(tiles))
			}
			for _, tile := range tiles {
				if tile.Z < 6 || tile.Z > 14 {
					t.Fatal("traffic requested unusably coarse or unsupported tile zoom", tile)
				}
				if zoom == 0 && (math.Abs(float64(tile.X)-32) > 1 || math.Abs(float64(tile.Y)-c.Center.Y*64) > 1) {
					t.Fatal("world view requested traffic outside its local focus", tile)
				}
			}
		}
	}
}

func TestTrafficStationaryRefreshCapsCacheAndRetainsStaleDataOnFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newTrafficProvider("synthetic-token")
	p.now = func() time.Time { return now }
	data := trafficTile(t)
	status := http.StatusOK
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, trafficTileset) {
			t.Fatal("traffic requested the Streets tileset")
		}
		return &http.Response{StatusCode: status, Header: http.Header{
			"Cache-Control": {"max-age=43200"}, "Age": {"60"},
		}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	c := Camera{Center: project(-.1276, 51.5072), Zoom: 14, Aspect: 2, Width: 1, Height: 1}
	var loader sourceLoader
	loader.set("traffic", p, true)
	t.Cleanup(loader.close)
	load := func() {
		t.Helper()
		request := oneSourceRequest(t, loader.poll(t.Context(), c, now, false))
		if result := request.run(); result.err != nil || !loader.accept(result, c, now) {
			t.Fatal("traffic snapshot was not accepted", result.err)
		}
	}
	load()
	initial := loader.view()
	if initial.Sources[0].RefreshAt == nil || !initial.Sources[0].RefreshAt.Equal(now.Add(5*time.Minute)) {
		t.Fatal("traffic retained the twelve-hour basemap TTL or ignored Age", initial.Sources)
	}
	if initial.Sources[0].FetchedAt == nil || !initial.Sources[0].FetchedAt.Equal(now) {
		t.Fatal("traffic lost its fetch timestamp")
	}
	if _, err := p.load(t.Context(), c); err != nil || p.snapshotStats().Requests != initial.Stats.Requests {
		t.Fatal("fresh traffic unnecessarily refetched tiles", err)
	}
	now = now.Add(5 * time.Minute)
	status = http.StatusServiceUnavailable
	load()
	failed := loader.view()
	if !reflect.DeepEqual(failed.Features, initial.Features) || !failed.Sources[0].Stale {
		t.Fatal("failed traffic refresh discarded usable geometry or hid staleness")
	}
	if !failed.Sources[0].FetchedAt.Equal(*initial.Sources[0].FetchedAt) {
		t.Fatal("retained traffic acquired a false fresh timestamp")
	}
	now = now.Add(10 * time.Second)
	status = http.StatusOK
	load()
	if loader.view().Sources[0].Stale {
		t.Fatal("traffic did not recover after the retry deadline")
	}
}

func TestTrafficUncacheableResponsesAndRateLimitBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newTrafficProvider("synthetic-token")
	p.now = func() time.Time { return now }
	data := trafficTile(t)
	status := http.StatusOK
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{
			"Cache-Control": {"no-store"}, "Retry-After": {"120"},
		}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	c := Camera{Center: project(-.1276, 51.5072), Zoom: 14, Aspect: 2, Width: 1, Height: 1}
	snapshot, err := p.load(t.Context(), c)
	uncacheablePolicyWrong := snapshot.Stats.Cached != 0 || !snapshot.RefreshAt.Equal(now.Add(5*time.Minute))
	if err != nil || uncacheablePolicyWrong {
		t.Fatal("uncacheable traffic was retained or used the basemap refresh fallback", err)
	}
	status = http.StatusTooManyRequests
	snapshot, err = p.load(t.Context(), c)
	rateLimitPolicyWrong := snapshot.Unavailable || !snapshot.RefreshAt.Equal(now.Add(2*time.Minute))
	if err != nil || rateLimitPolicyWrong {
		t.Fatal("rate limited traffic ignored Retry-After or required a credential reset", err)
	}
	requests := snapshot.Stats.Requests
	p.load(t.Context(), c)
	if p.snapshotStats().Requests != requests {
		t.Fatal("traffic retried within provider backoff")
	}
}

func TestTrafficColoursDefaultForExistingCustomPalettes(t *testing.T) {
	data, err := themeFiles.ReadFile("themes/catppuccin.toml")
	if err != nil {
		t.Fatal(err)
	}
	var legacy strings.Builder
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "traffic_") {
			legacy.WriteString(line + "\n")
		}
	}
	palette, err := decodeTheme(strings.NewReader(legacy.String()))
	if err != nil || palette.mapColor(roleTrafficSevere) == "" {
		t.Fatal("adding traffic broke an existing custom palette", err)
	}
	if _, err := decodeTheme(strings.NewReader(legacy.String() + "traffic_severe = \"bad colour\"\n")); err == nil {
		t.Fatal("explicit invalid traffic colour was silently replaced")
	}
}

func TestTrafficAccessDenialStopsRetriesUntilExplicitRecheck(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		now := time.Unix(1000, 0)
		p := newTrafficProvider("never-log-this-token")
		p.now = func() time.Time { return now }
		p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("private account details"))}, nil
		})
		c := Camera{Center: project(-.1, 51.5), Zoom: 12, Aspect: 2, Width: 60, Height: 20}
		var loader sourceLoader
		loader.set("traffic", p, true)
		t.Cleanup(loader.close)
		request := oneSourceRequest(t, loader.poll(t.Context(), c, now, false))
		result := request.run()
		if !loader.accept(result, c, now) || !result.snapshot.Unavailable {
			t.Fatal("traffic access denial was not accepted")
		}
		if strings.Contains(result.snapshot.Status, p.token) || strings.Contains(result.snapshot.Status, "private") {
			t.Fatal("traffic access feedback exposed token/account details")
		}
		now = now.Add(time.Hour)
		if len(loader.poll(t.Context(), c, now, false)) != 0 {
			t.Fatal("stationary traffic retried an account denial automatically")
		}
		c.Center = project(1, 52)
		loader.poll(t.Context(), c, now, false)
		request = oneSourceRequest(t, loader.poll(t.Context(), c, now.Add(100*time.Millisecond), false))
		request.run()
		if p.snapshotStats().Requests != 1 {
			t.Fatal("panning repeatedly requested denied traffic access")
		}
	}
}

func TestTrafficMainMenuPersistsCancelsAndResumesWithCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := authTestViewer(t)
	m.args.Provider, m.args.ProviderSet = "none", true
	if m.preferences.Traffic {
		t.Fatal("traffic was not off by default")
	}
	m.openDialog(dialogCommands)
	m.ui.search.SetValue("traffic")
	if commands := m.commands(); len(commands) != 1 || !commands[0].disabled {
		t.Fatal("unverified traffic was missing or enabled in the main menu")
	}
	if cmd := m.runCommand("traffic"); cmd != nil || m.preferences.Traffic || m.ui.dialog != dialogCommands {
		t.Fatal("disabled traffic made a request, changed the setting or closed the menu")
	}
	path, err := appSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disabled traffic saved a preference")
	}
	m.provider.token = "pk.unchecked"
	if !m.trafficCommand().disabled {
		t.Fatal("a token alone certified traffic access")
	}
	m.acceptCredentials(credentialResult{checked: true, access: authSection{State: "ready"},
		credential: renderingCredential{token: "pk.unchecked", source: "environment"}})
	if m.trafficCommand().disabled || m.ui.notice != "" {
		t.Fatal("successful credential check did not enable traffic or clear feedback")
	}
	m.runCommand("traffic")
	settings, err := loadAppSettings()
	settingWrong := !settings.Traffic || m.ui.dialog != dialogNone
	if err != nil || settingWrong {
		t.Fatal("main-menu traffic toggle was not persistent or stayed open", err)
	}
	m.provider.token = "pk.old"
	m.configureSources()
	old := oneSourceRequest(t, m.sources.poll(m.ctx, m.camera, time.Now(), false))
	m.acceptCredentials(credentialResult{credential: renderingCredential{token: "pk.new", source: "login"}})
	if m.traffic.token != "pk.new" || !m.trafficEnabled {
		t.Fatal("traffic did not resume with background credential discovery")
	}
	if old.ctx.Err() == nil {
		t.Fatal("credential replacement left obsolete traffic requests running")
	}
	m.openDialog(dialogCommands)
	m.runCommand("traffic")
	if m.preferences.Traffic || m.sources.accept(sourceResult{
		name: old.name, generation: old.generation, camera: old.camera,
		snapshot: sourceSnapshot{Features: []Feature{{Role: roleTrafficSevere}}},
	}, m.camera, time.Now()) {
		t.Fatal("turning traffic off retained it or accepted a late result")
	}
	if settings, err := loadAppSettings(); err != nil || settings.Traffic {
		t.Fatal("traffic off was not saved", err)
	}
}

func TestTrafficMainMenuFitsCompactTerminals(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		m := authTestViewer(t)
		m.width, m.height = size[0], size[1]
		m.layout()
		m.openDialog(dialogCommands)
		m.ui.search.SetValue("traffic")
		for _, ascii := range []bool{false, true} {
			m.settings.ASCII = ascii
			m.rebuild()
			if !strings.Contains(ansi.Strip(m.content), "Toggle traffic") {
				t.Fatal("main-menu traffic toggle was not visible at compact size", size, ascii)
			}
		}
	}
}

func TestTrafficSpaceTogglesWithoutClosingMenu(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := authTestViewer(t)
	m.args.Provider, m.args.ProviderSet = "none", true
	m.provider.token, m.authVerified = "pk.checked", true
	m.openDialog(dialogCommands)
	m.ui.search.SetValue("traffic")
	for _, enabled := range []bool{true, false} {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
		if m.preferences.Traffic != enabled || m.ui.dialog != dialogCommands || m.ui.search.Value() != "traffic" {
			t.Fatal("Space failed to toggle traffic in place")
		}
	}
	m.authVerified = false
	m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	if m.preferences.Traffic || m.ui.notice != trafficAuthGuidance {
		t.Fatal("Space bypassed disabled traffic verification")
	}
}

func TestTrafficVerificationGatesSavedPreferenceAndAllowsTurningOff(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := authTestViewer(t)
	m.args.Provider, m.args.ProviderSet = "none", true
	m.preferences.Traffic = true
	m.provider.token = "pk.unchecked"
	m.configureSources()
	if m.trafficEnabled || m.traffic != nil {
		t.Fatal("a saved traffic preference bypassed verification")
	}
	m.acceptCredentials(credentialResult{checked: true, access: authSection{State: "ready"},
		credential: renderingCredential{token: "pk.unchecked", source: "environment"}})
	if !m.trafficEnabled || !m.mapboxVerified() {
		t.Fatal("verification did not resume a saved traffic preference")
	}
	m.acceptCredentials(credentialResult{checked: true, access: authSection{State: "unavailable"},
		credential: renderingCredential{token: "pk.unchecked", source: "environment"}})
	if m.trafficEnabled || m.mapboxVerified() || m.trafficCommand().disabled {
		t.Fatal("failed verification left traffic active or disabled turning it off")
	}
	m.openDialog(dialogCommands)
	m.runCommand("traffic")
	if m.preferences.Traffic || !m.trafficCommand().disabled {
		t.Fatal("uncertified traffic could not be turned off or enabled again")
	}
	for _, action := range m.mapboxActions() {
		if action.action == "traffic" {
			t.Fatal("traffic was still buried in Mapbox settings")
		}
	}
}

func TestTrafficUnlocksAfterBasemapSuccessAndRejectsObsoleteProof(t *testing.T) {
	m := authTestViewer(t)
	m.provider = providerForCredential("mapbox", renderingCredential{token: "pk.first"})
	status := http.StatusOK
	m.provider.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Cache-Control": {"max-age=60"}},
			Body: io.NopCloser(bytes.NewReader(lifecycleTile(t)))}, nil
	})
	result := captureSourceResult(t, m.fetch())
	if !m.trafficCommand().disabled {
		t.Fatal("unchecked basemap token unlocked traffic")
	}
	m.Update(result)
	if m.trafficCommand().disabled {
		t.Fatal("successful basemap access did not certify the connection")
	}
	m.provider.mu.Lock()
	m.provider.cache = map[Tile]cacheEntry{}
	m.provider.mu.Unlock()
	m.sources.states[0].snapshot.RefreshAt = time.Now().Add(-time.Second)
	status = http.StatusForbidden
	m.Update(captureSourceResult(t, m.fetch()))
	if !m.trafficCommand().disabled {
		t.Fatal("denied Streets access retained old traffic certification")
	}
	m.acceptCredentials(credentialResult{credential: renderingCredential{token: "pk.second", source: "environment"}})
	m.Update(result)
	if !m.trafficCommand().disabled {
		t.Fatal("replacement token reused obsolete credential/basemap certification")
	}
}

func TestTrafficCacheOnlyBasemapDoesNotRestoreFailedVerification(t *testing.T) {
	m := authTestViewer(t)
	m.provider = providerForCredential("mapbox", renderingCredential{token: "pk.checked"})
	m.provider.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Cache-Control": {"max-age=3600"}},
			Body: io.NopCloser(bytes.NewReader(lifecycleTile(t)))}, nil
	})
	m.Update(captureSourceResult(t, m.fetch()))
	if !m.mapboxVerified() {
		t.Fatal("initial network load did not verify access")
	}
	m.acceptCredentials(credentialResult{checked: true, access: authSection{State: "unavailable"},
		credential: renderingCredential{token: "pk.checked", source: "environment"}})
	requests := m.provider.snapshotStats().Requests
	m.sources.states[0].snapshot.RefreshAt = time.Now().Add(-time.Second)
	m.Update(captureSourceResult(t, m.fetch()))
	if m.provider.snapshotStats().Requests != requests || m.mapboxVerified() {
		t.Fatal("cached geometry replaced a failed connection check with old proof")
	}
}

func TestTrafficCacheCapUsesRemainingResponseFreshness(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newTrafficProvider("synthetic-token")
	p.now = func() time.Time { return now }
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
			"Cache-Control": {"max-age=600"}, "Age": {"360"},
		}, Body: io.NopCloser(bytes.NewReader(trafficTile(t)))}, nil
	})
	camera := Camera{Center: project(-.1276, 51.5072), Zoom: 14, Aspect: 2, Width: 1, Height: 1}
	snapshot, err := p.load(t.Context(), camera)
	if err != nil || snapshot.Stats.Cached == 0 || !snapshot.RefreshAt.Equal(now.Add(4*time.Minute)) {
		t.Fatal("valid response freshness was discarded before applying the traffic cap", err)
	}
	requests := snapshot.Stats.Requests
	now = now.Add(time.Minute)
	p.load(t.Context(), camera)
	if p.snapshotStats().Requests != requests {
		t.Fatal("overlapping fresh traffic tiles were downloaded again")
	}
}

func TestTrafficFreshResponseBeyondBasemapCapIsCached(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newTrafficProvider("synthetic-token")
	p.now = func() time.Time { return now }
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
			"Cache-Control": {"max-age=86400"}, "Age": {"50000"},
		}, Body: io.NopCloser(bytes.NewReader(trafficTile(t)))}, nil
	})
	camera := Camera{Center: project(-.1276, 51.5072), Zoom: 14, Aspect: 2, Width: 1, Height: 1}
	snapshot, err := p.load(t.Context(), camera)
	if err != nil || snapshot.Stats.Cached == 0 || !snapshot.RefreshAt.Equal(now.Add(5*time.Minute)) {
		t.Fatal("fresh response became uncacheable because the basemap cap preceded Age", err)
	}
}

func TestTrafficAccessDeniedRefreshKeepsCompletePreviousSnapshot(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			trafficFailedBatchKeepsPreviousSnapshot(t, status)
		})
	}
}

func trafficFailedBatchKeepsPreviousSnapshot(t *testing.T, failureStatus int) {
	t.Helper()
	now := time.Unix(1000, 0)
	p := newTrafficProvider("synthetic-token")
	p.now = func() time.Time { return now }
	camera := Camera{Center: project(-.1276, 51.5072), Zoom: 8, Aspect: 2, Width: 88, Height: 34}
	if len(trafficTiles(camera)) < 2 {
		t.Fatal("test requires a multi-tile traffic view")
	}
	var calls int
	failing := false
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		calls++
		status := http.StatusOK
		if failing && calls == 2 {
			status = failureStatus
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Cache-Control": {"max-age=60"}},
			Body: io.NopCloser(bytes.NewReader(trafficTile(t)))}, nil
	})
	var loader sourceLoader
	loader.set("traffic", p, true)
	t.Cleanup(loader.close)
	initial := oneSourceRequest(t, loader.poll(t.Context(), camera, now, false)).run()
	if !loader.accept(initial, camera, now) {
		t.Fatal("initial traffic was not accepted")
	}
	previous := loader.view()
	now = now.Add(time.Minute)
	calls, failing = 0, true
	failed := oneSourceRequest(t, loader.poll(t.Context(), camera, now, false)).run()
	if !loader.accept(failed, camera, now) || !failed.snapshot.Failed {
		t.Fatal("failed refresh was not accepted")
	}
	current := loader.view()
	if failureStatus == http.StatusServiceUnavailable && current.Sources[0].RefreshAt == nil {
		t.Fatal("transient failure did not schedule a retry")
	}
	if failureStatus == http.StatusForbidden && current.Sources[0].RefreshAt != nil {
		t.Fatal("access denial scheduled an automatic retry")
	}
	if !reflect.DeepEqual(current.Features, previous.Features) || !current.Sources[0].Stale {
		t.Fatal("access denial replaced complete traffic with a partial tile batch")
	}
	if !current.Sources[0].FetchedAt.Equal(*previous.Sources[0].FetchedAt) {
		t.Fatal("retained traffic received a fresh timestamp")
	}
}

func TestTrafficEmptyCompleteSnapshotRetainsFreshnessOnFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newTrafficProvider("synthetic-token")
	p.now = func() time.Time { return now }
	unknown := geojson.NewFeature(orb.LineString{{0, 0}, {4096, 4096}})
	unknown.Properties["congestion"] = "unknown"
	data, err := mvt.Marshal(mvt.Layers{&mvt.Layer{
		Name: "traffic", Extent: 4096, Version: 2, Features: []*geojson.Feature{unknown},
	}})
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusOK
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Cache-Control": {"max-age=60"}},
			Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	camera := Camera{Center: project(-.1276, 51.5072), Zoom: 14, Aspect: 2, Width: 1, Height: 1}
	var loader sourceLoader
	loader.set("traffic", p, true)
	t.Cleanup(loader.close)
	first := oneSourceRequest(t, loader.poll(t.Context(), camera, now, false)).run()
	if !loader.accept(first, camera, now) || len(loader.view().Features) != 0 {
		t.Fatal("successful unknown traffic did not produce a complete empty snapshot")
	}
	fetched := *loader.view().Sources[0].FetchedAt
	now = now.Add(time.Minute)
	status = http.StatusServiceUnavailable
	failed := oneSourceRequest(t, loader.poll(t.Context(), camera, now, false)).run()
	if !loader.accept(failed, camera, now) {
		t.Fatal("failed empty-view refresh was not accepted")
	}
	info := loader.view().Sources[0]
	if info.FetchedAt == nil || !info.FetchedAt.Equal(fetched) || !info.Stale {
		t.Fatal("failed refresh erased a complete empty view's freshness metadata")
	}
}
