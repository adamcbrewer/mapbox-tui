package main

import (
	"math"
	"slices"
	"testing"
)

func TestJourneyEditsKeepIdentityAndImportsAndReverseEntireOrder(t *testing.T) {
	d, err := loadDocument("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	var w Workspace
	imported := w.addDocument(d)
	original := w.selection.track
	w.selection.playback.toggle()
	ref := w.newJourney()
	j, err := w.journey(ref)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := w.addJourneyWaypoint(ref); err != nil {
			t.Fatal(err)
		}
	}
	for i, stop := range j.Stops {
		if err := w.setJourneyLocation(ref, stop.ID, placeLocation{
			Name: string(rune('A' + i)), Lon: float64(i) * .01, Lat: 51.5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	entry, _, _ := w.lookup(ref)
	color := entry.colors[ref.ItemID]
	distance := w.selection.track.Distance
	before := slices.Clone(j.Stops)
	if err := w.reverseJourney(ref); err != nil {
		t.Fatal(err)
	}
	for i, stop := range j.Stops {
		if stop.ID != before[len(before)-1-i].ID {
			t.Fatal("reversal did not reverse intermediate waypoints")
		}
	}
	if math.Abs(w.selection.track.Distance-distance) > 1e-6 || entry.colors[ref.ItemID] != color {
		t.Fatal("reversal changed distance or colour")
	}
	if w.selection.playback != nil || w.position() != nil {
		t.Fatal("planned journey invented playback")
	}
	if err := w.undoJourney(ref); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(j.Stops, before) {
		t.Fatal("one undo did not restore the whole journey")
	}
	importEntry, i, _ := w.lookup(imported)
	if importEntry.document.Items[i].Track != original {
		t.Fatal("editing replaced an imported track")
	}
	second := w.newJourney()
	if second == ref || len(w.documents) != 3 {
		t.Fatal("generated journeys were deduplicated by their empty path")
	}
	if err := w.removeItem(ref); err != nil {
		t.Fatal(err)
	}
	if err := w.reverseJourney(ref); err == nil {
		t.Fatal("removed journey still accepted edits")
	}
}

func TestJourneyIncompleteLocationsAndInvalidEditsAreGapSafe(t *testing.T) {
	var w Workspace
	ref := w.newJourney()
	if _, found := w.visibleBounds(); found {
		t.Fatal("empty draft contributed bounds")
	}
	if err := w.addJourneyWaypoint(ref); err != nil {
		t.Fatal(err)
	}
	j, _ := w.journey(ref)
	for _, i := range []int{0, 2} {
		if err := w.setJourneyLocation(ref, j.Stops[i].ID, placeLocation{Lon: float64(i), Lat: 51}); err != nil {
			t.Fatal(err)
		}
	}
	track := w.selection.track
	if len(track.Segments) != 2 || track.Distance != 0 {
		t.Fatal("unresolved waypoint gained connecting geometry")
	}
	if err := w.setJourneyLocation(ref, j.Stops[1].ID, placeLocation{Lon: math.NaN(), Lat: 0}); err == nil {
		t.Fatal("accepted invalid coordinates")
	}
	if w.selection.track != track || j.Stops[1].Location != nil {
		t.Fatal("failed edit changed the draft")
	}
	if err := w.setJourneyLocation(ref, j.Stops[1].ID, placeLocation{Lon: 1, Lat: 51}); err != nil {
		t.Fatal(err)
	}
	if w.selection.track.Distance <= 0 || len(w.selection.track.Segments) != 1 {
		t.Fatal("resolved journey did not recompute distance and parts")
	}
	for _, point := range w.selection.track.Points {
		if point.Elevation != nil || point.Time != nil {
			t.Fatal("planned geometry invented measurements")
		}
	}
	if err := w.removeJourneyLocation(ref, 1); err != nil {
		t.Fatal(err)
	}
	if len(j.Stops) != 2 || len(w.selection.track.Points) != 2 {
		t.Fatal("deleting a waypoint did not update geometry")
	}
	if err := w.removeJourneyLocation(ref, 0); err != nil {
		t.Fatal(err)
	}
	if len(j.Stops) != 2 || j.Stops[0].Location != nil || w.selection.track.Distance != 0 {
		t.Fatal("clearing start removed an endpoint or retained its distance")
	}
}

func TestJourneyEditsAndUndoReuseImportedProfiles(t *testing.T) {
	for _, tc := range []struct {
		name           string
		importSelected bool
	}{
		{name: "journey selected"},
		{name: "import selected", importSelected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := loadDocument("../fixtures/example.gpx")
			if err != nil {
				t.Fatal(err)
			}
			var w Workspace
			imported := w.addDocument(d)
			ref := w.newJourney()
			if tc.importSelected {
				if err := w.selectItem(imported); err != nil {
					t.Fatal(err)
				}
			}
			profile := w.profile(80, 5)
			if profile == nil || !profile.Available {
				t.Fatal("imported elevation profile unavailable")
			}
			j, err := w.journey(ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.setJourneyLocation(ref, j.Stops[0].ID, placeLocation{Lon: 0, Lat: 51}); err != nil {
				t.Fatal(err)
			}
			if w.profile(80, 5) != profile {
				t.Fatal("journey edit rebuilt an unchanged imported profile")
			}
			if err := w.reverseJourney(ref); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := w.undoJourney(ref); err != nil {
					t.Fatal(err)
				}
				if w.profile(80, 5) != profile {
					t.Fatal("journey undo rebuilt an unchanged imported profile")
				}
			}
			if j.Stops[0].Location != nil || j.Stops[1].Location != nil {
				t.Fatal("undo did not restore the empty draft")
			}
		})
	}
}

func TestCoordinateLocation(t *testing.T) {
	for _, tc := range []struct {
		name, input  string
		matched, bad bool
	}{
		{name: "latitude first", input: "51.5, -0.1", matched: true},
		{name: "address with comma", input: "Brighton, UK"},
		{name: "out of bounds", input: "91, 0", matched: true, bad: true},
		{name: "not finite", input: "NaN, 0", matched: true, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location, matched, err := coordinateLocation(tc.input)
			if matched != tc.matched || (err != nil) != tc.bad {
				t.Fatalf("matched %v, err %v", matched, err)
			}
			if tc.name == "latitude first" && (location.Lat != 51.5 || location.Lon != -.1) {
				t.Fatal("coordinate order changed")
			}
		})
	}
}
