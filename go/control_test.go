package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestControlRecoverySurvivesSaturation(t *testing.T) {
	for _, kind := range []string{"import", "route"} {
		t.Run(kind, func(t *testing.T) {
			m := journeyViewer(t)
			var target string
			var route *Journey
			for range pendingCapacity {
				if kind == "route" && target == "" {
					ref := routedTestJourney(t, &m.workspace)
					route, _ = m.workspace.journey(ref)
					_, cancel := context.WithCancel(t.Context())
					route.Busy = true
					route.cancel = cancel
					request := controlTestRequest(m, "journey.calculate", map[string]any{"ref": ref})
					target = request.ID
					m.control = &viewerControl{operations: map[string]*controlOperation{
						target: {request: request, created: time.Now(), ref: ref,
							routeID: route.requestID, routeRevision: route.revision, cancel: cancel,
							response: controlResponse{State: "pending"}},
					}}
					t.Cleanup(cancel)
					continue
				}
				path, _ := filepath.Abs("../fixtures/example.gpx")
				request := controlTestRequest(m, "load", map[string]any{"path": path})
				result, _ := m.applyControl(request)
				if result.State != "pending" {
					t.Fatal(result)
				}
				if target == "" {
					target = request.ID
				}
			}
			t.Cleanup(m.cancelControlOperations)
			// The worker budget must not reject synchronous settings commands.
			result, _ := m.applyControl(controlTestRequest(m, "theme.set", map[string]any{"theme": "nord"}))
			if result.Error != nil {
				t.Fatal("pending worker limit blocked synchronous edit", result)
			}
			for len(m.control.operations) < operationCapacity {
				m.control.operations[newSessionID()] = &controlOperation{
					created: time.Now(), response: controlResponse{State: "applied"},
				}
			}
			cancelRequest := controlTestRequest(m, "operation.cancel", map[string]any{"operation_id": target})
			result, _ = m.applyControl(cancelRequest)
			if result.Error != nil || m.control.operations[target].response.State != "cancelled" {
				t.Fatal("saturated operation table blocked cancellation", result)
			}
			if m.control.operations[target].response.Revision != m.workspace.liveRevision {
				t.Fatal("cancelled operation retained an old revision")
			}
			if route != nil && route.Busy {
				t.Fatal("route cancellation did not stop routing")
			}
			duplicate, _ := m.applyControl(cancelRequest)
			if !reflect.DeepEqual(result, duplicate) {
				t.Fatal("reserved cancellation lost idempotency")
			}
			result, _ = m.applyControl(controlTestRequest(m, "workspace.clear", map[string]any{"discard": true}))
			if result.Error != nil {
				t.Fatal("saturation blocked clear", result)
			}
			m.reconcileOperations()
			for _, op := range m.control.operations {
				if op.response.State == "pending" {
					t.Fatal("clear retained pending operations")
				}
			}
		})
	}
}

func TestControlPersistenceDirtinessUnavailable(t *testing.T) {
	m := journeyViewer(t)
	for _, action := range []string{"theme.set", "workspace.clear"} {
		args := map[string]any{"theme": "nord"}
		if action == "workspace.clear" {
			args = map[string]any{"discard": true}
		}
		result, _ := m.applyControl(controlTestRequest(m, action, args))
		if result.Error != nil {
			t.Fatal(result)
		}
		state := m.controlStatus()
		if _, exists := state["unsaved_changes"]; exists || state["persistence_dirty_available"] != false {
			t.Fatal("live revision must not imply persistence dirtiness", state)
		}
	}
}

func TestControlCameraTransitionOutcomes(t *testing.T) {
	m := journeyViewer(t)
	start := func(zoom float64) controlResponse {
		t.Helper()
		response, _ := m.applyControl(controlTestRequest(m, "camera.set", map[string]any{"zoom": zoom, "animate": true}))
		if response.Error != nil || response.State != "pending" || response.TransitionID == "" {
			t.Fatal(response)
		}
		return response
	}
	status := func(id string) controlResponse {
		t.Helper()
		response, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": id}))
		return response
	}
	first := start(13)
	second := start(10)
	if result := status(first.OperationID); result.State != "superseded" || result.TransitionID != first.TransitionID {
		t.Fatal("replacement was not attributed to original transition", result)
	}
	for range 300 {
		m.motion.advance(&m.camera, 1.0/60)
	}
	if result := status(second.OperationID); result.State != "applied" || result.TransitionID != second.TransitionID {
		t.Fatal("settled transition did not complete", result)
	}
	third := start(15)
	m.panCamera(20, 0)
	if result := status(third.OperationID); result.State != "superseded" {
		t.Fatal("human navigation did not supersede transition", result)
	}
	fourth := start(9)
	_, _ = m.applyControl(controlTestRequest(m, "operation.cancel", map[string]any{"operation_id": fourth.OperationID}))
	if result := status(fourth.OperationID); result.State != "cancelled" || m.motion.Active || result.Revision != m.workspace.liveRevision {
		t.Fatal("cancel did not stop transition", result)
	}
}

