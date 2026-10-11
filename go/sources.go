package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

type sourceCredit struct {
	Text string
	URL  string
}

type sourceSnapshot struct {
	Features       []Feature
	Stats          Stats
	Status         string
	RefreshAt      time.Time
	Credits        []sourceCredit
	Failed         bool
	Unavailable    bool
	AccessDenied   bool
	AccessVerified bool
	FetchedAt      time.Time
}

type mapSource interface {
	load(context.Context, Camera) (sourceSnapshot, error)
}

type sourceRequestMatcher interface {
	sameRequest(Camera, Camera) bool
}

type sourceRequest struct {
	name       string
	generation uint64
	camera     Camera
	ctx        context.Context
	source     mapSource
}

type sourceResult struct {
	name       string
	generation uint64
	camera     Camera
	snapshot   sourceSnapshot
	err        error
}

func (r sourceRequest) run() sourceResult {
	snapshot, err := r.source.load(r.ctx, r.camera)
	if r.ctx.Err() != nil {
		err = r.ctx.Err()
	}
	return sourceResult{
		name:       r.name,
		generation: r.generation,
		camera:     r.camera,
		snapshot:   snapshot,
		err:        err,
	}
}

type sourceState struct {
	name                string
	source              mapSource
	enabled             bool
	generation          uint64
	cancel              context.CancelFunc
	wanted, requested   Camera
	hasView, hasRequest bool
	completed           bool
	changed, started    time.Time
	snapshot            sourceSnapshot
}

func (s *sourceState) invalidate() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.generation++
}

func (s *sourceState) sameRequest(a, b Camera) bool {
	if source, ok := s.source.(sourceRequestMatcher); ok {
		return source.sameRequest(a, b)
	}
	return a == b
}

func (s *sourceState) observe(camera Camera, now time.Time) {
	if !s.hasView || !s.sameRequest(s.wanted, camera) {
		s.invalidate()
		s.changed = now
	}
	s.wanted, s.hasView = camera, true
}

type sourceLoader struct{ states []*sourceState }

func (l *sourceLoader) set(name string, source mapSource, enabled bool) {
	var state *sourceState
	for _, candidate := range l.states {
		if candidate.name == name {
			state = candidate
			break
		}
	}
	if state == nil {
		state = &sourceState{name: name}
		l.states = append(l.states, state)
	}
	state.invalidate()
	state.source, state.enabled = source, enabled
	state.hasView, state.hasRequest = false, false
	state.completed = false
	state.snapshot = sourceSnapshot{}
}

func (l *sourceLoader) poll(ctx context.Context, camera Camera, now time.Time, moving bool) []sourceRequest {
	var requests []sourceRequest
	for _, state := range l.states {
		if !state.enabled {
			continue
		}
		if ctx.Err() != nil {
			l.close()
			return nil
		}
		state.observe(camera, now)
		if state.cancel != nil {
			continue
		}
		if state.hasRequest {
			viewLoadNeeded := !state.sameRequest(state.requested, camera) || !state.completed
			if viewLoadNeeded {
				settling := now.Sub(state.changed) < 100*time.Millisecond
				if settling {
					movingUpdateDue := moving && now.Sub(state.started) >= 250*time.Millisecond
					if !movingUpdateDue {
						continue
					}
				}
			} else if state.snapshot.RefreshAt.IsZero() || now.Before(state.snapshot.RefreshAt) {
				continue
			}
		}
		requestCtx, cancel := context.WithCancel(ctx)
		state.generation++
		state.cancel, state.requested, state.hasRequest, state.started = cancel, camera, true, now
		state.completed = false
		requests = append(requests, sourceRequest{
			name:       state.name,
			generation: state.generation,
			camera:     camera,
			ctx:        requestCtx,
			source:     state.source,
		})
	}
	return requests
}

