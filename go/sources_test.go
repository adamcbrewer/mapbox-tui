package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type sourceFunc func(context.Context, Camera) (sourceSnapshot, error)

func (f sourceFunc) load(ctx context.Context, c Camera) (sourceSnapshot, error) { return f(ctx, c) }

func oneSourceRequest(t *testing.T, requests []sourceRequest) sourceRequest {
	t.Helper()
	if len(requests) != 1 {
		t.Fatalf("wanted one source request, got %d", len(requests))
	}
	return requests[0]
}

func TestSourcesRejectOldCameraResultsBeforeTheNextPoll(t *testing.T) {
	now := time.Unix(1000, 0)
	a := fit(nil, 120, 40, 2)
	b := a
	b.pan(20, 0)
	var l sourceLoader
	t.Cleanup(l.close)
	l.set("basemap", sourceFunc(func(_ context.Context, c Camera) (sourceSnapshot, error) {
		return sourceSnapshot{Features: []Feature{{Parts: [][]XY{{c.Center}}}}, RefreshAt: now.Add(time.Hour)}, nil
	}), true)
	old := oneSourceRequest(t, l.poll(context.Background(), a, now, false))
	result := old.run()
	if l.accept(result, b, now.Add(10*time.Millisecond)) || old.ctx.Err() == nil || l.busy() || len(l.view().Features) != 0 {
		t.Fatal("late camera result was applied before the next polling tick")
	}
	if jobs := l.poll(context.Background(), b, now.Add(50*time.Millisecond), false); len(jobs) != 0 {
		t.Fatal("camera change bypassed debounce")
	}
	current := oneSourceRequest(t, l.poll(context.Background(), b, now.Add(110*time.Millisecond), false))
	if !l.accept(current.run(), b, now.Add(110*time.Millisecond)) || l.view().Features[0].Parts[0][0] != b.Center {
		t.Fatal("current viewport result was not applied")
	}
	if l.accept(result, b, now.Add(time.Second)) || l.view().Features[0].Parts[0][0] != b.Center {
		t.Fatal("an old result replaced the current snapshot")
	}
}

func TestSourcesReturningToCancelledViewportStartsAnotherRequest(t *testing.T) {
	now := time.Unix(1000, 0)
	a, b := fit(nil, 120, 40, 2), fit(nil, 100, 30, 2)
	var l sourceLoader
	t.Cleanup(l.close)
	l.set("basemap", sourceFunc(func(context.Context, Camera) (sourceSnapshot, error) {
		return sourceSnapshot{Features: []Feature{{Label: "usable"}}}, nil
	}), true)
	old := oneSourceRequest(t, l.poll(context.Background(), a, now, false))
	l.poll(context.Background(), b, now.Add(10*time.Millisecond), false)
	l.poll(context.Background(), a, now.Add(20*time.Millisecond), false)
	current := oneSourceRequest(t, l.poll(context.Background(), a, now.Add(120*time.Millisecond), false))
	if old.ctx.Err() == nil || l.accept(old.run(), a, now.Add(120*time.Millisecond)) {
		t.Fatal("cancelled request survived a round-trip camera change")
	}
	if !l.accept(current.run(), a, now.Add(120*time.Millisecond)) || len(l.view().Features) != 1 {
		t.Fatal("returning to the old viewport left the source permanently empty")
	}
}

func TestSourcesRefreshIndependentlyWhileAnotherSourceIsBlocked(t *testing.T) {
	now := time.Unix(1000, 0)
	c := fit(nil, 120, 40, 2)
	started, release := make(chan struct{}), make(chan struct{})
	var l sourceLoader
	t.Cleanup(l.close)
	l.set("slow", sourceFunc(func(ctx context.Context, _ Camera) (sourceSnapshot, error) {
		close(started)
		select {
		case <-release:
			return sourceSnapshot{Status: "Temporarily unavailable", RefreshAt: now.Add(10 * time.Second)}, nil
		case <-ctx.Done():
			return sourceSnapshot{}, ctx.Err()
		}
	}), true)
	calls := 0
	l.set("overlay", sourceFunc(func(context.Context, Camera) (sourceSnapshot, error) {
		calls++
		return sourceSnapshot{Features: []Feature{{Label: "overlay"}}, RefreshAt: now.Add(time.Duration(calls*5) * time.Second)}, nil
	}), true)
	jobs := l.poll(context.Background(), c, now, false)
	if len(jobs) != 2 {
		t.Fatal("enabled sources did not get independent jobs")
	}
	result := make(chan sourceResult, 1)
	go func() { result <- jobs[0].run() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow source did not start")
	}
	if !l.accept(jobs[1].run(), c, now) || len(l.view().Features) != 1 || !l.busy() {
		t.Fatal("slow source blocked another source's usable data")
	}
	if jobs := l.poll(context.Background(), c, now.Add(4*time.Second), false); len(jobs) != 0 {
		t.Fatal("a fresh source reloaded early")
	}
	refresh := oneSourceRequest(t, l.poll(context.Background(), c, now.Add(5*time.Second), false))
	if refresh.name != "overlay" || !l.accept(refresh.run(), c, now.Add(5*time.Second)) || calls != 2 {
		t.Fatal("stationary refresh waited for another source")
	}
	close(release)
	if !l.accept(<-result, c, now.Add(5*time.Second)) || l.busy() || len(l.view().Features) != 1 || !strings.Contains(l.view().status(), "slow: Temporarily unavailable") {
		t.Fatal("one source's failure hid another source or lost its own status")
	}
	if jobs := l.poll(context.Background(), c, now.Add(9*time.Second), false); len(jobs) != 0 {
		t.Fatal("a source ignored its refresh or retry deadline")
	}
	if jobs := l.poll(context.Background(), c, now.Add(10*time.Second), false); len(jobs) != 2 {
		t.Fatal("eligible sources were not scheduled independently")
	}
}