func TestControlSchemasMatchArgumentValidation(t *testing.T) {
	for _, test := range []struct {
		action, args string
		valid        bool
	}{
		{"select", `{"ref":{"document_id":1,"item_id":0}}`, false},
		{"visibility", `{"ref":{"document_id":1,"item_id":0},"visible":false}`, true},
		{"camera.set", `{"lat":90,"lon":0}`, false},
		{"camera.set", `{"lat":85.05112878,"lon":0}`, true},
		{"camera.set", `{"lat":20}`, false},
		{"camera.set", `{"zoom":21}`, false},
		{"camera.set", `{"animate":null}`, false},
		{"playback.speed", `{"ref":{"document_id":1,"item_id":1},"speed":7}`, false},
	} {
		t.Run(test.action+test.args, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(test.args), &value); err != nil {
				t.Fatal(err)
			}
			advertised := controlSpecs[test.action].resolved.Validate(value)
			_, actual := decodeControlArgs(test.action, []byte(test.args))
			if (advertised == nil) != test.valid || (actual == nil) != test.valid {
				t.Fatalf("schema=%v handler=%v valid=%v", advertised, actual, test.valid)
			}
		})
	}
}

func TestCreateJourneyPreservesActiveRecording(t *testing.T) {
	m := journeyViewer(t)
	ref, err := m.workspace.open("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m.workspace.setPlaybackPlaying(true)
	if err := m.workspace.setPlaybackSpeed(2); err != nil {
		t.Fatal(err)
	}
	selection := m.workspace.selection
	response, _ := m.applyControl(controlTestRequest(m, "journey.create", map[string]any{
		"mode": "offline", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}},
	}))
	if response.Error != nil || m.workspace.selection != selection || m.workspace.selection.ref != ref {
		t.Fatal("journey creation replaced active recording", response)
	}
	if !m.activePlayback().Playing || m.activePlayback().multiplier() != 20 {
		t.Fatal("journey creation altered playback")
	}
}

func TestSettledTransitionIsNotSupersededByLaterHumanMove(t *testing.T) {
	m := journeyViewer(t)
	first, _ := m.applyControl(controlTestRequest(m, "camera.set", map[string]any{"zoom": 12, "animate": true}))
	// Disabling animation completes it immediately, even before the next pulse.
	m.setCameraAnimation(false)
	m.panCamera(10, 0)
	status, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": first.OperationID}))
	if status.State != "applied" {
		t.Fatal("a completed transition was retrospectively superseded", status)
	}
}

func TestRecoveryAllowanceRemainsBoundedAndIndependent(t *testing.T) {
	m := journeyViewer(t)
	for range recoveryCapacity {
		response, _ := m.applyControl(controlTestRequest(m, "workspace.clear", map[string]any{"discard": true}))
		if response.Error != nil {
			t.Fatal(response)
		}
	}
	generation := m.workspace.liveIdentity()
	response, _ := m.applyControl(controlTestRequest(m, "workspace.clear", map[string]any{"discard": true}))
	if response.Error == nil || response.Error.Code != "busy" || m.workspace.liveIdentity() != generation {
		t.Fatal("recovery budget was unbounded or rejected after mutating", response)
	}
	response, _ = m.applyControl(controlTestRequest(m, "camera.set", map[string]any{"zoom": 12, "animate": false}))
	if response.Error != nil {
		t.Fatal("recovery budget consumed ordinary capacity", response)
	}
}

func TestRepeatedSelectionReportsInterruptedCamera(t *testing.T) {
	for _, action := range []string{"select", "load"} {
		t.Run(action, func(t *testing.T) {
			m := journeyViewer(t)
			ref, err := m.workspace.open("../fixtures/example.gpx")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = m.applyControl(controlTestRequest(m, "camera.set", map[string]any{"zoom": 15, "animate": true}))
			before := m.workspace.liveRevision
			args := map[string]any{"ref": ref}
			if action == "load" {
				path, _ := filepath.Abs("../fixtures/example.gpx")
				args = map[string]any{"path": path}
			}
			response, _ := m.applyControl(controlTestRequest(m, action, args))
			if response.Error != nil || !response.Changed || m.workspace.liveRevision == before {
				t.Fatal("interrupted destination was not reported", response)
			}
		})
	}
}

