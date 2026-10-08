package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
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
	data, err := mvt.Marshal(mvt.Layers{&mvt.Layer{
		Name:     "road",
		Extent:   4096,
		Version:  2,
		Features: []*geojson.Feature{road},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProviderMovingSourceCompletesWithinTileCoverage(t *testing.T) {
	base := time.Unix(1000, 0)
	now := base
	camera := Camera{Center: project(-.1, 51.5), Zoom: 12, Aspect: 2, Width: 88, Height: 26}
	coverage := visible(camera)
	p := newProvider("mapbox")
	p.token, p.now = "synthetic-token", func() time.Time { return now }
	data := lifecycleTile(t)
	started, release := make(chan struct{}, 1), make(chan struct{})
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Cache-Control": {"max-age=5"}},
				Body:       io.NopCloser(bytes.NewReader(data)),
			}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	var loader sourceLoader
	t.Cleanup(loader.close)
	loader.set("basemap", p, true)
	first := oneSourceRequest(t, loader.poll(
		context.Background(),
		camera,
		base,
		true,
	))
	done := make(chan sourceResult, 1)
	go func() { done <- first.run() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tile request did not start")
	}
	for elapsed := 50 * time.Millisecond; elapsed <= 2*time.Second; elapsed += 50 * time.Millisecond {
		camera.pan(.1, 0)
		if !slices.Equal(coverage, visible(camera)) {
			t.Fatal("moving test camera left the original tile coverage")
		}
		if jobs := loader.poll(
			context.Background(),
			camera,
			base.Add(elapsed),
			true,
		); len(jobs) != 0 {
			t.Fatalf("same-coverage movement scheduled another batch at %v", elapsed)
		}
		if elapsed < 150*time.Millisecond && first.ctx.Err() != nil {
			t.Fatal("same-coverage movement cancelled an unfinished tile batch")
		}
		if elapsed == 150*time.Millisecond {
			close(release)
			select {
			case result := <-done:
				if result.err != nil || !loader.accept(result, camera, base.Add(elapsed)) {
					t.Fatal("moving camera rejected a completed, still-relevant tile batch", result.err)
				}
			case <-time.After(time.Second):
				t.Fatal("released tile batch did not complete")
			}
		}
	}
	view := loader.view()
	completedSnapshotLost := len(view.Features) == 0 ||
		view.Stats.Requests != uint64(len(coverage)) ||
		view.Stats.Errors != 0 ||
		loader.busy()
	if completedSnapshotLost {
		t.Fatal("moving camera failed to retain its completed tile snapshot", view.Stats)
	}
	now = base.Add(5 * time.Second)
	camera.pan(.1, 0)
	refresh := oneSourceRequest(t, loader.poll(
		context.Background(),
		camera,
		now,
		true,
	))
	if !loader.accept(refresh.run(), camera, now) || loader.view().Stats.Requests != uint64(2*len(coverage)) {
		t.Fatal("same-coverage movement suppressed refresh of expired tiles")
	}
}

func TestProviderSourceCoverageChangesCancelNetworkWork(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newProvider("mapbox")
	p.token, p.now = "synthetic-token", func() time.Time { return now }
	started := make(chan struct{})
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	a := Camera{Center: project(-.1, 51.5), Zoom: 12, Aspect: 2, Width: 88, Height: 26}
	b := a
	b.pan(256, 0)
	if slices.Equal(visible(a), visible(b)) {
		t.Fatal("test cameras have identical tile coverage")
	}
	var loader sourceLoader
	t.Cleanup(loader.close)
	loader.set("basemap", p, true)
	old := oneSourceRequest(t, loader.poll(
		context.Background(),
		a,
		now,
		false,
	))
	done := make(chan sourceResult, 1)
	go func() { done <- old.run() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tile request did not start")
	}
	jobs := loader.poll(
		context.Background(),
		b,
		now.Add(50*time.Millisecond),
		false,
	)
	obsoleteCoverageRetained := len(jobs) != 0 || old.ctx.Err() == nil || loader.busy()
	if obsoleteCoverageRetained {
		t.Fatal("obsolete coverage was not cancelled and debounced")
	}
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || loader.accept(result, b, now.Add(50*time.Millisecond)) {
			t.Fatal("cancelled batch completed successfully or was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("coverage change did not release cancelled network work")
	}
	if p.snapshotStats().Errors != 0 || !p.retry.IsZero() {
		t.Fatal("coverage cancellation triggered provider backoff")
	}
	coverage := visible(b)
	b.pan(.1, 0)
	if !slices.Equal(coverage, visible(b)) {
		t.Fatal("small test movement changed coverage")
	}
	if jobs := loader.poll(
		context.Background(),
		b,
		now.Add(100*time.Millisecond),
		false,
	); len(jobs) != 0 {
		t.Fatal("replacement coverage bypassed debounce")
	}
	current := oneSourceRequest(t, loader.poll(
		context.Background(),
		b,
		now.Add(150*time.Millisecond),
		false,
	))
	data := lifecycleTile(t)
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Cache-Control": {"max-age=60"}},
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})
	if !loader.accept(current.run(), b, now.Add(150*time.Millisecond)) || len(loader.view().Features) == 0 {
		t.Fatal("movement within replacement coverage restarted debounce or prevented loading")
	}
}

func TestProviderSourceRejectsQueuedResultsAcrossCoverageRoundTrip(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newProvider("mapbox")
	p.token, p.now = "synthetic-token", func() time.Time { return now }
	data := lifecycleTile(t)
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Cache-Control": {"max-age=60"}},
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})
	a := Camera{Center: project(-.1, 51.5), Zoom: 12, Aspect: 2, Width: 88, Height: 26}
	b := a
	b.pan(256, 0)
	var loader sourceLoader
	t.Cleanup(loader.close)
	loader.set("basemap", p, true)
	old := oneSourceRequest(t, loader.poll(
		context.Background(),
		a,
		now,
		false,
	))
	result := old.run()
	if result.err != nil {
		t.Fatal("initial tile batch failed", result.err)
	}
	queuedResultRetained := loader.accept(result, b, now.Add(10*time.Millisecond)) ||
		old.ctx.Err() == nil ||
		len(loader.view().Features) != 0
	if queuedResultRetained {
		t.Fatal("queued successful result survived a coverage change before polling")
	}
	if jobs := loader.poll(
		context.Background(),
		a,
		now.Add(20*time.Millisecond),
		false,
	); len(jobs) != 0 {
		t.Fatal("returning to original coverage bypassed debounce")
	}
	current := oneSourceRequest(t, loader.poll(
		context.Background(),
		a,
		now.Add(120*time.Millisecond),
		false,
	))
	if loader.accept(result, a, now.Add(120*time.Millisecond)) || !loader.busy() {
		t.Fatal("matching coverage revived an obsolete generation")
	}
	if !loader.accept(current.run(), a, now.Add(120*time.Millisecond)) || len(loader.view().Features) == 0 {
		t.Fatal("replacement generation failed to load after a coverage round trip")
	}
}

