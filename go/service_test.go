package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestMCPUnavailableCredentialsAreDiscoveredOnlyOnce(t *testing.T) {
	fakeMapbox(t, "loggedout")
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	s := &Service{providers: map[string]*Provider{"mapbox": newProvider("mapbox")}}
	if _, err := s.render(t.Context(), Args{Provider: "none"}); err != nil {
		t.Fatal(err)
	}
	log := os.Getenv("MAPBOX_TUI_TEST_LOG")
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("explicit offline render discovered credentials")
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			result, err := s.render(t.Context(), Args{})
			if err != nil {
				t.Error(err)
				return
			}
			if result.(map[string]any)["scene"].(map[string]any)["provider"] != "none" {
				t.Error("unavailable credentials did not retain offline rendering")
			}
		})
	}
	group.Wait()
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(calls), "\n"); count != 1 {
		t.Fatal("concurrent offline renders repeated account discovery", count)
	}
	for _, provider := range []string{"", "mapbox"} {
		if _, err := s.render(t.Context(), Args{Provider: provider}); err != nil {
			t.Fatal(err)
		}
	}
	later, err := os.ReadFile(log)
	if err != nil || string(later) != string(calls) {
		t.Fatal("later offline renders repeated failed account discovery", err)
	}
}

func TestMCPCancelledDiscoveryCanBeRetried(t *testing.T) {
	fakeMapbox(t, "loggedout")
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	if err := os.Unsetenv("MAPBOX_ACCESS_TOKEN"); err != nil {
		t.Fatal(err)
	}
	s := &Service{providers: map[string]*Provider{"mapbox": newProvider("mapbox")}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.render(ctx, Args{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled discovery did not return cancellation", err)
	}
	if _, err := s.render(t.Context(), Args{}); err != nil {
		t.Fatal("cancelled discovery poisoned subsequent renders", err)
	}
	if _, err := os.Stat(os.Getenv("MAPBOX_TUI_TEST_LOG")); err != nil {
		t.Fatal("credentials were not retried after cancellation", err)
	}
}
