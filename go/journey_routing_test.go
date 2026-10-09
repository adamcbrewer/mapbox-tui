package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func routedTestJourney(t *testing.T, w *Workspace) ItemRef {
	t.Helper()
	ref := w.newJourney()
	if err := w.addJourneyWaypoint(ref); err != nil {
		t.Fatal(err)
	}
	j, _ := w.journey(ref)
	for i, location := range directionsTestLocations() {
		if err := w.setJourneyLocation(ref, j.Stops[i].ID, location); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.setJourneyMode(ref, journeyWalking); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestJourneyRouteReversalUndoAndImportIsolation(t *testing.T) {
	d, err := loadDocument("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	var w Workspace
	imported := w.addDocument(d)
	original := w.selection.track
	ref := routedTestJourney(t, &w)
	j, _ := w.journey(ref)
	stops := slices.Clone(j.Stops)
	profile := w.profile(80, 5)
	p := newDirectionsClient("token")
	var requests int
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), float64(requests)*100, 300)), nil
	})
	calculate := func() {
		t.Helper()
		request, err := w.beginJourneyRoute(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		result := request.run(p)
		if !w.acceptJourneyRoute(result) || result.err != nil || !j.routeCurrent() {
			t.Fatal("route did not become current", result.err)
		}
		if w.position() != nil || w.selection.playback != nil || w.profile(80, 5) != profile {
			t.Fatal("route changed playback or imported elevation")
		}
	}
	calculate()
	first := j.Route
	if !reflect.DeepEqual(j.Stops, stops) || w.selection.track != first.Track {
		t.Fatal("routing changed ordered stop intent or missed scene geometry")
	}
	if err := w.reverseJourney(ref); err != nil {
		t.Fatal(err)
	}
	if j.routeCurrent() || j.Route != first || w.selection.track == first.Track {
		t.Fatal("reversal used old geometry/estimates as current")
	}
	for i, stop := range j.Stops {
		if stop.ID != stops[len(stops)-1-i].ID {
			t.Fatal("reversal changed stop identity/order")
		}
	}
	calculate()
	if j.Route.Distance != 200 || j.Route.Track.Points[0].Lon != stops[len(stops)-1].Location.Lon+.0001 {
		t.Fatal("reversal did not obtain new directional geometry/estimates")
	}
	if err := w.undoJourney(ref); err != nil {
		t.Fatal(err)
	}
	if j.routeCurrent() || !reflect.DeepEqual(j.Stops, stops) {
		t.Fatal("undo retained reversed estimates or lost original stops")
	}
	calculate()
	if j.Route.Distance != 300 || j.Route.Track.Points[0].Lon != stops[0].Location.Lon+.0001 {
		t.Fatal("undo did not recalculate the restored intent")
	}
	entry, i, _ := w.lookup(imported)
	if entry.document.Items[i].Track != original {
		t.Fatal("routing replaced imported data")
	}
	if err := w.setJourneyMode(ref, journeyManual); err != nil {
		t.Fatal(err)
	}
	if j.routeCurrent() || len(w.selection.track.Points) != 3 || w.selection.track.Points[0].Lon != -.1 {
		t.Fatal("manual fallback did not restore user geometry")
	}
	if err := w.undoJourney(ref); err != nil || j.Mode != journeyWalking || j.routeCurrent() {
		t.Fatal("mode undo did not restore intent and invalidate estimates", err)
	}
}

func TestJourneyRouteCancelsAndRejectsSupersededResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Workspace, ItemRef) error
	}{
		{name: "edited location", change: func(w *Workspace, ref ItemRef) error {
			j, _ := w.journey(ref)
			return w.setJourneyLocation(ref, j.Stops[1].ID, placeLocation{Lon: -.16, Lat: 51.56})
		}},
		{name: "waypoint added", change: (*Workspace).addJourneyWaypoint},
		{name: "waypoint removed", change: func(w *Workspace, ref ItemRef) error { return w.removeJourneyLocation(ref, 1) }},
		{name: "waypoint reordered", change: func(w *Workspace, ref ItemRef) error {
			if err := w.addJourneyWaypoint(ref); err != nil {
				return err
			}
			return w.moveJourneyWaypoint(ref, 1, 1)
		}},
		{name: "reversal", change: (*Workspace).reverseJourney},
		{name: "undo", change: (*Workspace).undoJourney},
		{name: "mode switch", change: func(w *Workspace, ref ItemRef) error { return w.setJourneyMode(ref, journeyCycling) }},
		{name: "retry", change: func(w *Workspace, ref ItemRef) error {
			_, err := w.beginJourneyRoute(t.Context(), ref)
			return err
		}},
		{name: "item removed", change: (*Workspace).removeItem},
		{name: "file removed", change: func(w *Workspace, ref ItemRef) error { return w.removeDocument(ref.DocumentID) }},
		{name: "clear", change: func(w *Workspace, _ ItemRef) error { w.clear(); return nil }},
		{name: "credentials replaced", change: func(w *Workspace, _ ItemRef) error { w.invalidateJourneyRoutes(); return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w Workspace
			ref := routedTestJourney(t, &w)
			request, err := w.beginJourneyRoute(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(w.cancelJourneyRoutes)
			p := newDirectionsClient("token")
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				return locationResponse(directionsTestResponse(directionsTestLocations(), 100, 100)), nil
			})
			late := request.run(p)
			if err := tc.change(&w, ref); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(request.ctx.Err(), context.Canceled) || w.acceptJourneyRoute(late) {
				t.Fatal("obsolete request was not cancelled or late success was accepted")
			}
			late.err = context.Canceled
			if w.acceptJourneyRoute(late) {
				t.Fatal("late error overwrote current status")
			}
			if _, err := w.journey(ref); err != nil {
				w.newJourney()
				if w.acceptJourneyRoute(late) {
					t.Fatal("late result was accepted by a replacement journey")
				}
			}
		})
	}
}

func TestJourneyRoutingFailurePreservesStopsAndStaleSnapshot(t *testing.T) {
	var w Workspace
	ref := routedTestJourney(t, &w)
	j, _ := w.journey(ref)
	p := newDirectionsClient("token")
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return locationResponse(directionsTestResponse(directionsTestLocations(), 100, 100)), nil
	})
	request, err := w.beginJourneyRoute(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	w.acceptJourneyRoute(request.run(p))
	previous := j.Route
	if err := w.reverseJourney(ref); err != nil {
		t.Fatal(err)
	}
	stops, manual := slices.Clone(j.Stops), w.selection.track
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return locationResponse(`{"code":"NoRoute"}`), nil
	})
	request, err = w.beginJourneyRoute(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !w.acceptJourneyRoute(request.run(p)) || !strings.Contains(j.Error, "no Walking route") {
		t.Fatal("no-route error not exposed")
	}
	if !reflect.DeepEqual(j.Stops, stops) || w.selection.track != manual || j.Route != previous || j.routeCurrent() {
		t.Fatal("failure changed stops, removed stale snapshot or reused its estimates")
	}
	if err := w.addJourneyWaypoint(ref); err != nil {
		t.Fatal(err)
	}
	_, err = w.beginJourneyRoute(t.Context(), ref)
	if err == nil || !strings.Contains(j.Error, "Waypoint is not set") || j.Busy {
		t.Fatal("unresolved stop did not block requests with feedback", err)
	}
}