func TestProviderRefreshDeadlineHonoursAgeAndCacheExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newProvider("mapbox")
	p.token, p.now = "synthetic-token", func() time.Time { return now }
	data := lifecycleTile(t)
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Cache-Control": {"max-age=5"}, "Age": {"2"}},
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})
	c := fit(
		nil,
		120,
		40,
		2,
	)
	first, err := p.load(context.Background(), c)
	freshnessWrong := err != nil ||
		!first.RefreshAt.Equal(now.Add(3*time.Second)) ||
		first.Stats.Requests != 1 ||
		first.Stats.Cached != 1
	if freshnessWrong {
		t.Fatal("freshness did not subtract Age", first, err)
	}
	now = now.Add(2 * time.Second)
	warm, err := p.load(context.Background(), c)
	warmCacheChanged := err != nil ||
		warm.Stats.Requests != 1 ||
		warm.Stats.Hits != 1 ||
		!warm.RefreshAt.Equal(first.RefreshAt)
	if warmCacheChanged {
		t.Fatal("fresh tile was refetched or its deadline extended", err)
	}
	now = now.Add(time.Second)
	expired, err := p.load(context.Background(), c)
	expiredTileNotRefreshed := err != nil ||
		expired.Stats.Requests != 2 ||
		expired.Stats.Cached != 1 ||
		!expired.RefreshAt.Equal(now.Add(3*time.Second))
	if expiredTileNotRefreshed {
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
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"Cache-Control": {policy}},
					Body:       io.NopCloser(bytes.NewReader(data)),
				}, nil
			})
			for requests := uint64(1); requests <= 2; requests++ {
				snapshot, err := p.load(context.Background(), fit(
					nil,
					120,
					40,
					2,
				))
				uncacheableResponseWrong := err != nil ||
					snapshot.Stats.Requests != requests ||
					snapshot.Stats.Cached != 0 ||
					len(snapshot.Features) == 0 ||
					!snapshot.RefreshAt.Equal(now.Add(time.Minute))
				if uncacheableResponseWrong {
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
	var calls int
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{
				StatusCode: 429,
				Header:     http.Header{"Retry-After": {"120"}},
				Body:       io.NopCloser(strings.NewReader("private diagnostic")),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Cache-Control": {"max-age=60"}},
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})
	c := fit(
		nil,
		120,
		40,
		2,
	)
	limited, err := p.load(context.Background(), c)
	rateLimitWrong := err != nil ||
		!limited.RefreshAt.Equal(now.Add(120*time.Second)) ||
		strings.Contains(limited.Status, "private") ||
		limited.Stats.Errors != 1
	if rateLimitWrong {
		t.Fatal("rate limit recovery deadline or diagnostics are wrong", err)
	}
	now = now.Add(119 * time.Second)
	paused, err := p.load(context.Background(), c)
	backoffChanged := err != nil || calls != 1 || !paused.RefreshAt.Equal(limited.RefreshAt)
	if backoffChanged {
		t.Fatal("backoff made an early request or extended the deadline", err)
	}
	now = now.Add(time.Second)
	recovered, err := p.load(context.Background(), c)
	recoveryFailed := err != nil || calls != 2 || recovered.Status != "" || len(recovered.Features) == 0
	if recoveryFailed {
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
	c := fit(
		nil,
		120,
		40,
		2,
	)
	firstDone := make(chan error, 1)
	go func() {
		_, err := p.load(firstCtx, c)
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial request did not start")
	}
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := p.load(waiterCtx, c)
		waiterDone <- err
	}()
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
	err := <-firstDone
	cancellationTriggeredBackoff := !errors.Is(err, context.Canceled) ||
		!p.retry.IsZero() ||
		p.snapshotStats().Errors != 0
	if cancellationTriggeredBackoff {
		t.Fatal("cancelling an obsolete request poisoned provider backoff", err)
	}
	data := lifecycleTile(t)
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	snapshot, err := p.load(context.Background(), c)
	nextViewFailed := err != nil || len(snapshot.Features) == 0 || snapshot.Stats.Requests != 2
	if nextViewFailed {
		t.Fatal("cancelled request prevented the next view from loading", err)
	}
}