func TestSourcesReplacementAndVisibilityInvalidateOldGenerations(t *testing.T) {
	now := time.Unix(1000, 0)
	c := fit(nil, 120, 40, 2)
	var l sourceLoader
	t.Cleanup(l.close)
	oldSource := sourceFunc(func(context.Context, Camera) (sourceSnapshot, error) {
		return sourceSnapshot{Features: []Feature{{Label: "old"}}}, nil
	})
	newSource := sourceFunc(func(context.Context, Camera) (sourceSnapshot, error) {
		return sourceSnapshot{Features: []Feature{{Label: "new"}}, Credits: []sourceCredit{{"Example", "https://example.test"}}}, nil
	})
	l.set("basemap", oldSource, true)
	old := oneSourceRequest(t, l.poll(context.Background(), c, now, false))
	l.set("basemap", newSource, true)
	current := oneSourceRequest(t, l.poll(context.Background(), c, now, false))
	if old.ctx.Err() == nil || l.accept(old.run(), c, now) || !l.accept(current.run(), c, now) || l.view().Features[0].Label != "new" {
		t.Fatal("source replacement reused an old generation")
	}
	l.set("basemap", newSource, false)
	if jobs := l.poll(context.Background(), c, now, false); len(jobs) != 0 || len(l.view().Features) != 0 || len(l.view().Credits) != 0 {
		t.Fatal("disabled source retained visible data, credits or requests")
	}
	l.set("basemap", newSource, true)
	reenabled := oneSourceRequest(t, l.poll(context.Background(), c, now, false))
	if l.accept(current.run(), c, now) || !l.accept(reenabled.run(), c, now) {
		t.Fatal("reenabling a source accepted an older lifetime's result")
	}
}

func TestSourcesBoundRefreshLoopsAndKeepMovingViewsUpdated(t *testing.T) {
	now := time.Unix(1000, 0)
	c := fit(nil, 120, 40, 2)
	var l sourceLoader
	t.Cleanup(l.close)
	l.set("basemap", sourceFunc(func(context.Context, Camera) (sourceSnapshot, error) {
		return sourceSnapshot{RefreshAt: now.Add(-time.Second)}, nil
	}), true)
	first := oneSourceRequest(t, l.poll(context.Background(), c, now, false))
	l.accept(first.run(), c, now)
	if jobs := l.poll(context.Background(), c, now.Add(999*time.Millisecond), false); len(jobs) != 0 {
		t.Fatal("expired data caused a tight refresh loop")
	}
	refresh := oneSourceRequest(t, l.poll(context.Background(), c, now.Add(time.Second), false))
	l.accept(refresh.run(), c, now.Add(time.Second))
	for _, elapsed := range []time.Duration{1100, 1200} {
		c.pan(1, 0)
		if jobs := l.poll(context.Background(), c, now.Add(elapsed*time.Millisecond), true); len(jobs) != 0 {
			t.Fatal("moving view bypassed its request cadence")
		}
	}
	c.pan(1, 0)
	if jobs := l.poll(context.Background(), c, now.Add(1250*time.Millisecond), true); len(jobs) != 1 {
		t.Fatal("continuously moving view was starved by debounce")
	}
}

func TestSourcesCancelOnShutdownAndSanitiseUnexpectedErrors(t *testing.T) {
	now := time.Unix(1000, 0)
	c := fit(nil, 120, 40, 2)
	var l sourceLoader
	l.set("basemap", sourceFunc(func(context.Context, Camera) (sourceSnapshot, error) {
		return sourceSnapshot{}, errors.New("private source details")
	}), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := oneSourceRequest(t, l.poll(ctx, c, now, false))
	l.accept(first.run(), c, now)
	if l.view().status() != "Source request failed" || !l.view().Sources[0].RefreshAt.Equal(now.Add(10*time.Second)) {
		t.Fatal("unexpected error leaked private details or omitted recovery")
	}
	second := oneSourceRequest(t, l.poll(ctx, c, now.Add(10*time.Second), false))
	cancel()
	if jobs := l.poll(ctx, c, now.Add(11*time.Second), false); len(jobs) != 0 || second.ctx.Err() == nil || l.busy() {
		t.Fatal("shutdown did not cancel outstanding work")
	}
	if l.accept(second.run(), c, now.Add(11*time.Second)) {
		t.Fatal("shutdown accepted a late result")
	}
}

func TestSourceViewCombinesAttributionWithoutMutatingSnapshots(t *testing.T) {
	features := make([]Feature, 1, 2)
	features[0].Label = "first"
	features[:2][1].Label = "untouched capacity"
	shared := sourceCredit{"Shared", "https://shared.test"}
	first := sourceSnapshot{Features: features, Credits: []sourceCredit{shared}, Stats: Stats{Requests: 2}}
	second := sourceSnapshot{Features: []Feature{{Label: "second"}}, Credits: []sourceCredit{shared, {"Other", "https://other.test"}}, Stats: Stats{Requests: 3}}
	var view sourceView
	view.add("basemap", first, false)
	view.add("overlay", second, false)
	credits, links := view.attribution()
	if credits != "Shared  Other" || !reflect.DeepEqual(links, []string{"https://shared.test", "https://other.test"}) || view.Stats.Requests != 5 || len(view.Features) != 2 {
		t.Fatal("source composition lost data, stats or attribution")
	}
	if features[:2][1].Label != "untouched capacity" || len(first.Credits) != 1 {
		t.Fatal("composition mutated a source-owned snapshot")
	}
}
