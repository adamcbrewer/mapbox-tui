package main

import (
	"slices"
	"strconv"
	"testing"
)

func TestControlJourneyInitializationAndReplacementUndo(t *testing.T) {
	m := journeyViewer(t)
	response, _ := m.applyControl(controlTestRequest(m, "journey.create", map[string]any{
		"mode": "offline", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}},
	}))
	ref := controlTestRef(t, response)
	j, _ := m.workspace.journey(ref)
	initial := slices.Clone(j.Stops)
	if !j.modeChosen || j.Mode != journeyManual {
		t.Fatal("creation lost the explicit offline mode")
	}
	if err := m.workspace.undoJourney(ref); err == nil {
		t.Fatal("creation left an empty draft in undo history")
	}
	response, _ = m.applyControl(controlTestRequest(m, "journey.stops", map[string]any{
		"ref": ref, "stops": []controlStop{{Lat: 53, Lon: 2}, {Lat: 54, Lon: 3}, {Lat: 55, Lon: 4}},
	}))
	if response.Error != nil || !response.Changed || len(j.Stops) != 3 {
		t.Fatal("stop replacement failed", response)
	}
	lastID := initial[len(initial)-1].ID
	for _, stop := range j.Stops {
		if stop.ID <= lastID || stop.Location == nil {
			t.Fatal("replacement reused an old identity or lost a location")
		}
		lastID = stop.ID
	}
	if err := m.workspace.undoJourney(ref); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(j.Stops, initial) {
		t.Fatal("undo did not restore original stop identities and locations")
	}
	response, _ = m.applyControl(controlTestRequest(m, "journey.stop.add", map[string]any{
		"ref": ref, "after": initial[0].ID, "lat": 51.5, "lon": .5,
	}))
	if response.Error != nil || j.Stops[1].ID <= lastID ||
		j.Stops[0] != initial[0] || j.Stops[2] != initial[1] {
		t.Fatal("insertion reused a replaced identity or changed endpoints", response)
	}
}

func TestControlJourneyRejectedInsertionsPreserveState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		after int
	}{
		{name: "unknown stop", count: 2, after: 99},
		{name: "destination", count: 2, after: 2},
		{name: "stop limit", count: 5000, after: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := journeyViewer(t)
			stops := make([]controlStop, tc.count)
			for i := range stops {
				stops[i] = controlStop{Lat: 51, Lon: float64(i) / 1000}
			}
			response, _ := m.applyControl(controlTestRequest(m, "journey.create", map[string]any{
				"mode": "offline", "stops": stops,
			}))
			ref := controlTestRef(t, response)
			j, _ := m.workspace.journey(ref)
			before := slices.Clone(j.Stops)
			revision := m.workspace.liveRevision
			response, _ = m.applyControl(controlTestRequest(m, "journey.stop.add", map[string]any{
				"ref": ref, "after": tc.after, "lat": 51, "lon": 0,
			}))
			if response.Error == nil || response.Changed || m.workspace.liveRevision != revision ||
				!slices.Equal(j.Stops, before) {
				t.Fatal("rejected insertion changed the journey", response)
			}
			if err := m.workspace.undoJourney(ref); err == nil {
				t.Fatal("rejected insertion added an undo entry")
			}
		})
	}
}

func TestControlInvalidJourneyCreationDoesNotAllocateIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  string
		stops []controlStop
	}{
		{name: "invalid mode", mode: "flying", stops: []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}}},
		{name: "one stop", mode: "offline", stops: []controlStop{{Lat: 51, Lon: 0}}},
		{name: "invalid coordinates", mode: "offline", stops: []controlStop{{Lat: 91, Lon: 0}, {Lat: 52, Lon: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := journeyViewer(t)
			revision := m.workspace.liveRevision
			response, _ := m.applyControl(controlTestRequest(m, "journey.create", map[string]any{
				"mode": tc.mode, "stops": tc.stops,
			}))
			if response.Error == nil || response.Changed {
				t.Fatal("invalid creation was not rejected", response)
			}
			if len(m.workspace.documents) != 0 || m.workspace.liveRevision != revision {
				t.Fatal("invalid creation changed the workspace", response)
			}
			response, _ = m.applyControl(controlTestRequest(m, "journey.create", map[string]any{
				"mode": "offline", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}},
			}))
			if ref := controlTestRef(t, response); ref.DocumentID != 1 {
				t.Fatal("invalid creation consumed a document identity", ref)
			}
		})
	}
}

func TestControlJourneyStopLimit(t *testing.T) {
	m := journeyViewer(t)
	stops := make([]controlStop, 5000)
	for i := range stops {
		stops[i] = controlStop{Lat: 51, Lon: float64(i) / 100000}
	}
	response, _ := m.applyControl(controlTestRequest(m, "journey.create", map[string]any{
		"mode": "offline", "stops": stops,
	}))
	ref := controlTestRef(t, response)
	for _, count := range []int{5000, 5001} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			values := append(slices.Clone(stops), controlStop{Lat: 51})[:count]
			for _, action := range []string{"journey.create", "journey.stops"} {
				args := map[string]any{"stops": values}
				if action == "journey.create" {
					args["mode"] = "offline"
				} else {
					args["ref"] = ref
				}
				before := m.workspace.liveRevision
				response, _ := m.applyControl(controlTestRequest(m, action, args))
				if count == 5000 {
					if response.Error != nil || !response.Changed {
						t.Fatal("remote journey rejected the permitted stop count", action, response)
					}
				} else if response.Error == nil || response.Changed || m.workspace.liveRevision != before {
					t.Fatal("oversized remote journey changed state", action, response)
				}
			}
		})
	}
}
