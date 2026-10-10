//go:build integration

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestTrafficAccountAccess(t *testing.T) {
	if os.Getenv("SHOE_TRAFFIC_CHECK") != "1" {
		t.Skip("set SHOE_TRAFFIC_CHECK=1 to request Traffic v1 metadata and a London tile")
	}
	credential := resolveCredential(t.Context())
	if credential.token == "" {
		t.Fatal("no selected Mapbox token: " + credential.guidance)
	}
	p := newTrafficProvider(credential.token)
	endpoint := "https://api.mapbox.com/v4/" + trafficTileset + ".json?access_token=" + url.QueryEscape(credential.token)
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		endpoint,
		nil,
	)
	if err != nil {
		t.Fatal("cannot create traffic metadata request")
	}
	response, err := p.client.Do(request)
	if err != nil {
		t.Fatal("traffic metadata request failed; check connection")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Traffic v1 metadata HTTP %d; check account access", response.StatusCode)
	}
	var metadata struct {
		MinZoom int `json:"minzoom"`
		MaxZoom int `json:"maxzoom"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&metadata); err != nil {
		t.Fatal("invalid Traffic v1 metadata")
	}
	if metadata.MaxZoom < 14 {
		t.Fatalf("Traffic v1 maximum zoom %d does not support the configured ceiling", metadata.MaxZoom)
	}
	if metadata.MinZoom > 6 {
		t.Fatalf("Traffic v1 minimum zoom %d exceeds the configured floor", metadata.MinZoom)
	}
	c := Camera{Center: project(-.1276, 51.5072), Zoom: 14, Aspect: 2, Width: 1, Height: 1}
	snapshot, err := p.load(t.Context(), c)
	if err != nil || snapshot.Failed {
		t.Fatalf("Traffic v1 tile access failed: %s", snapshot.Status)
	}
	t.Logf(
		"Traffic v1: zooms %d–%d, %d tile requests, %d decoded road features, credential source %s",
		metadata.MinZoom,
		metadata.MaxZoom,
		snapshot.Stats.Requests,
		len(snapshot.Features),
		credential.source,
	)
	if !snapshot.RefreshAt.IsZero() {
		t.Logf("Next refresh in %s", snapshot.RefreshAt.Sub(p.now()).Round(time.Second))
	}
}
