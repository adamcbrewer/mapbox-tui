package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func routingTestViewer(t *testing.T) (*viewer, *int) {
	t.Helper()
	m := journeyViewer(t)
	m.planner.ref = routedTestJourney(t, &m.workspace)
	m.directions = newDirectionsClient("token")
	requests := new(int)
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		*requests++
		return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 12345, 3661)), nil
	})
	t.Cleanup(m.workspace.cancelJourneyRoutes)
	m.openJourney()
	return m, requests
}

func TestJourneyRoutingOnlyRequestsCommittedChanges(t *testing.T) {
	m, requests := routingTestViewer(t)
	j, _ := m.workspace.journey(m.planner.ref)
	m.Update(m.calculateJourneyRoute()())
	if !j.routeCurrent() || *requests != 1 || !strings.Contains(m.journeyCredits(), directionsCredits) {
		t.Fatal("route/attribution not displayed")
	}
	if !strings.Contains(strings.Join(m.journeyDetails(), "\n"), "Estimated travel time\n1 h 2 min") {
		t.Fatal("estimated duration not distinguished from recorded time")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if cmd == nil || j.routeCurrent() {
		t.Fatal("reversal did not invalidate estimates/request replacement")
	}
	late := cmd()
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("undo did not recalculate")
	}
	m.Update(late)
	if j.routeCurrent() {
		t.Fatal("superseded reversal response won after undo")
	}
	m.Update(cmd())
	before := *requests
	m.closeDialog()
	m.Update(tea.KeyPressMsg{Code: 'l'})
	m.openJourney()
	m.editJourneyLocation(1)
	m.Update(tea.PasteMsg{Content: " changed"})
	if *requests != before || !j.routeCurrent() {
		t.Fatal("typing, panning or reopening requested/invalidated a route")
	}
	m.ui.search.SetValue("51.56, -0.16")
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || j.routeCurrent() || j.Stops[1].Location.Lat != 51.56 {
		t.Fatal("committed coordinate edit did not request a new route")
	}
	m.Update(cmd())
	if !j.routeCurrent() || *requests != before+1 {
		t.Fatal("committed edit did not resolve exactly once")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if cmd != nil || j.Error != "" || j.routeCurrent() {
		t.Fatal("empty waypoint did not wait for its location without requesting HTTP")
	}
}

func TestJourneyConnectedDefaultsToDrivingAndRoutesAutomatically(t *testing.T) {
	m := journeyViewer(t)
	m.directions.token = "token"
	var requests int
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if !strings.Contains(r.URL.Path, "/driving/") {
			t.Fatal("default journey did not use driving")
		}
		return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 12345, 3661)), nil
	})
	t.Cleanup(m.workspace.cancelJourneyRoutes)
	m.openJourney()
	j, _ := m.workspace.journey(m.planner.ref)
	_, content, _ := m.journeyDialogContent(64, 17)
	if j.Mode != journeyDriving || !strings.Contains(ansi.Strip(content), "Travel mode: Driving (ctrl+b)") {
		t.Fatal("default travel mode was not visible in the planner")
	}
	for _, command := range m.commands() {
		if command.action == "journey" && command.title != "Journey Planner" {
			t.Fatal("command palette kept the old planner name")
		}
	}
	m.editJourneyLocation(0)
	m.ui.search.SetValue("51.5, -0.1")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || j.Error != "" || requests != 0 {
		t.Fatal("incomplete journey requested a route or showed a premature warning")
	}
	m.editJourneyLocation(1)
	m.ui.search.SetValue("51.6, -0.2")
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !j.Busy {
		t.Fatal("completed journey required a separate calculate action")
	}
	result := cmd().(journeyRouteResult)
	camera := m.camera
	show := showJourneyFromMenu(m)
	if show != nil || m.ui.dialog != dialogJourney || m.camera != camera || requests != 1 {
		t.Fatal("show action fitted straight lines or duplicated the pending request")
	}
	m.Update(result)
	expected := journeyView(j, result.route.Track, m.camera)
	if m.ui.dialog != dialogNone || !j.routeCurrent() || m.camera != expected {
		t.Fatal("show action did not fit the resolved route on completion")
	}
	m.openJourney()
	_, content, _ = m.journeyDialogContent(64, 17)
	if !strings.Contains(ansi.Strip(content), "Recalculate route (ctrl+d)") {
		t.Fatal("recalculate action kept the old name")
	}
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	m.editJourneyLocation(1)
	m.ui.search.SetValue("51.55, -0.15")
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("completed intermediate waypoint did not recalculate")
	}
	m.Update(cmd())
	if !j.routeCurrent() || len(j.Route.Snapped) != 3 || requests != 2 {
		t.Fatal("waypoint route did not complete automatically")
	}
	showJourneyFromMenu(m)
	if requests != 2 || m.ui.dialog != dialogNone {
		t.Fatal("showing a resolved route requested it again")
	}
}

