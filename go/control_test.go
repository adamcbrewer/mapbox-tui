package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func controlTestRequest(m *viewer, action string, args any) controlRequest {
	revision := m.workspace.liveRevision
	return controlRequest{Version: controlVersion, ID: newSessionID(), Action: action,
		Generation: m.workspace.liveIdentity(), Revision: &revision, Args: controlJSON(args)}
}

func controlTestResult(t *testing.T, response controlResponse) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err, string(response.Result))
	}
	return result
}

func controlTestRef(t *testing.T, response controlResponse) ItemRef {
	t.Helper()
	var result struct {
		Ref ItemRef `json:"ref"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Ref.DocumentID == 0 {
		t.Fatalf("missing ref: %+v", response)
	}
	return result.Ref
}

func TestControlMutationsGuardHumanEditsAndDeduplicate(t *testing.T) {
	m := journeyViewer(t)
	request := controlTestRequest(m, "journey.create", map[string]any{"mode": "offline", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}}})
	response, _ := m.applyControl(request)
	if response.Error != nil || !response.Changed {
		t.Fatalf("create: %+v", response)
	}
	ref := controlTestRef(t, response)
	duplicate, _ := m.applyControl(request)
	if !reflect.DeepEqual(response, duplicate) || len(m.workspace.documents) != 1 {
		t.Fatal("retry duplicated mutation")
	}
	changed := request
	changed.Args = controlJSON(map[string]any{"mode": "walking", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}}})
	conflict, _ := m.applyControl(changed)
	if conflict.Error == nil || conflict.Error.Code != "request_id_conflict" {
		t.Fatal(conflict)
	}
	stale := controlTestRequest(m, "journey.reverse", map[string]any{"ref": ref})
	if err := m.workspace.reverseJourney(ref); err != nil {
		t.Fatal(err)
	}
	conflict, _ = m.applyControl(stale)
	if conflict.Error == nil || conflict.Error.Code != "revision_conflict" || conflict.Changed {
		t.Fatal(conflict)
	}
	stale = controlTestRequest(m, "journey.reverse", map[string]any{"ref": ref})
	m.clearWorkspace()
	conflict, _ = m.applyControl(stale)
	if conflict.Error == nil || conflict.Error.Code != "workspace_changed" {
		t.Fatal(conflict)
	}
}

func TestControlImportsCompleteIndependentlyAndClearRejectsLateResult(t *testing.T) {
	m := journeyViewer(t)
	var commands []tea.Cmd
	var ids []string
	for _, name := range []string{"example.gpx", "waypoints.gpx"} {
		path, err := filepath.Abs("../fixtures/" + name)
		if err != nil {
			t.Fatal(err)
		}
		request := controlTestRequest(m, "load", map[string]any{"path": path})
		response, cmd := m.applyControl(request)
		if response.State != "pending" || cmd == nil {
			t.Fatal(response)
		}
		commands = append(commands, cmd)
		ids = append(ids, request.ID)
	}
	// Complete in reverse order, unlike the old single load generation.
	m.Update(commands[1]())
	m.Update(commands[0]())
	if len(m.workspace.documents) != 2 {
		t.Fatal("one concurrent load was lost")
	}
	for _, id := range ids {
		response, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": id}))
		if response.State != "applied" || !response.Changed {
			t.Fatal(response)
		}
	}
	path, _ := filepath.Abs("../fixtures/country-ride.gpx")
	request := controlTestRequest(m, "load", map[string]any{"path": path})
	_, cmd := m.applyControl(request)
	late := cmd()
	m.clearWorkspace()
	m.Update(late)
	response, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": request.ID}))
	if response.State != "superseded" || len(m.workspace.documents) != 0 {
		t.Fatal("late import repopulated workspace", response)
	}
}

func TestControlImportFailureAndReopen(t *testing.T) {
	m := journeyViewer(t)
	path, _ := filepath.Abs("../fixtures/example.gpx")
	request := controlTestRequest(m, "load", map[string]any{"path": path})
	_, cmd := m.applyControl(request)
	m.Update(cmd())
	entry := m.workspace.documents[0]
	ref := ItemRef{DocumentID: entry.id, ItemID: entry.document.Items[0].ID}
	if err := m.workspace.setVisible(ref, false); err != nil {
		t.Fatal(err)
	}
	response, cmd := m.applyControl(controlTestRequest(m, "load", map[string]any{"path": path}))
	if cmd != nil || response.State != "applied" || m.workspace.documents[0].document != entry.document || entry.hidden[ref.ItemID] {
		t.Fatal(response)
	}
	request = controlTestRequest(m, "load", map[string]any{"path": path + ".missing"})
	_, cmd = m.applyControl(request)
	m.Update(cmd())
	response, _ = m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": request.ID}))
	if response.Error == nil || response.Changed || len(m.workspace.documents) != 1 {
		t.Fatal(response)
	}
}

func TestControlJourneyRouteAndEditWorkflow(t *testing.T) {
	m := journeyViewer(t)
	m.preferences.BillingAcceptedVersion = billingNoticeVersion
	m.preferences.Offline = false
	m.directions = newDirectionsClient("token")
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 12345, 3661)), nil
	})
	camera := m.destination()
	request := controlTestRequest(m, "journey.create", map[string]any{"mode": "cycling", "stops": []controlStop{{Lat: 51.5, Lon: -0.1}, {Lat: 51.6, Lon: -0.2}}})
	response, cmd := m.applyControl(request)
	if response.State != "pending" || !response.Changed || cmd == nil {
		t.Fatal(response)
	}
	ref := controlTestRef(t, response)
	m.Update(cmd())
	response, _ = m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": request.ID}))
	result := controlTestResult(t, response)
	if response.State != "applied" || result["route_state"] != "ready" || result["distance_m"] != float64(12345) {
		t.Fatal(response, result)
	}
	if m.destination() != camera || m.workspace.selection != nil {
		t.Fatal("planning unexpectedly moved camera or selection")
	}
	for _, test := range []struct {
		name, action string
		args         map[string]any
	}{
		{name: "walking", action: "journey.mode", args: map[string]any{"ref": ref, "mode": "walking"}},
		{name: "waypoint", action: "journey.stop.add", args: map[string]any{"ref": ref, "after": 1, "lat": 51.55, "lon": -0.15}},
		{name: "replace", action: "journey.stop.set", args: map[string]any{"ref": ref, "stop_id": 3, "lat": 51.56, "lon": -0.15}},
		{name: "reverse", action: "journey.reverse", args: map[string]any{"ref": ref}},
		{name: "remove", action: "journey.stop.remove", args: map[string]any{"ref": ref, "stop_id": 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, cmd := m.applyControl(controlTestRequest(m, test.action, test.args))
			if response.Error != nil || cmd == nil {
				t.Fatal(response)
			}
			m.Update(cmd())
		})
	}
	j, _ := m.workspace.journey(ref)
	if len(j.Stops) != 2 || j.Stops[0].ID != 2 || j.Mode != journeyWalking {
		t.Fatal(j)
	}
	response, _ = m.applyControl(controlTestRequest(m, "journey.show", map[string]any{"ref": ref}))
	if response.Error != nil || m.workspace.selection == nil || m.workspace.selection.ref != ref {
		t.Fatal(response)
	}
	// Late routing after a human edit must finish as superseded, without estimates.
	request = controlTestRequest(m, "journey.calculate", map[string]any{"ref": ref})
	_, cmd = m.applyControl(request)
	late := cmd()
	if err := m.workspace.reverseJourney(ref); err != nil {
		t.Fatal(err)
	}
	m.Update(late)
	response, _ = m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": request.ID}))
	if response.State != "superseded" {
		t.Fatal(response)
	}
	inspect, _ := m.applyControl(controlTestRequest(m, "journey.inspect", map[string]any{"ref": ref}))
	if _, ok := controlTestResult(t, inspect)["duration_s"]; ok {
		t.Fatal("stale estimate exposed")
	}
}

func TestControlOfflineFailureRetainsStopsAndCancelRetainsEdits(t *testing.T) {
	m := journeyViewer(t)
	response, _ := m.applyControl(controlTestRequest(m, "journey.create", map[string]any{"mode": "cycling", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}}}))
	if response.Error == nil || response.Error.Code != "routing_unavailable" || !response.Changed {
		t.Fatal(response)
	}
	ref := controlTestRef(t, response)
	j, _ := m.workspace.journey(ref)
	if len(j.Stops) != 2 {
		t.Fatal("failed routing discarded intent")
	}
	path, _ := filepath.Abs("../fixtures/example.gpx")
	request := controlTestRequest(m, "load", map[string]any{"path": path})
	_, cmd := m.applyControl(request)
	cancelled, _ := m.applyControl(controlTestRequest(m, "operation.cancel", map[string]any{"operation_id": request.ID}))
	if cancelled.Error != nil {
		t.Fatal(cancelled)
	}
	m.Update(cmd())
	status, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": request.ID}))
	if status.State != "cancelled" || len(m.workspace.documents) != 1 {
		t.Fatal(status)
	}
}

func TestControlCameraAndPlaybackGuardHumanNavigation(t *testing.T) {
	m := journeyViewer(t)
	request := controlTestRequest(m, "camera.set", map[string]any{"lat": 51.5, "lon": -0.1, "zoom": 12})
	response, _ := m.applyControl(request)
	if response.Error != nil || m.camera.Zoom != 12 {
		t.Fatal(response)
	}
	stale := controlTestRequest(m, "camera.set", map[string]any{"zoom": 10})
	before := m.destination()
	m.camera.Zoom = 13
	m.recordViewChange(before)
	response, _ = m.applyControl(stale)
	if response.Error == nil || response.Error.Code != "revision_conflict" {
		t.Fatal(response)
	}
	ref, err := m.workspace.open("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"playback.play", "playback.pause", "playback.seek"} {
		args := map[string]any{"ref": ref}
		if action == "playback.seek" {
			args["point_index"] = 1
		}
		response, _ = m.applyControl(controlTestRequest(m, action, args))
		if response.Error != nil {
			t.Fatal(response)
		}
	}
	if m.activePlayback().Playing || m.activePlayback().Index != 1 {
		t.Fatal("seek did not pause")
	}
}

func TestControlRetentionBoundsAndBadArguments(t *testing.T) {
	m := journeyViewer(t)
	for _, test := range []struct{ name, action, args string }{
		{name: "unknown", action: "camera.fit", args: `{"zoom":5}`},
		{name: "null", action: "visibility", args: `{"ref":{"document_id":1,"item_id":1},"visible":null}`},
		{name: "coordinates", action: "journey.create", args: `{"mode":"offline","stops":[{"lat":91,"lon":0},{"lat":0,"lon":0}]}`},
		{name: "missing coordinate", action: "journey.create", args: `{"mode":"offline","stops":[{"lat":0},{"lat":0,"lon":0}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := controlTestRequest(m, test.action, nil)
			request.Args = []byte(test.args)
			response, _ := m.applyControl(request)
			if response.Error == nil || response.Changed || len(m.workspace.documents) != 0 {
				t.Fatal(response)
			}
		})
	}
	request := controlTestRequest(m, "camera.fit", map[string]any{})
	response, _ := m.applyControl(request)
	if response.Error != nil {
		t.Fatal(response)
	}
	for len(m.control.operations) < operationCapacity {
		id := newSessionID()
		m.control.operations[id] = &controlOperation{created: time.Now(), response: controlResponse{State: "applied"}}
	}
	response, _ = m.applyControl(controlTestRequest(m, "camera.fit", map[string]any{}))
	if response.Error == nil || response.Error.Code != "busy" {
		t.Fatal(response)
	}
	for _, op := range m.control.operations {
		op.created = time.Now().Add(-operationRetention - time.Second)
	}
	response, _ = m.applyControl(controlTestRequest(m, "camera.fit", map[string]any{}))
	if response.Error != nil {
		t.Fatal(response)
	}
}

