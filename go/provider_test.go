package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSyntheticVectorTileDecodeAndWarmCache(t *testing.T) {
	road := geojson.NewFeature(orb.LineString{{0, 0}, {4096, 4096}})
	road.Properties["class"] = "primary"
	place := geojson.NewFeature(orb.Point{2048, 2048})
	place.Properties["name_en"] = "Synthetic place"
	data, e := mvt.Marshal(mvt.Layers{&mvt.Layer{Name: "road", Extent: 4096, Version: 2, Features: []*geojson.Feature{road}}, &mvt.Layer{Name: "place_label", Extent: 4096, Version: 2, Features: []*geojson.Feature{place}}})
	if e != nil {
		t.Fatal(e)
	}
	fs, e := decodeTile(data, Tile{2, 1, 1})
	if e != nil {
		t.Fatal(e)
	}
	if len(fs) != 2 || fs[0].Layer != 3 || fs[0].Parts[0][1] != (XY{0.5, 0.5}) || fs[1].Label != "Synthetic place" {
		t.Fatal("MVT layer/coordinate/property decoding failed")
	}
	p := newProvider("mapbox")
	p.token = "synthetic-test-token"
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": []string{"max-age=60"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	c := fit(track, 120, 40, 2)
	_, cold, status := p.scene(context.Background(), track, c)
	if status != "" || cold.Requests == 0 {
		t.Fatal("cold tile request failed")
	}
	_, warm, status := p.scene(context.Background(), track, c)
	if status != "" || warm.Requests != cold.Requests || warm.Hits == 0 {
		t.Fatal("warm scene refetched tiles")
	}
}
func TestRateLimitBackoffDoesNotLeakToken(t *testing.T) {
	p := newProvider("mapbox")
	p.token = "never-log-this-test-token"
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader("private provider diagnostic"))}, nil
	})
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	c := fit(track, 120, 40, 2)
	_, s, status := p.scene(context.Background(), track, c)
	if s.Requests != 1 || s.Errors != 1 || strings.Contains(status, p.token) {
		t.Fatal("rate limit behavior incorrect")
	}
	_, s, _ = p.scene(context.Background(), track, c)
	if s.Requests != 1 {
		t.Fatal("backoff refetched tiles")
	}
}

func TestTokenFreeGPXRendersOnlyImportedGeometryWithoutWarnings(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	track, err := loadTrack("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	p := newProvider(defaultProvider(accessToken()))
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("token-free GPX viewing made a network request")
		return nil, nil
	})
	features, stats, status := p.scene(context.Background(), track, fit(track, 120, 40, 2))
	if p.Kind != "none" || len(features) != 0 || stats.Requests != 0 || status != "" {
		t.Fatal("token-free GPX acquired a basemap or unnecessary warning")
	}
	result, err := renderResult(context.Background(), track, Args{}, p)
	if err != nil {
		t.Fatal(err)
	}
	text := result["text"].(string)
	if !strings.Contains(text, "S") || !strings.Contains(text, "E") || !strings.Contains(text, "W") {
		t.Fatal("empty-canvas rendering lost the GPX geometry")
	}
	p.Kind = "mapbox"
	result, err = renderResult(context.Background(), track, Args{}, p)
	if err != nil || result["text"] != text || !strings.Contains(result["scene"].(map[string]any)["status"].(string), "MAPBOX_ACCESS_TOKEN") {
		t.Fatal("explicit Mapbox request without credentials lost the GPX or failed to warn", err)
	}
}

func TestMapboxConfigurationIsReadAtRuntimeWithEnvironmentPrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("mapbox.local.json", []byte(`{"access_token":"synthetic-local-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if p := newProvider("mapbox"); p.token != "synthetic-local-token" {
		t.Fatal("runtime local config was not loaded")
	}
	t.Setenv("MAPBOX_ACCESS_TOKEN", "synthetic-env-token")
	if p := newProvider("mapbox"); p.token != "synthetic-env-token" {
		t.Fatal("runtime environment did not override local config")
	}
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	if p := newProvider("mapbox"); p.token != "" || defaultProvider(p.token) != "none" {
		t.Fatal("explicitly empty environment did not disable the local token")
	}
}

func TestMCPBasemapDefaultsToConfiguredTokenAndCanBeDisabled(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "synthetic-runtime-token")
	road := geojson.NewFeature(orb.LineString{{0, 0}, {4096, 4096}})
	data, err := mvt.Marshal(mvt.Layers{&mvt.Layer{Name: "road", Extent: 4096, Version: 2, Features: []*geojson.Feature{road}}})
	if err != nil {
		t.Fatal(err)
	}
	p := newProvider("mapbox")
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	s := &Service{tracks: map[string]*Track{}, providers: map[string]*Provider{"mapbox": p, "none": newProvider("none")}}
	result, err := s.call(context.Background(), "render_map", Args{})
	if err != nil {
		t.Fatal(err)
	}
	scene := result.(map[string]any)["scene"].(map[string]any)
	if scene["provider"] != "mapbox" || scene["tile_stats"].(Stats).Requests == 0 || scene["status"] != "" {
		t.Fatal("MCP did not automatically use the configured token", scene)
	}
	requests := p.stats.Requests
	result, err = s.call(context.Background(), "render_map", Args{Provider: "none"})
	if err != nil || result.(map[string]any)["scene"].(map[string]any)["provider"] != "none" || p.stats.Requests != requests {
		t.Fatal("explicit none failed to disable MCP basemap requests", err)
	}
}