func TestJourneyDrivingDefaultAfterCredentialDiscoveryRespectsManualChoice(t *testing.T) {
	for _, explicitManual := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "explicit manual"}[explicitManual], func(t *testing.T) {
			m := journeyViewer(t)
			m.args.Provider, m.args.ProviderSet = "none", true
			m.openJourney()
			enterJourneyCoordinate(m, 0, "51.5, -0.1")
			enterJourneyCoordinate(m, 1, "51.6, -0.2")
			if explicitManual {
				if err := m.workspace.setJourneyMode(m.planner.ref, journeyManual); err != nil {
					t.Fatal(err)
				}
			}
			cmd := m.acceptCredentials(credentialResult{credential: renderingCredential{token: "token", source: "login"}})
			j, _ := m.workspace.journey(m.planner.ref)
			if explicitManual {
				if j.Mode != journeyManual || cmd != nil || j.Busy {
					t.Fatal("credential discovery overrode explicit Manual")
				}
				return
			}
			if j.Mode != journeyDriving || cmd == nil || !j.Busy {
				t.Fatal("late credential discovery left the default journey as straight lines")
			}
			m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 100, 100)), nil
			})
			m.Update(cmd())
			if !j.routeCurrent() {
				t.Fatal("default driving route did not resolve after credential discovery")
			}
		})
	}
}

func TestJourneyShowFailureOffersFeedbackInsteadOfStraightLines(t *testing.T) {
	m, requests := routingTestViewer(t)
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		*requests++
		return locationResponse(`{"code":"NoRoute"}`), nil
	})
	camera := m.camera
	cmd := showJourneyFromMenu(m)
	if cmd == nil || m.ui.dialog != dialogJourney {
		t.Fatal("Show did not calculate a missing route")
	}
	m.Update(cmd())
	if m.ui.dialog != dialogJourney || m.camera != camera || !strings.Contains(m.ui.notice, "no Walking route") {
		t.Fatal("failed Show silently presented straight lines")
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, content, _ := m.journeyDialogContent(64, 17)
	if !strings.Contains(ansi.Strip(content), "Travel mode: Manual") {
		t.Fatal("explicit manual choice was not reflected in the main menu")
	}
	showJourneyFromMenu(m)
	if m.ui.dialog != dialogNone || *requests != 1 {
		t.Fatal("explicit Manual did not provide a usable offline Show action")
	}
}

func TestJourneyRouteResultUpdatesNestedPlannerNotice(t *testing.T) {
	for _, nestedMode := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			name := fmt.Sprintf("mode=%v/failure=%v", nestedMode, fail)
			t.Run(name, func(t *testing.T) {
				m, _ := routingTestViewer(t)
				if fail {
					m.directions.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
						return locationResponse(`{"code":"NoRoute"}`), nil
					})
				}
				route := showJourneyFromMenu(m)
				if route == nil || !strings.Contains(m.ui.notice, "Calculating") {
					t.Fatal("Show did not start routing with feedback")
				}
				if nestedMode {
					m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
				}
				m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
				m.Update(route())
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				if nestedMode {
					m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				}
				j, _ := m.workspace.journey(m.planner.ref)
				if m.ui.dialog != dialogJourney || j.Busy || m.ui.notice != j.Error {
					t.Fatal("restored planner displayed obsolete route feedback", m.ui.notice, j.Error)
				}
				if fail && m.ui.notice == "" {
					t.Fatal("restored planner lost the route failure")
				}
			})
		}
	}
}

