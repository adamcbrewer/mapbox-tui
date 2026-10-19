package main

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"
)

func TestJourneyInitializationOwnsStateAndPreservesRecording(t *testing.T) {
	for _, mode := range []journeyMode{journeyManual, journeyWalking, journeyCycling, journeyDriving} {
		for _, existingDraft := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%v", mode.label(), existingDraft), func(t *testing.T) {
				var w Workspace
				if _, err := w.open("../fixtures/example.gpx"); err != nil {
					t.Fatal(err)
				}
				w.setPlaybackPlaying(true)
				selection := w.selection
				locations := []placeLocation{
					{Name: "Start", Lon: -.1, Lat: 51.5, Source: placeSourceCoordinates},
					{Name: "Café", Lon: -.15, Lat: 51.55, Source: placeSourceMapboxSearch,
						ProviderID: "cafe", Attribution: "© Mapbox"},
					{Name: "End", Lon: -.2, Lat: 51.6, Source: placeSourceCoordinates},
				}
				var ref ItemRef
				var err error
				if existingDraft {
					ref = w.createJourney()
					if err := w.addJourneyWaypoint(ref); err != nil {
						t.Fatal(err)
					}
					err = w.initializeJourney(ref, locations, mode)
				} else {
					ref, err = w.createJourneyWithLocations(locations, mode)
				}
				if err != nil {
					t.Fatal(err)
				}
				j, _ := w.journey(ref)
				if j.Mode != mode || !j.modeChosen || !j.hasCompleteLocations() {
					t.Fatal("initialization lost the chosen mode or locations")
				}
				for i, stop := range j.Stops {
					if stop.ID != i+1 || *stop.Location != locations[i] {
						t.Fatal("initialization lost stop identities or location metadata")
					}
				}
				locations[1].Name = "Changed caller input"
				if j.Stops[1].Location.Name != "Café" {
					t.Fatal("journey retained mutable caller storage")
				}
				if w.selection != selection || !selection.playback.Playing {
					t.Fatal("initialization interrupted the imported recording")
				}
				if err := w.undoJourney(ref); err == nil {
					t.Fatal("initialization retained undo history")
				}
				if err := w.addJourneyWaypoint(ref); err != nil {
					t.Fatal(err)
				}
				if j.Stops[2].ID != 4 {
					t.Fatal("initialization did not establish the next stop identity")
				}
			})
		}
	}
}

func TestJourneyInitialIntentAssignsIDsAndOwnsLocations(t *testing.T) {
	location := placeLocation{Name: "Start", Lon: 0, Lat: 51, Source: placeSourceCoordinates}
	stops := []journeyStop{{ID: 40, Location: &location}, {ID: 80}, {ID: 120}}
	var w Workspace
	ref, err := w.createJourneyFromIntent(journeyIntent{Stops: stops, Mode: journeyCycling})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := w.journey(ref)
	if len(j.Stops) != len(stops) {
		t.Fatal("initialization changed the number of stops")
	}
	for i, stop := range j.Stops {
		if stop.ID != i+1 {
			t.Fatal("initialization retained caller-assigned stop IDs")
		}
	}
	location.Name = "Changed caller input"
	stops[0].Location = nil
	if j.Stops[0].Location.Name != "Start" || j.Stops[1].Location != nil {
		t.Fatal("initialization retained caller storage or filled a missing stop")
	}
}