func (l *sourceLoader) accept(result sourceResult, camera Camera, now time.Time) bool {
	for _, state := range l.states {
		if state.name != result.name || !state.enabled {
			continue
		}
		state.observe(camera, now)
		superseded := state.generation != result.generation
		notInFlight := state.cancel == nil
		if superseded || notInFlight || !state.sameRequest(result.camera, camera) {
			return false
		}
		state.cancel()
		state.cancel = nil
		if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
			state.hasRequest = false
			return false
		}
		snapshot := result.snapshot
		snapshot.Failed = snapshot.Failed || result.err != nil
		if snapshot.Failed {
			if snapshot.Status == "" {
				snapshot.Status = "Source request failed"
			}
			if snapshot.RefreshAt.IsZero() && !snapshot.Unavailable {
				snapshot.RefreshAt = now.Add(10 * time.Second)
			}
		}
		retainPrevious := snapshot.Failed && len(snapshot.Features) == 0
		previousComplete := len(state.snapshot.Features) > 0 || !state.snapshot.FetchedAt.IsZero()
		if retainPrevious && previousComplete {
			snapshot.Features = state.snapshot.Features
			snapshot.Credits = state.snapshot.Credits
			snapshot.FetchedAt = state.snapshot.FetchedAt
		}
		if !snapshot.RefreshAt.IsZero() && snapshot.RefreshAt.Before(now.Add(time.Second)) {
			snapshot.RefreshAt = now.Add(time.Second)
		}
		state.snapshot = snapshot
		state.completed = true
		return true
	}
	return false
}

func (l *sourceLoader) busy() bool {
	for _, state := range l.states {
		if state.enabled && state.cancel != nil {
			return true
		}
	}
	return false
}

func (l *sourceLoader) close() {
	for _, state := range l.states {
		state.invalidate()
		state.enabled = false
	}
}

type sourceInfo struct {
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	Loading   bool       `json:"loading"`
	RefreshAt *time.Time `json:"refresh_at,omitempty"`
	Stats     Stats      `json:"tile_stats"`
	FetchedAt *time.Time `json:"fetched_at,omitempty"`
	Stale     bool       `json:"stale,omitempty"`
}

type sourceView struct {
	Features []Feature
	Stats    Stats
	Credits  []sourceCredit
	Sources  []sourceInfo
}

func (v *sourceView) add(name string, snapshot sourceSnapshot, loading bool) {
	if len(v.Features) == 0 {
		v.Features = snapshot.Features
	} else if len(snapshot.Features) > 0 {
		combined := make([]Feature, 0, len(v.Features)+len(snapshot.Features))
		combined = append(combined, v.Features...)
		v.Features = append(combined, snapshot.Features...)
	}
	v.Stats.Requests += snapshot.Stats.Requests
	v.Stats.Hits += snapshot.Stats.Hits
	v.Stats.Errors += snapshot.Stats.Errors
	v.Stats.Cached += snapshot.Stats.Cached
	for _, credit := range snapshot.Credits {
		found := false
		for _, existing := range v.Credits {
			found = found || existing == credit
		}
		if !found {
			v.Credits = append(v.Credits, credit)
		}
	}
	info := sourceInfo{Name: name, Status: snapshot.Status, Loading: loading, Stats: snapshot.Stats}
	if !snapshot.RefreshAt.IsZero() {
		deadline := snapshot.RefreshAt
		info.RefreshAt = &deadline
	}
	if !snapshot.FetchedAt.IsZero() {
		fetched := snapshot.FetchedAt
		info.FetchedAt = &fetched
		info.Stale = snapshot.Failed
	}
	v.Sources = append(v.Sources, info)
}

func (l *sourceLoader) view() sourceView {
	v := sourceView{Sources: []sourceInfo{}}
	for _, state := range l.states {
		if state.enabled {
			v.add(state.name, state.snapshot, state.cancel != nil)
		}
	}
	return v
}

func (v sourceView) status() string {
	var statuses []string
	for _, source := range v.Sources {
		if source.Status != "" {
			status := source.Status
			if len(v.Sources) > 1 {
				status = source.Name + ": " + status
			}
			statuses = append(statuses, status)
		}
	}
	return strings.Join(statuses, " | ")
}

func (v sourceView) attribution() (string, []string) {
	var texts []string
	links := []string{}
	for _, credit := range v.Credits {
		texts = append(texts, credit.Text)
		if credit.URL != "" {
			links = append(links, credit.URL)
		}
	}
	if len(texts) == 0 {
		return "No basemap", links
	}
	return strings.Join(texts, "  "), links
}

func earlier(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}