func TestJourneyFailedRecalculationUsesManualPreview(t *testing.T) {
	m, requests := routingTestViewer(t)
	m.Update(m.calculateJourneyRoute()())
	j, _ := m.workspace.journey(m.planner.ref)
	previous, stops := j.Route, slices.Clone(j.Stops)
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		*requests++
		return locationResponse(`{"code":"NoRoute"}`), nil
	})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m.Update(cmd())
	if *requests != 2 || j.routeCurrent() || j.Route != previous || !slices.Equal(j.Stops, stops) {
		t.Fatal("failed retry retained a current route or changed the previous snapshot/stops")
	}
	if m.activeTrack() == previous.Track || m.activeTrack().Points[0].Lon != stops[0].Location.Lon {
		t.Fatal("failed retry did not restore manual geometry")
	}
	details := strings.Join(m.journeyDetails(), "\n")
	if strings.Contains(details, "Estimated travel time") || strings.Contains(m.journeyCredits(), directionsCredits) {
		t.Fatal("failed retry still displayed current estimates or routing credits")
	}
	if !strings.Contains(m.ui.notice, "no Walking route") {
		t.Fatal("failed retry lost its feedback")
	}
	show := showJourneyFromMenu(m)
	if show == nil || m.ui.dialog != dialogJourney {
		t.Fatal("Show accepted the failed route instead of requesting current directions")
	}
}

func TestJourneyShowWaitsForCurrentRouteRecalculation(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail bool
	}{
		{name: "success"},
		{name: "failure", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, requests := routingTestViewer(t)
			m.Update(m.calculateJourneyRoute()())
			j, _ := m.workspace.journey(m.planner.ref)
			m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				*requests++
				if tc.fail {
					return locationResponse(`{"code":"NoRoute"}`), nil
				}
				locations := directionsRequestLocations(t, r)
				locations[1].Lon = -.5
				return locationResponse(directionsTestResponse(locations, 20000, 7200)), nil
			})
			_, retry := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
			result := retry()
			camera := m.camera
			show := showJourneyFromMenu(m)
			if show != nil || m.ui.dialog != dialogJourney || m.camera != camera {
				t.Fatal("Show closed the planner, fitted the old route or duplicated the pending request")
			}
			m.Update(result)
			if *requests != 2 {
				t.Fatal("Show requested the route again")
			}
			if tc.fail {
				if m.ui.dialog != dialogJourney || !strings.Contains(m.ui.notice, "no Walking route") {
					t.Fatal("failed recalculation did not keep the planner open with feedback")
				}
				return
			}
			if m.ui.dialog != dialogNone || m.camera != journeyView(j, j.Route.Track, m.camera) {
				t.Fatal("Show did not fit the replacement route after recalculation")
			}
		})
	}
}

func TestJourneyUnchangedLocationAndModeKeepResolvedRoute(t *testing.T) {
	m, requests := routingTestViewer(t)
	m.Update(m.calculateJourneyRoute()())
	j, _ := m.workspace.journey(m.planner.ref)
	previous := j.Route
	m.editJourneyLocation(1)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || *requests != 1 || !j.routeCurrent() || j.Route != previous || m.activeTrack() != previous.Track {
		t.Fatal("accepting unchanged coordinates invalidated or recalculated the route")
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || *requests != 1 || !j.routeCurrent() || j.Route != previous {
		t.Fatal("choosing the current travel mode requested the route again")
	}
}

func TestJourneyFooterDeduplicatesBasemapAndRoutingCredits(t *testing.T) {
	m, _ := routingTestViewer(t)
	m.Update(m.calculateJourneyRoute()())
	m.provider = providerForCredential("mapbox", renderingCredential{token: "token"})
	m.configureSources()
	m.sources.states[0].snapshot.Credits = providerCredits("mapbox")
	m.rebuild()
	lines := strings.Split(ansi.Strip(m.content), "\n")
	footer := lines[len(lines)-1]
	for _, credit := range []string{"© Mapbox", "© OpenStreetMap", "Improve this map"} {
		if strings.Count(footer, credit) != 1 {
			t.Fatalf("footer lost or repeated %q: %q", credit, footer)
		}
	}
}

func TestJourneyPendingShowDoesNotMoveCameraAfterDismissalOrEdit(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "dismissed", true: "edited"}[edit], func(t *testing.T) {
			m, _ := routingTestViewer(t)
			cmd := showJourneyFromMenu(m)
			late := cmd()
			if edit {
				m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
			} else {
				m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			}
			camera := m.camera
			m.Update(late)
			if m.camera != camera || m.planner.showRevision != nil {
				t.Fatal("obsolete Show action moved the camera")
			}
		})
	}
}