func TestProviderExpiredReplacementDoesNotEvictAnotherFreshTile(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newProvider("mapbox")
	p.token, p.now, p.clock = "synthetic-token", func() time.Time { return now }, 64
	for i := range 63 {
		p.cache[Tile{Z: 6, X: i, Y: 0}] = cacheEntry{Expires: now.Add(time.Hour), Used: uint64(i + 1)}
	}
	p.cache[Tile{}] = cacheEntry{Expires: now.Add(-time.Second), Used: 64}
	data := lifecycleTile(t)
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Cache-Control": {"max-age=60"}},
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})
	snapshot, err := p.load(context.Background(), fit(
		nil,
		120,
		40,
		2,
	))
	if err != nil || snapshot.Stats.Cached != 64 {
		t.Fatal("refreshing one expired key evicted unrelated fresh data", err)
	}
	for i := range 63 {
		if _, ok := p.cache[Tile{Z: 6, X: i, Y: 0}]; !ok {
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
		snapshot, err := p.load(context.Background(), fit(
			nil,
			120,
			40,
			2,
		))
		badResponseRetained := err != nil ||
			snapshot.Status == "" ||
			snapshot.Stats.Errors != 1 ||
			snapshot.Stats.Cached != 0 ||
			!snapshot.RefreshAt.Equal(now.Add(10*time.Second))
		if badResponseRetained {
			t.Fatal("invalid response bypassed failure backoff or entered the cache", err)
		}
	}
}