func TestUnavailableCalculationAdvancesRevision(t *testing.T) {
	m := journeyViewer(t)
	ref := routedTestJourney(t, &m.workspace)
	before := m.workspace.liveRevision
	response, _ := m.applyControl(controlTestRequest(m, "journey.calculate", map[string]any{"ref": ref}))
	if response.Error == nil || response.Error.Code != "routing_unavailable" || !response.Changed || response.Revision <= before {
		t.Fatal("routing error changed state without advancing revision", response)
	}
	repeated, _ := m.applyControl(controlTestRequest(m, "journey.calculate", map[string]any{"ref": ref}))
	if repeated.Changed || repeated.Revision != response.Revision {
		t.Fatal("unchanged routing error advanced revision", repeated)
	}
}

func TestSupersededOperationReportsCurrentIdentity(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			m := journeyViewer(t)
			pending, _ := m.applyControl(controlTestRequest(m, "camera.set", map[string]any{"zoom": 15, "animate": true}))
			if replace {
				m.workspace.clear()
			} else {
				m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
			}
			result, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": pending.OperationID}))
			if result.State != "superseded" || result.Generation != m.workspace.liveIdentity() || result.Revision != m.workspace.liveRevision {
				t.Fatal("superseded operation returned stale identity", result)
			}
		})
	}
}

func TestPlaybackSwitchCleansViewerSelection(t *testing.T) {
	for _, action := range []string{"playback.play", "playback.pause", "playback.seek", "playback.speed"} {
		t.Run(action, func(t *testing.T) {
			m := journeyViewer(t)
			first := openWorkspaceFile(t, &m.workspace, "../fixtures/example.gpx")
			second := openWorkspaceFile(t, &m.workspace, "../fixtures/country-ride.gpx")
			if err := m.workspace.selectItem(first); err != nil {
				t.Fatal(err)
			}
			m.following, m.inspected = true, &XY{1, 2}
			args := map[string]any{"ref": second}
			if action == "playback.seek" {
				args["point_index"] = 1
			}
			if action == "playback.speed" {
				args["speed"] = 20
			}
			response, _ := m.applyControl(controlTestRequest(m, action, args))
			if response.Error != nil || m.following || m.inspected != nil {
				t.Fatal("playback retained previous selection state", response)
			}
			m.following, m.inspected = true, &XY{1, 2}
			response, _ = m.applyControl(controlTestRequest(m, action, args))
			if response.Error != nil || !m.following || m.inspected == nil {
				t.Fatal("same-recording command cleared selection state", response)
			}
		})
	}
}

func TestControlTextPreservesUnicodeAndRuneBoundaries(t *testing.T) {
	value := "/tmp/東京/café.json"
	if got := controlText(value, 4096); got != value {
		t.Fatal(got)
	}
	if got := controlText("é東京", 4); got != "é..." || !utf8.ValidString(got) {
		t.Fatal(got)
	}
	if got := controlText("a\x1bb", 20); strings.ContainsRune(got, '\x1b') {
		t.Fatal("ANSI escape retained")
	}
}

func TestHideAndRemoveRunSelectionCleanup(t *testing.T) {
	for _, action := range []string{"visibility", "remove"} {
		for _, document := range []bool{false, true} {
			for _, active := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/document=%t/active=%t", action, document, active), func(t *testing.T) {
					m := journeyViewer(t)
					target := openWorkspaceFile(t, &m.workspace, "../fixtures/example.gpx")
					other := openWorkspaceFile(t, &m.workspace, "../fixtures/country-ride.gpx")
					selected := other
					if active {
						selected = target
					}
					if err := m.workspace.selectItem(selected); err != nil {
						t.Fatal(err)
					}
					_, _ = m.applyControl(controlTestRequest(m, "camera.set", map[string]any{"zoom": 15, "animate": true}))
					m.following, m.inspected = true, &XY{1, 2}
					if document {
						target.ItemID = 0
					}
					args := map[string]any{"ref": target}
					if action == "visibility" {
						args["visible"] = false
					}
					response, _ := m.applyControl(controlTestRequest(m, action, args))
					if response.Error != nil {
						t.Fatal(response)
					}
					if active {
						if m.workspace.selection != nil || m.following || m.inspected != nil || m.motion.Active {
							t.Fatal("removed selection retained viewer state")
						}
					} else if m.workspace.selection == nil || !m.following || m.inspected == nil || !m.motion.Active {
						t.Fatal("unrelated item edit cleared viewer selection state")
					}
				})
			}
		}
	}
}