func TestJourneyRejectedPlansPreserveState(t *testing.T) {
	valid := []placeLocation{{Lon: 0, Lat: 51}, {Lon: 1, Lat: 52}}
	for _, tc := range []struct {
		name      string
		locations []placeLocation
		mode      journeyMode
	}{
		{name: "no stops"},
		{name: "one stop", locations: valid[:1]},
		{name: "over stop limit", locations: journeyLimitLocations(5001)},
		{name: "invalid coordinates", locations: []placeLocation{{Lon: math.NaN()}, valid[1]}},
		{name: "invalid mode", locations: valid, mode: "flying"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w Workspace
			ref := w.createJourney()
			j, _ := w.journey(ref)
			before := *j
			before.Stops = slices.Clone(j.Stops)
			revision, nextID := w.liveRevision, w.nextID
			if _, err := w.createJourneyWithLocations(tc.locations, tc.mode); err == nil {
				t.Fatal("invalid creation accepted")
			}
			stops := make([]journeyStop, len(tc.locations))
			for i, location := range tc.locations {
				stops[i].Location = &location
			}
			if _, err := w.createJourneyFromIntent(journeyIntent{Stops: stops, Mode: tc.mode}); err == nil {
				t.Fatal("invalid initial intent accepted")
			}
			if err := w.initializeJourney(ref, tc.locations, tc.mode); err == nil {
				t.Fatal("invalid initialization accepted")
			}
			if tc.mode == journeyManual {
				if err := w.replaceJourneyLocations(ref, tc.locations); err == nil {
					t.Fatal("invalid replacement accepted")
				}
			}
			if !reflect.DeepEqual(j, &before) || len(w.documents) != 1 ||
				w.liveRevision != revision || w.nextID != nextID {
				t.Fatal("rejected plan changed the journey or workspace identity")
			}
		})
	}
}

func TestJourneyMutationRejectsMissingReferences(t *testing.T) {
	ref := ItemRef{DocumentID: 99, ItemID: 1}
	locations := []placeLocation{{Lon: 0, Lat: 51}, {Lon: 1, Lat: 52}}
	for _, tc := range []struct {
		name   string
		mutate func(*Workspace) error
	}{
		{name: "initialize", mutate: func(w *Workspace) error {
			return w.initializeJourney(ref, locations, journeyManual)
		}},
		{name: "replace", mutate: func(w *Workspace) error {
			return w.replaceJourneyLocations(ref, locations)
		}},
		{name: "insert waypoint", mutate: func(w *Workspace) error {
			return w.insertJourneyWaypoint(ref, 1, &locations[0])
		}},
		{name: "add empty waypoint", mutate: func(w *Workspace) error {
			return w.addJourneyWaypoint(ref)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w Workspace
			if err := tc.mutate(&w); err == nil {
				t.Fatal("mutation accepted a missing journey")
			}
			if len(w.documents) != 0 || w.liveRevision != 0 {
				t.Fatal("missing-reference mutation changed workspace")
			}
		})
	}
}

func TestJourneyWaypointInsertionOwnsLocationAndPreservesRoutingOnFailure(t *testing.T) {
	var w Workspace
	locations := directionsTestLocations()
	ref, err := w.createJourneyWithLocations(locations, journeyWalking)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := w.journey(ref)
	request, err := w.beginJourneyRoute(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.cancelJourneyRoutes)
	before := slices.Clone(j.Stops)
	revision, requestID, nextID := w.liveRevision, j.requestID, j.nextID
	invalid := placeLocation{Lon: math.NaN(), Lat: 51}
	if err := w.insertJourneyWaypoint(ref, j.Stops[0].ID, &invalid); err == nil {
		t.Fatal("invalid waypoint was accepted")
	}
	if !slices.Equal(j.Stops, before) || w.liveRevision != revision ||
		j.requestID != requestID || j.nextID != nextID || !j.Busy || request.ctx.Err() != nil {
		t.Fatal("rejected waypoint changed the journey or cancelled routing")
	}
	location := placeLocation{Name: "Waypoint", Lon: -.15, Lat: 51.55, Source: placeSourceCoordinates}
	if err := w.insertJourneyWaypoint(ref, j.Stops[0].ID, &location); err != nil {
		t.Fatal(err)
	}
	location.Name = "Changed caller input"
	if j.Stops[1].ID != nextID+1 || j.Stops[1].Location.Name != "Waypoint" {
		t.Fatal("insertion consumed an ID on failure or retained caller storage")
	}
	if request.ctx.Err() == nil || j.Busy || j.revision == request.revision {
		t.Fatal("successful waypoint insertion did not invalidate the old route")
	}
	if err := w.undoJourney(ref); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(j.Stops, before) {
		t.Fatal("failed insertion affected the next undo")
	}
}

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

