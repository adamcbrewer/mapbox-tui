package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
)

func lifecycleTile(t *testing.T) []byte {
	t.Helper()
	road := geojson.NewFeature(orb.LineString{{0, 0}, {4096, 4096}})
	data, err := mvt.Marshal(mvt.Layers{&mvt.Layer{Name: "road", Extent: 4096, Version: 2, Features: []*geojson.Feature{road}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProviderRefreshDeadlineHonoursAgeAndCacheExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newProvider("mapbox")
	p.token, p.now = "synthetic-token", func() time.Time { return now }
	data := lifecycleTile(t)
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=5"}, "Age": {"2"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	c := fit(nil, 120, 40, 2)
	first, err := p.load(context.Background(), c)
	if err != nil || !first.RefreshAt.Equal(now.Add(3*time.Second)) || first.Stats.Requests != 1 || first.Stats.Cached != 1 {
		t.Fatal("freshness did not subtract Age", first, err)
	}
	now = now.Add(2 * time.Second)
	warm, err := p.load(context.Background(), c)
	if err != nil || warm.Stats.Requests != 1 || warm.Stats.Hits != 1 || !warm.RefreshAt.Equal(first.RefreshAt) {
		t.Fatal("fresh tile was refetched or its deadline extended", err)
	}
	now = now.Add(time.Second)
	expired, err := p.load(context.Background(), c)
	if err != nil || expired.Stats.Requests != 2 || expired.Stats.Cached != 1 || !expired.RefreshAt.Equal(now.Add(3*time.Second)) {
		t.Fatal("expired tile was reused or failed to acquire new freshness", err)
	}
}

func TestProviderUncacheableResponsesAreNotReusedAndRefreshConservatively(t *testing.T) {
	data := lifecycleTile(t)
	for _, policy := range []string{"no-store,max-age=60", "no-cache,max-age=60", "max-age=0", ""} {
		t.Run(policy, func(t *testing.T) {
			now := time.Unix(1000, 0)
			p := newProvider("mapbox")
			p.token, p.now = "synthetic-token", func() time.Time { return now }
			p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {policy}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
			})
			for requests := uint64(1); requests <= 2; requests++ {
				snapshot, err := p.load(context.Background(), fit(nil, 120, 40, 2))
				if err != nil || snapshot.Stats.Requests != requests || snapshot.Stats.Cached != 0 || len(snapshot.Features) == 0 || !snapshot.RefreshAt.Equal(now.Add(time.Minute)) {
					t.Fatal("uncacheable tile was reused, lost or given an unsafe refresh cadence", err)
				}
			}
		})
	}
}

func TestProviderRateLimitRefreshWaitsThenRecovers(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newProvider("mapbox")
	p.token, p.now = "private-synthetic-token", func() time.Time { return now }
	data := lifecycleTile(t)
	calls := 0
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"120"}}, Body: io.NopCloser(strings.NewReader("private diagnostic"))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	c := fit(nil, 120, 40, 2)
	limited, err := p.load(context.Background(), c)
	if err != nil || !limited.RefreshAt.Equal(now.Add(120*time.Second)) || strings.Contains(limited.Status, "private") || limited.Stats.Errors != 1 {
		t.Fatal("rate limit recovery deadline or diagnostics are wrong", err)
	}
	now = now.Add(119 * time.Second)
	paused, err := p.load(context.Background(), c)
	if err != nil || calls != 1 || !paused.RefreshAt.Equal(limited.RefreshAt) {
		t.Fatal("backoff made an early request or extended the deadline", err)
	}
	now = now.Add(time.Second)
	recovered, err := p.load(context.Background(), c)
	if err != nil || calls != 2 || recovered.Status != "" || len(recovered.Features) == 0 {
		t.Fatal("provider did not recover at Retry-After", err)
	}
}

func TestProviderCancellationDoesNotBlockWaitersOrTriggerBackoff(t *testing.T) {
	p := newProvider("mapbox")
	p.token = "synthetic-token"
	started := make(chan struct{})
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	c := fit(nil, 120, 40, 2)
	firstDone := make(chan error, 1)
	go func() { _, err := p.load(firstCtx, c); firstDone <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial request did not start")
	}
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() { _, err := p.load(waiterCtx, c); waiterDone <- err }()
	cancelWaiter()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("waiting request lost cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter was stuck behind network I/O")
	}
	if stats := p.snapshotStats(); stats.Requests != 1 || stats.Errors != 0 {
		t.Fatal("waiting for the batch lock counted as a request or failure")
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) || !p.retry.IsZero() || p.snapshotStats().Errors != 0 {
		t.Fatal("cancelling an obsolete request poisoned provider backoff", err)
	}
	data := lifecycleTile(t)
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	if snapshot, err := p.load(context.Background(), c); err != nil || len(snapshot.Features) == 0 || snapshot.Stats.Requests != 2 {
		t.Fatal("cancelled request prevented the next view from loading", err)
	}
}

func TestProviderExpiredReplacementDoesNotEvictAnotherFreshTile(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newProvider("mapbox")
	p.token, p.now, p.clock = "synthetic-token", func() time.Time { return now }, 64
	for i := range 63 {
		p.cache[Tile{6, i, 0}] = cacheEntry{Expires: now.Add(time.Hour), Used: uint64(i + 1)}
	}
	p.cache[Tile{}] = cacheEntry{Expires: now.Add(-time.Second), Used: 64}
	data := lifecycleTile(t)
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	snapshot, err := p.load(context.Background(), fit(nil, 120, 40, 2))
	if err != nil || snapshot.Stats.Cached != 64 {
		t.Fatal("refreshing one expired key evicted unrelated fresh data", err)
	}
	for i := range 63 {
		if _, ok := p.cache[Tile{6, i, 0}]; !ok {
			t.Fatal("fresh tile was evicted during replacement", i)
		}
	}
}

func TestProviderBadResponsesBackOffWithoutRetainingPayloads(t *testing.T) {
	for _, body := range [][]byte{{0xff}, bytes.Repeat([]byte{'x'}, 8*1024*1024+1)} {
		now := time.Unix(1000, 0)
		p := newProvider("mapbox")
		p.token, p.now = "synthetic-token", func() time.Time { return now }
		p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
		snapshot, err := p.load(context.Background(), fit(nil, 120, 40, 2))
		if err != nil || snapshot.Status == "" || snapshot.Stats.Errors != 1 || snapshot.Stats.Cached != 0 || !snapshot.RefreshAt.Equal(now.Add(10*time.Second)) {
			t.Fatal("invalid response bypassed failure backoff or entered the cache", err)
		}
	}
}