func TestJourneyShowCleansSelectionBeforeAnimating(t *testing.T) {
	m := journeyViewer(t)
	ref := routedTestJourney(t, &m.workspace)
	if err := m.workspace.setJourneyMode(ref, journeyManual); err != nil {
		t.Fatal(err)
	}
	openWorkspaceFile(t, &m.workspace, "../fixtures/example.gpx")
	m.following, m.inspected = true, &XY{1, 2}
	response, _ := m.applyControl(controlTestRequest(m, "journey.show", map[string]any{"ref": ref, "animate": true}))
	if response.Error != nil || m.inspected != nil || m.following || !m.motion.Active || m.workspace.selection.ref != ref {
		t.Fatal("journey show did not clean selection before fitting", response)
	}
}

func TestDefaultJourneyModeRejectsStaleControlMutation(t *testing.T) {
	m := journeyViewer(t)
	m.planner.ref = m.workspace.newJourney()
	m.directions = newDirectionsClient("token")
	request := controlTestRequest(m, "journey.reverse", map[string]any{"ref": m.planner.ref})
	before := m.workspace.liveRevision
	if cmd := m.calculateJourneyIfRouted(); cmd != nil {
		t.Fatal("incomplete draft requested routing")
	}
	j, _ := m.workspace.journey(m.planner.ref)
	if j.Mode != journeyDriving || m.workspace.liveRevision <= before {
		t.Fatal("default mode did not record live edit")
	}
	response, _ := m.applyControl(request)
	if response.Error == nil || response.Error.Code != "revision_conflict" {
		t.Fatal("stale edit accepted after default mode change", response)
	}
	before = m.workspace.liveRevision
	m.calculateJourneyIfRouted()
	if m.workspace.liveRevision != before {
		t.Fatal("unchanged default mode advanced revision")
	}
}

func TestCameraNoOpRecordsStoppingFollow(t *testing.T) {
	for _, action := range []string{"camera.set", "camera.pan"} {
		t.Run(action, func(t *testing.T) {
			m := journeyViewer(t)
			args := map[string]any{"zoom": m.camera.Zoom, "animate": false}
			if action == "camera.pan" {
				args = map[string]any{"dx": 0, "dy": 0, "animate": false}
			}
			m.following = true
			before := m.workspace.liveRevision
			response, _ := m.applyControl(controlTestRequest(m, action, args))
			if response.Error != nil || !response.Changed || m.following || response.Revision <= before {
				t.Fatal("stopping follow was not recorded", response)
			}
			repeated, _ := m.applyControl(controlTestRequest(m, action, args))
			if repeated.Error != nil || repeated.Changed || repeated.Revision != response.Revision {
				t.Fatal("repeated camera command recorded unchanged state", repeated)
			}
		})
	}
}

func TestChoosingDefaultModeRecordsPreference(t *testing.T) {
	m := journeyViewer(t)
	ref := m.workspace.newJourney()
	before := m.workspace.liveRevision
	response, _ := m.applyControl(controlTestRequest(m, "journey.mode", map[string]any{"ref": ref, "mode": "offline"}))
	if response.Error != nil || !response.Changed || response.Revision <= before {
		t.Fatal("explicit mode preference not recorded", response)
	}
	repeated, _ := m.applyControl(controlTestRequest(m, "journey.mode", map[string]any{"ref": ref, "mode": "offline"}))
	if repeated.Error != nil || repeated.Changed || repeated.Revision != response.Revision {
		t.Fatal("unchanged mode preference recorded twice", repeated)
	}
	m.directions = newDirectionsClient("token")
	j, _ := m.workspace.journey(ref)
	if m.defaultJourneyMode(j) || j.Mode != journeyManual {
		t.Fatal("credential discovery replaced explicit Offline")
	}
}

func TestRouteCancellationRejectsStaleMutation(t *testing.T) {
	m := journeyViewer(t)
	ref := routedTestJourney(t, &m.workspace)
	if _, err := m.workspace.beginJourneyRoute(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	request := controlTestRequest(m, "journey.reverse", map[string]any{"ref": ref})
	before := m.workspace.liveRevision
	m.workspace.cancelJourneyRoutes()
	j, _ := m.workspace.journey(ref)
	if j.Busy || m.workspace.liveRevision <= before {
		t.Fatal("route cancellation did not record edit")
	}
	response, _ := m.applyControl(request)
	if response.Error == nil || response.Error.Code != "revision_conflict" {
		t.Fatal("stale edit accepted after cancellation", response)
	}
	before = m.workspace.liveRevision
	m.workspace.cancelJourneyRoutes()
	if m.workspace.liveRevision != before {
		t.Fatal("idle cancellation recorded edit")
	}
}