func TestJourneyWaypointIDsSurviveUndo(t *testing.T) {
	var w Workspace
	ref := w.newJourney()
	j, _ := w.journey(ref)
	original := slices.Clone(j.Stops)
	if err := w.addJourneyWaypoint(ref); err != nil {
		t.Fatal(err)
	}
	addedID := j.Stops[1].ID
	if err := w.undoJourney(ref); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(j.Stops, original) {
		t.Fatal("undo did not restore the endpoints")
	}
	if err := w.addJourneyWaypoint(ref); err != nil {
		t.Fatal(err)
	}
	if j.Stops[1].ID <= addedID {
		t.Fatal("waypoint insertion reused an undone stop ID")
	}
}

func TestJourneyRejectedLocationPreservesState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location placeLocation
	}{
		{name: "not finite", location: placeLocation{Lon: math.NaN(), Lat: 51}},
		{name: "out of bounds", location: placeLocation{Lon: 0, Lat: 91}},
		{name: "unknown source", location: placeLocation{Lon: 0, Lat: 51, Source: "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w Workspace
			ref := w.newJourney()
			j, _ := w.journey(ref)
			before := slices.Clone(j.Stops)
			selection, revision := w.selection, w.liveRevision
			if err := w.setJourneyLocation(ref, j.Stops[0].ID, tc.location); err == nil {
				t.Fatal("invalid location was accepted")
			}
			if !slices.Equal(j.Stops, before) || w.selection != selection {
				t.Fatal("rejected location changed journey or selection")
			}
			if w.liveRevision != revision {
				t.Fatal("rejected location changed revision")
			}
			if err := w.undoJourney(ref); err == nil {
				t.Fatal("rejected location added an undo entry")
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

func journeyLimitLocations(count int) []placeLocation {
	locations := make([]placeLocation, count)
	for i := range locations {
		locations[i] = placeLocation{Lat: 51, Lon: float64(i) / 100000, Source: placeSourceCoordinates}
	}
	return locations
}

func TestJourneyStopLimit(t *testing.T) {
	for _, emptyWaypoint := range []bool{false, true} {
		name := "located waypoint"
		if emptyWaypoint {
			name = "empty waypoint"
		}
		t.Run(name, func(t *testing.T) {
			var w Workspace
			ref, err := w.createJourneyWithLocations(journeyLimitLocations(5000), journeyManual)
			if err != nil {
				t.Fatal(err)
			}
			j, _ := w.journey(ref)
			if len(j.Stops) != 5000 {
				t.Fatal("creation changed stop count")
			}
			if err := w.replaceJourneyLocations(ref, journeyLimitLocations(4999)); err != nil {
				t.Fatal(err)
			}
			var location *placeLocation
			if !emptyWaypoint {
				location = &placeLocation{Lat: 51, Lon: .001, Source: placeSourceCoordinates}
			}
			if err := w.insertJourneyWaypoint(ref, j.Stops[0].ID, location); err != nil || len(j.Stops) != 5000 {
				t.Fatal("could not insert the final permitted stop", err)
			}
			before := *j
			before.Stops = slices.Clone(j.Stops)
			revision := w.liveRevision
			if err := w.insertJourneyWaypoint(ref, j.Stops[0].ID, location); err == nil {
				t.Fatal("insertion exceeded the stop limit")
			}
			if !reflect.DeepEqual(j, &before) || w.liveRevision != revision {
				t.Fatal("rejected insertion changed state or history")
			}
			if err := w.undoJourney(ref); err != nil || len(j.Stops) != 4999 {
				t.Fatal("rejected insertion changed undo", err)
			}
			if err := w.replaceJourneyLocations(ref, journeyLimitLocations(5000)); err != nil {
				t.Fatal("could not replace stops at the limit", err)
			}
		})
	}
}