func TestJourneyRoutingModeSelectionAndOfflineFallback(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	enterJourneyCoordinate(m, 0, "51.5, -0.1")
	enterJourneyCoordinate(m, 1, "51.6, -0.2")
	j, _ := m.workspace.journey(m.planner.ref)
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || j.Mode != journeyManual || !strings.Contains(m.ui.notice, "not connected") {
		t.Fatal("routing silently enabled without credentials")
	}
	m.directions.token = "token"
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 100, 100)), nil
	})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || j.Mode != journeyWalking || m.ui.dialog != dialogJourney {
		t.Fatal("choosing walking did not explicitly enable routing")
	}
	late := cmd()
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(late)
	if cmd != nil || j.Mode != journeyManual || j.Route != nil || m.activeTrack().Points[0].Lon != -.1 {
		t.Fatal("manual fallback requested work or accepted a late routed result")
	}
}

func TestJourneyNestedFeedbackReturnsThroughTravelModeAndMenu(t *testing.T) {
	m := journeyViewer(t)
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 18})
	m.openDialog(dialogCommands)
	m.ui.search.SetValue("journey")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	row, camera := m.planner.row, m.camera
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.PasteMsg{Content: "51.6, -0.2"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	j, _ := m.workspace.journey(m.planner.ref)
	if m.ui.dialog != dialogJourney || m.planner.row != row || m.camera != camera || j.Stops[1].Location != nil {
		t.Fatal("cancelled location changed the plan, selection or compact panel layout")
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	notice := m.ui.notice
	if notice == "" {
		t.Fatal("offline travel mode did not show feedback")
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogJourneyRouting || m.ui.selected != 1 || m.ui.notice != notice {
		t.Fatal("feedback skipped or reset its travel-mode parent")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogJourneyRouting || m.ui.selected != len(journeyModes) {
		t.Fatal("feedback menu entry did not retain its parent selection")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogJourney || m.planner.row != row {
		t.Fatal("travel mode did not return to the selected planner row")
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogJourney {
		t.Fatal("direct planner feedback acquired a travel-mode parent")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogCommands || m.ui.search.Value() != "journey" || !m.ui.search.Focused() {
		t.Fatal("planner did not restore the menu filter after editing a location")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.ui.dialog != dialogNone {
		t.Fatal("nested planner chain did not end at the map")
	}
}

func TestJourneyRoutingSearchSelectionCalculatesAfterRetrieval(t *testing.T) {
	m, requests := routingTestViewer(t)
	m.places.token = "token"
	m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/suggest") {
			return locationResponse(`{"suggestions":[{"mapbox_id":"station","name":"Station"}]}`), nil
		}
		return locationResponse(`{"attribution":"Search credits","features":[{"geometry":{"coordinates":[-0.16,51.56]}}]}`), nil
	})
	m.editJourneyLocation(1)
	m.ui.search.SetValue("Station")
	m.schedulePlaceSearch()
	m.Update(m.requestPlaceSearch(m.planner.searchID)())
	if *requests != 0 {
		t.Fatal("autocomplete made a routing request")
	}
	_, retrieve := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, calculate := m.Update(retrieve())
	j, _ := m.workspace.journey(m.planner.ref)
	if calculate == nil || j.Stops[1].Location.Name != "Station" || *requests != 0 {
		t.Fatal("selected search result did not commit intent and schedule routing")
	}
	m.Update(calculate())
	if *requests != 1 || !j.routeCurrent() || !strings.Contains(m.journeyCredits(), "Search credits") {
		t.Fatal("retrieved stop did not resolve once with both providers' credits")
	}
}

func TestJourneyRoutingFeedbackAndLayouts(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			m, _ := routingTestViewer(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.settings.ASCII = ascii
			m.Update(m.calculateJourneyRoute()())
			m.closeDialog()
			m.rebuild()
			text := ansi.Strip(m.content)
			if size[0] >= 70 && !strings.Contains(text, "1 h 2 min") {
				t.Fatalf("resolved route estimate missing %v, ASCII %v: %q", size, ascii, text)
			}
			if lipgloss.Height(m.content) > size[1] || lipgloss.Width(m.content) > size[0] {
				t.Fatal("routed map overflowed terminal", size, ascii)
			}
			m.openJourney()
			j, _ := m.workspace.journey(m.planner.ref)
			for len(j.Stops) < 26 {
				if err := m.workspace.addJourneyWaypoint(m.planner.ref); err != nil {
					t.Fatal(err)
				}
			}
			before := slices.Clone(j.Stops)
			if cmd := m.calculateJourneyRoute(); cmd != nil || !strings.Contains(j.Error, "26") {
				t.Fatal("over-limit plan made a request or omitted reason")
			}
			if !slices.Equal(before, j.Stops) || !strings.Contains(journeyRoutingStatus(j, m.activeTrack()), "stale") {
				t.Fatal("limit error truncated stops or reused old estimates")
			}
			m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
			m.journeyRoutingDialogContent(22, 4)
			page := m.ui.page.GetContent()
			if !strings.Contains(page, j.Error) || !strings.Contains(page, "remove stops or choose Manual") {
				t.Fatal("full error and cause not available in scrollable feedback")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			if !strings.Contains(ansi.Strip(m.ui.page.View()), "session only") {
				t.Fatal("End did not reach the bottom of feedback")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
			if !strings.Contains(ansi.Strip(m.ui.page.View()), "stale") {
				t.Fatal("Home did not return to routing status")
			}
			for _, dialog := range []dialogKind{dialogJourney, dialogJourneyRouting, dialogJourneyRoutingHelp} {
				m.openDialog(dialog)
				m.rebuild()
				if lipgloss.Height(m.content) > size[1] || lipgloss.Width(m.content) > size[0] {
					t.Fatal("routing dialog overflowed terminal", dialog, size, ascii)
				}
				if ascii && strings.ContainsFunc(ansi.Strip(m.content), func(r rune) bool { return r >= 0x2800 && r <= 0x28ff }) {
					t.Fatal("ASCII routing view used Braille")
				}
			}
		}
	}
}

func TestJourneyRouteCompletionPreservesSelectedImportAndAuthReplacementCancels(t *testing.T) {
	m, _ := routingTestViewer(t)
	cmd := m.calculateJourneyRoute()
	late := cmd()
	ref, err := m.workspace.open("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m.activePlayback().Playing = true
	playback := m.activePlayback()
	m.Update(late)
	if m.workspace.selection.ref != ref || m.activePlayback() != playback || !playback.Playing {
		t.Fatal("routing completion interrupted a different activity")
	}
	request, err := m.workspace.beginJourneyRoute(m.ctx, m.planner.ref)
	if err != nil {
		t.Fatal(err)
	}
	result := request.run(m.directions)
	m.args.Provider, m.args.ProviderSet = "none", true
	m.acceptCredentials(credentialResult{credential: renderingCredential{token: "new-token", source: "environment"}})
	if request.ctx.Err() == nil || m.directions.token != "new-token" {
		t.Fatal("credential replacement missed routing cancellation/client")
	}
	m.Update(result)
	j, _ := m.workspace.journey(m.planner.ref)
	if j.routeCurrent() || !strings.Contains(j.Error, "connection changed") || !playback.Playing {
		t.Fatal("old credential route was accepted or import playback was changed")
	}
}
