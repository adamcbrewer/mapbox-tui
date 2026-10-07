package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestMCPWorkspaceRenderingDoesNotHoldLifecycleLockDuringNetworkRequests(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	p := newProvider("mapbox")
	p.token = "synthetic-token"
	started := make(chan struct{})
	var once sync.Once
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	s := &Service{providers: map[string]*Provider{"mapbox": p, "none": newProvider("none")}}
	loaded, err := s.call(context.Background(), "load_track", Args{Path: "../fixtures/example.gpx"})
	if err != nil {
		t.Fatal(err)
	}
	id := loaded.(map[string]any)["track_id"].(string)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.call(ctx, "render_map", Args{Workspace: true, Provider: "mapbox"})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("render did not reach the fake provider")
	}
	unloaded := make(chan error, 1)
	go func() {
		_, err := s.call(context.Background(), "unload_track", Args{TrackID: id})
		unloaded <- err
	}()
	select {
	case err := <-unloaded:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("network request blocked workspace removal")
	}
	cancel()
	<-done
	if _, err := s.call(context.Background(), "summarize_track", Args{TrackID: id}); err == nil {
		t.Fatal("unloaded track still exists")
	}
}
