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
	decodedTileWrong := len(fs) != 2 ||
		fs[0].Role != roleMajorRoad ||
		fs[0].Parts[0][1] != (XY{0.5, 0.5}) ||
		fs[1].Label != "Synthetic place"
	if decodedTileWrong {
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
	cold, err := p.load(context.Background(), c)
	coldRequestFailed := err != nil || cold.Status != "" || cold.Stats.Requests == 0
	if coldRequestFailed {
		t.Fatal("cold tile request failed")
	}
	warm, err := p.load(context.Background(), c)
	warmCacheMissed := err != nil ||
		warm.Status != "" ||
		warm.Stats.Requests != cold.Stats.Requests ||
		warm.Stats.Hits == 0
	if warmCacheMissed {
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
	s, err := p.load(context.Background(), c)
	rateLimitWrong := err != nil ||
		s.Stats.Requests != 1 ||
		s.Stats.Errors != 1 ||
		strings.Contains(s.Status, p.token)
	if rateLimitWrong {
		t.Fatal("rate limit behavior incorrect")
	}
	s, _ = p.load(context.Background(), c)
	if s.Stats.Requests != 1 {
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
	snapshot, err := p.load(context.Background(), fit(
		track,
		120,
		40,
		2,
	))
	unnecessaryBasemap := err != nil ||
		p.Kind != "none" ||
		len(snapshot.Features) != 0 ||
		snapshot.Stats.Requests != 0 ||
		snapshot.Status != ""
	if unnecessaryBasemap {
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
	s := &Service{providers: map[string]*Provider{"mapbox": p, "none": newProvider("none")}}
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