func TestControlRouteCannotApplyToReplacementWithReusedItemIDs(t *testing.T) {
	m := journeyViewer(t)
	ref := routedTestJourney(t, &m.workspace)
	request, err := m.workspace.beginJourneyRoute(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	replacement := Workspace{}
	sameRef := routedTestJourney(t, &replacement)
	if sameRef != ref {
		t.Fatal("fixture must reuse runtime item IDs")
	}
	current, err := replacement.beginJourneyRoute(t.Context(), sameRef)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.cancelJourneyRoutes()
	m.workspace.cancelJourneyRoutes()
	m.workspace = replacement
	old := journeyRouteResult{generation: request.generation, ref: ref, id: current.id, revision: current.revision}
	if m.workspace.acceptJourneyRoute(old) {
		t.Fatal("late result crossed workspace generation")
	}
}

func TestControlIdempotentCameraAndPauseReportNoChange(t *testing.T) {
	m := journeyViewer(t)
	request := controlTestRequest(m, "camera.set", map[string]any{"zoom": m.camera.Zoom})
	response, _ := m.applyControl(request)
	if response.Error != nil || response.Changed {
		t.Fatal(response)
	}
	ref, err := m.workspace.open("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	response, _ = m.applyControl(controlTestRequest(m, "playback.pause", map[string]any{"ref": ref}))
	if response.Error != nil || response.Changed {
		t.Fatal(response)
	}
}

func TestControlPanUsesTerminalCells(t *testing.T) {
	m := journeyViewer(t)
	m.camera.Zoom = 12
	expected := m.camera
	expected.pan(10, 12) // Five horizontal cells and three vertical cells.
	response, _ := m.applyControl(controlTestRequest(m, "camera.pan", map[string]any{"dx": 5, "dy": 3}))
	if response.Error != nil || m.camera.Center != expected.Center {
		t.Fatal(response, m.camera, expected)
	}
}

func TestControlPresentationSettingsAndAnimation(t *testing.T) {
	m := journeyViewer(t)
	run := func(action string, args map[string]any) controlResponse {
		t.Helper()
		response, _ := m.applyControl(controlTestRequest(m, action, args))
		if response.Error != nil {
			t.Fatal(response)
		}
		return response
	}
	listed := run("theme.list", map[string]any{})
	if len(controlTestResult(t, listed)["themes"].([]any)) < 6 {
		t.Fatal("built-in themes missing")
	}
	m.pushDialog(dialogThemes)
	selected := run("theme.set", map[string]any{"theme": "nord"})
	m.restoreThemePreview()
	if m.theme().ID != "nord" || !selected.Changed {
		t.Fatal("remote theme was undone by a stale preview")
	}
	if run("theme.set", map[string]any{"theme": "nord"}).Changed {
		t.Fatal("same theme should be a no-op")
	}
	bad, _ := m.applyControl(controlTestRequest(m, "theme.set", map[string]any{"theme": "missing"}))
	if bad.Error == nil || bad.Changed || m.theme().ID != "nord" {
		t.Fatal(bad)
	}
	m.closeDialog()
	before := m.camera
	run("camera.set", map[string]any{"zoom": 14, "lat": 51.5, "lon": -0.1, "animate": true})
	if !m.motion.Active || m.camera != before || m.destination().Zoom != 14 || !m.settings.NoAnimation {
		t.Fatal("override must animate without changing the session preference")
	}
	for range 300 {
		m.motion.advance(&m.camera, 1.0/60)
	}
	if m.motion.Active || m.camera.Zoom != 14 {
		t.Fatal("camera did not settle at requested destination")
	}
	run("camera.animation", map[string]any{"enabled": true})
	run("camera.set", map[string]any{"zoom": 10})
	if !m.motion.Active {
		t.Fatal("default did not follow enabled animation setting")
	}
	run("camera.animation", map[string]any{"enabled": false})
	if m.motion.Active || m.camera.Zoom != 10 {
		t.Fatal("disabling animation did not finish the transition")
	}
	run("camera.animation", map[string]any{"enabled": true})
	run("camera.set", map[string]any{"zoom": 8, "animate": false})
	if m.motion.Active || m.camera.Zoom != 8 || m.settings.NoAnimation {
		t.Fatal("explicit instant move changed the setting or animated")
	}
}

func TestControlPlaybackSpeed(t *testing.T) {
	m := journeyViewer(t)
	ref, err := m.workspace.open("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	for _, speed := range playbackSpeeds {
		response, _ := m.applyControl(controlTestRequest(m, "playback.speed", map[string]any{"ref": ref, "speed": speed}))
		if response.Error != nil || m.activePlayback().multiplier() != speed || m.activePlayback().Playing {
			t.Fatal(response)
		}
	}
	p := m.activePlayback()
	p.Playing = true
	m.lastTick = time.Now()
	response, _ := m.applyControl(controlTestRequest(m, "playback.speed", map[string]any{"ref": ref, "speed": 5}))
	if response.Error != nil || !p.Playing || p.multiplier() != 5 {
		t.Fatal("speed change paused playback", response)
	}
	before := m.workspace.liveRevision
	response, _ = m.applyControl(controlTestRequest(m, "playback.speed", map[string]any{"ref": ref, "speed": 7}))
	if response.Error == nil || response.Changed || p.multiplier() != 5 || m.workspace.liveRevision != before {
		t.Fatal("invalid speed changed playback", response)
	}
}
