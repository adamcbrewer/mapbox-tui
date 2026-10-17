package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

func journeyViewer(t *testing.T) *viewer {
	t.Helper()
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	c, err := viewport(nil, Args{})
	if err != nil {
		t.Fatal(err)
	}
	m := &viewer{
		ctx: t.Context(), provider: newProvider("none"), camera: c,
		width: 120, height: 40, args: Args{NoAnimation: true},
	}
	if err := m.initUI(); err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	return m
}

func enterJourneyCoordinate(m *viewer, index int, text string) {
	m.editJourneyLocation(index)
	m.ui.search.SetValue(text)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func showJourneyFromMenu(m *viewer) tea.Cmd {
	return m.showJourneyOnMap()
}

func TestJourneyViewerManualEditReverseAndReopen(t *testing.T) {
	m := journeyViewer(t)
	var requests int
	m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		t.Fatal("manual workflow made a location request")
		return nil, nil
	})
	m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.ui.dialog != dialogNone {
		t.Fatal("removed Journey Planner shortcut still opened a dialog")
	}
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if m.ui.dialog != dialogJourney || requests != 0 {
		t.Fatal("planner did not open directly without a location request")
	}
	enterJourneyCoordinate(m, 0, "51.5, -0.1")
	enterJourneyCoordinate(m, 1, "51.6, -0.2")
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	enterJourneyCoordinate(m, 1, "51.55, -0.15")
	j, _ := m.workspace.journey(m.planner.ref)
	before := slices.Clone(j.Stops)
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	for i, stop := range j.Stops {
		if stop.ID != before[len(before)-1-i].ID {
			t.Fatal("viewer did not reverse the complete journey")
		}
	}
	m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if !slices.Equal(j.Stops, before) {
		t.Fatal("viewer undo did not restore reversal")
	}
	camera := m.camera
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if cmd != nil || m.ui.dialog != dialogJourney || m.camera != camera {
		t.Fatal("removed Show shortcut still ran the action")
	}
	_, content, _ := m.journeyDialogContent(70, 17)
	if strings.Contains(content, "Show journey on map") || strings.Contains(content, "Add waypoint (ctrl+n)") {
		t.Fatal("redundant journey action remains in the menu")
	}
	showJourneyFromMenu(m)
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.content), "Offline") || m.activePlayback() != nil || m.profileHeight != 0 {
		t.Fatal("manual journey became a recorded activity or elevation profile")
	}
	ref := m.planner.ref
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if m.ui.dialog != dialogJourney || m.planner.ref != ref || requests != 0 {
		t.Fatal("reopening created a new journey or made a location request")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.ui.dialog != dialogNone || m.planner.ref != ref || requests != 0 {
		t.Fatal("Escape did not close the planner without changing the journey")
	}
}

func TestDisconnectedJourneySearchIsDisabledButManualPlanningWorks(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	m.editJourneyLocation(0)
	_, content, _ := m.journeyDialogContent(70, 12)
	if !strings.Contains(ansi.Strip(content), "Place search unavailable: not connected") {
		t.Fatal("unavailable Mapbox search was not visibly disabled")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd != nil || m.ui.dialog != dialogJourneyLocation || m.authBusy {
		t.Fatal("disabled place search started work or became selectable")
	}
	enterJourneyCoordinate(m, 0, "51.5, -0.1")
	enterJourneyCoordinate(m, 1, "51.6, -0.2")
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil || j.Stops[0].Location == nil || j.Stops[1].Location == nil {
		t.Fatal("offline coordinates were disabled along with search", err)
	}
}

func TestJourneyInspectionBackRestoresCollapsedPlanner(t *testing.T) {
	m := journeyViewer(t)
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.ui.dialog != dialogInspect || m.planner.collapsed {
		t.Fatal("collapsed planner did not allow inspecting the current position")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	row, center := m.planner.row, m.camera.Center
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !m.planner.collapsed || m.planner.row != row || m.camera.Center == center {
		t.Fatal("cancelled location edit did not restore map keyboard focus")
	}
}

func TestJourneyViewerAutomaticSearchSessionsAndSupersededResults(t *testing.T) {
	m := journeyViewer(t)
	m.places.token = "test-token"
	var requests int
	var session string
	m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if strings.HasSuffix(r.URL.Path, "/suggest") {
			session = r.URL.Query().Get("session_token")
			return locationResponse(`{"suggestions":[{"mapbox_id":"station","name":"Station","place_formatted":"Brighton, UK"}]}`), nil
		}
		if r.URL.Query().Get("session_token") != session {
			t.Fatal("retrieve did not use the suggestion session")
		}
		return locationResponse(`{"attribution":"Mapbox search credits","features":[{"geometry":{"coordinates":[-0.14,50.82]}}]}`), nil
	})
	m.openJourney()
	m.editJourneyLocation(1)
	m.ui.search.SetValue("Brighton")
	if m.schedulePlaceSearch() == nil || requests != 0 {
		t.Fatal("connected search was not automatically scheduled after the pause")
	}
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.ui.dialog != dialogJourneyLocation {
		t.Fatal("search retry interrupted the editor with a confirmation prompt")
	}
	cmd := m.requestPlaceSearch(m.planner.searchID)
	old := cmd()
	m.ui.search.SetValue("Brighton station")
	m.schedulePlaceSearch()
	m.Update(old)
	if len(m.planner.suggestions) != 0 {
		t.Fatal("old query populated new suggestions")
	}
	cmd = m.requestPlaceSearch(m.planner.searchID)
	m.Update(cmd())
	if len(m.planner.suggestions) != 1 || !strings.Contains(m.planner.suggestions[0].Label, "Brighton, UK") {
		t.Fatal("suggestions lost place context")
	}
	previousID := m.planner.searchID
	m.Update(tea.PasteMsg{Content: " central"})
	if len(m.planner.suggestions) != 0 || m.planner.searchID == previousID {
		t.Fatal("pasted query retained stale suggestions")
	}
	cmd = m.requestPlaceSearch(m.planner.searchID)
	m.Update(cmd())
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	result := cmd()
	m.Update(result)
	j, _ := m.workspace.journey(m.planner.ref)
	if m.ui.dialog != dialogJourney || j.Stops[1].Location.Lat != 50.82 || requests != 4 {
		t.Fatal("chosen destination was not retrieved/applied")
	}
	if !strings.Contains(m.journeyCredits(), "Mapbox search credits") {
		t.Fatal("search attribution was lost")
	}
	m.editJourneyLocation(1)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if j.Stops[1].Location.Name != "Station, Brighton, UK" || j.Stops[1].Location.Source != placeSourceMapboxSearch {
		t.Fatal("accepting an unchanged location removed provider provenance")
	}
	enterJourneyCoordinate(m, 1, "51.6, -0.2")
	if !strings.Contains(m.journeyCredits(), "Mapbox search credits") {
		t.Fatal("editing searched coordinates removed attribution")
	}
	m.Update(result)
	if j.Stops[1].Location.Lat != 51.6 {
		t.Fatal("old retrieval replaced an edited destination")
	}
}

func TestJourneySearchAutomaticallyWorksOnEveryLaunch(t *testing.T) {
	for _, name := range []string{"first launch", "next launch"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := journeyViewer(t)
				m.places.token = "token"
				var requests int
				m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
					requests++
					return locationResponse(`{"suggestions":[{"mapbox_id":"village","name":"Hinton"}]}`), nil
				})
				m.openJourney()
				m.editJourneyLocation(0)
				if requests != 0 {
					t.Fatal("opening an empty editor made a search request")
				}
				_, cmd := m.Update(tea.PasteMsg{Content: "Hinton"})
				if cmd == nil || m.ui.dialog != dialogJourneyLocation || requests != 0 {
					t.Fatal("typing did not automatically debounce connected search")
				}
				start := time.Now()
				_, request := m.Update(cmd())
				if request == nil || time.Since(start) != 350*time.Millisecond {
					t.Fatal("search started before the pause or required confirmation")
				}
				m.Update(request())
				if requests != 1 || len(m.planner.suggestions) != 1 {
					t.Fatal("automatic search did not display matching places")
				}
				m.closeDialog()
			})
		})
	}
}

func TestJourneySearchStartsWhenCredentialsArriveAndCancelsOnDisconnect(t *testing.T) {
	m := journeyViewer(t)
	m.args.Provider, m.args.ProviderSet = "none", true
	m.openJourney()
	m.editJourneyLocation(0)
	_, cmd := m.Update(tea.PasteMsg{Content: "Hinton"})
	if cmd != nil {
		t.Fatal("disconnected editor scheduled a network search")
	}
	ready := m.acceptCredentials(credentialResult{credential: renderingCredential{token: "token", source: "login"}})
	if ready == nil || m.ui.dialog != dialogJourneyLocation || m.planner.query != "Hinton" {
		t.Fatal("credential discovery did not resume the current typed query")
	}
	var requestContext context.Context
	m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requestContext = r.Context()
		return locationResponse(`{"suggestions":[{"mapbox_id":"village","name":"Hinton"}]}`), nil
	})
	cmd = m.requestPlaceSearch(m.planner.searchID)
	late := cmd()
	m.acceptCredentials(credentialResult{credential: renderingCredential{source: "disabled"}})
	m.Update(late)
	if requestContext.Err() == nil || len(m.planner.suggestions) != 0 || m.planner.searchBusy {
		t.Fatal("disconnect did not cancel/reject old search work")
	}
	if m.schedulePlaceSearch() != nil {
		t.Fatal("search remained enabled without selected credentials")
	}
}

func TestJourneyBackCancelsLocationSearchAndRejectsLateResults(t *testing.T) {
	m := journeyViewer(t)
	m.places.token = "token"
	var requestContext context.Context
	m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requestContext = r.Context()
		return locationResponse(`{"suggestions":[{"mapbox_id":"village","name":"Hinton"}]}`), nil
	})
	m.openJourney()
	m.editJourneyLocation(0)
	m.ui.search.SetValue("Hinton")
	m.schedulePlaceSearch()
	late := m.requestPlaceSearch(m.planner.searchID)()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if requestContext.Err() == nil || m.ui.dialog != dialogJourney || m.planner.searchBusy {
		t.Fatal("back did not cancel the location search and restore the planner")
	}
	m.editJourneyLocation(0)
	m.Update(late)
	if len(m.planner.suggestions) != 0 || m.ui.search.Value() != "" {
		t.Fatal("cancelled editor's response changed the reopened location editor")
	}
}

func TestJourneyConnectedCoordinatesAndShortQueriesDoNotSearch(t *testing.T) {
	m := journeyViewer(t)
	m.places.token = "token"
	m.openJourney()
	m.editJourneyLocation(0)
	for _, query := range []string{
		"", "Hi", "51", "-51", "51.", "51.5", "51.5,", "51.5, ",
		"51.5, -", "51.5, -0.", "51.5, -0.1", "5e1", "5.15e1, -", "51.5, -0e-",
	} {
		m.ui.search.SetValue(query)
		if m.schedulePlaceSearch() != nil || m.planner.searchBusy {
			t.Fatal("empty, short or unfinished/complete coordinate input scheduled search", query)
		}
	}
}

func TestJourneyNumberedAddressesAndPostcodesStillSearch(t *testing.T) {
	for _, query := range []string{"10 Downing Street", "90210", "10001-0001", "51.5 Café"} {
		t.Run(query, func(t *testing.T) {
			m := journeyViewer(t)
			m.places.token = "token"
			var requests int
			m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Query().Get("q") != query {
					t.Fatal("search query changed")
				}
				return locationResponse(`{"suggestions":[]}`), nil
			})
			m.openJourney()
			m.editJourneyLocation(0)
			m.ui.search.SetValue(query)
			if m.schedulePlaceSearch() == nil {
				t.Fatal("numbered address/place or postcode did not schedule search")
			}
			m.Update(m.requestPlaceSearch(m.planner.searchID)())
			if requests != 1 {
				t.Fatal("scheduled query did not make one search request")
			}
		})
	}
}

func TestJourneyUnicodeLocationsSurviveGeometryAndEdits(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		t.Fatal(err)
	}
	location := placeLocation{
		Name: "Café Zürich\n東京\x1b", Lon: 8.54, Lat: 47.37,
		Source: placeSourceMapboxSearch, Attribution: "© Mapbox\r\n© OpenStreetMap\x1b",
	}
	if err := m.workspace.addJourneyWaypoint(m.planner.ref); err != nil {
		t.Fatal(err)
	}
	for _, stop := range j.Stops {
		if err := m.workspace.setJourneyLocation(m.planner.ref, stop.ID, location); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		for _, stop := range j.Stops {
			if stop.Location.Name != "Café Zürich 東京 " || stop.Location.Attribution != "© Mapbox  © OpenStreetMap " {
				t.Fatalf("workspace corrupted printable Unicode or retained controls: %+v", stop.Location)
			}
		}
		entry, index, err := m.workspace.lookup(m.planner.ref)
		if err != nil {
			t.Fatal(err)
		}
		track := entry.document.Items[index].Track
		for _, points := range [][]Point{track.Points, track.Waypoints} {
			for _, point := range points {
				if point.Name != "Café Zürich 東京 " {
					t.Fatalf("derived geometry corrupted the place name: %q", point.Name)
				}
			}
		}
		m.rebuild()
		text := ansi.Strip(m.content)
		if !strings.Contains(text, "Café Zürich 東京") || !strings.Contains(m.journeyCredits(), "© Mapbox") {
			t.Fatal("journey display or credits lost printable Unicode")
		}
	}
	check()
	m.editJourneyLocation(0)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	check()
	m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	check()
}

func TestJourneySuggestionCreditsAreVisibleAndTransient(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	m.editJourneyLocation(0)
	m.places.token = "token"
	m.ui.search.SetValue("Zürich")
	m.schedulePlaceSearch()
	m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/suggest") {
			return locationResponse(`{"attribution":"© Mapbox suggestions\n","suggestions":[{"mapbox_id":"id","name":"Zürich"}]}`), nil
		}
		return locationResponse(`{"attribution":"© Mapbox location","features":[{"geometry":{"coordinates":[8.54,47.37]}}]}`), nil
	})
	cmd := m.requestPlaceSearch(m.planner.searchID)
	m.Update(cmd())
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.content), "© Mapbox suggestions") {
		t.Fatal("suggestions were displayed without provider attribution")
	}
	m.ui.search.SetValue("Bern")
	m.schedulePlaceSearch()
	if m.journeyCredits() != "" {
		t.Fatal("clearing suggestions retained their attribution")
	}
	m.Update(m.requestPlaceSearch(m.planner.searchID)())
	m.closeDialog()
	if m.journeyCredits() != "" {
		t.Fatal("closing the editor retained suggestion attribution")
	}
	m.editJourneyLocation(0)
	m.ui.search.SetValue("Zürich")
	m.schedulePlaceSearch()
	m.Update(m.requestPlaceSearch(m.planner.searchID)())
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if !strings.Contains(m.journeyCredits(), "© Mapbox location") ||
		strings.Contains(m.journeyCredits(), "© Mapbox suggestions") {
		t.Fatal("retrieval did not replace temporary suggestion credits with location credits")
	}
}

func TestJourneyDistinctCreditsKeepFooterSingleLine(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			m := journeyViewer(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.settings.ASCII = ascii
			m.openJourney()
			j, _ := m.workspace.journey(m.planner.ref)
			for i, stop := range j.Stops {
				if err := m.workspace.setJourneyLocation(m.planner.ref, stop.ID, placeLocation{
					Lon: float64(i) * .01, Lat: 51,
					Attribution: fmt.Sprintf("© C%d", i+1),
				}); err != nil {
					t.Fatal(err)
				}
			}
			m.closeDialog()
			m.rebuild()
			if lipgloss.Height(m.content) > size[1] || lipgloss.Width(m.content) > size[0] {
				t.Fatalf("distinct credits overflowed %v, ASCII %v", size, ascii)
			}
			lines := strings.Split(ansi.Strip(m.content), "\n")
			footer := lines[len(lines)-1]
			if !strings.Contains(footer, "© C1 © C2") {
				t.Fatalf("footer lost distinct credits: %q", footer)
			}
			m.openDialog(dialogCredits)
			_, _, _ = m.dialogContent(60, 17)
			if !strings.Contains(m.journeyCredits(), "© C1\n© C2") {
				t.Fatal("credits dialog no longer has separately listed credits")
			}
		}
	}
}

func TestJourneyViewerRequestsCancelOnCloseAndRemoval(t *testing.T) {
	m := journeyViewer(t)
	m.places.token = "token"
	var requestContext context.Context
	m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requestContext = r.Context()
		return locationResponse(`{"suggestions":[]}`), nil
	})
	m.openJourney()
	m.editJourneyLocation(1)
	m.ui.search.SetValue("Place")
	m.schedulePlaceSearch()
	cmd := m.requestPlaceSearch(m.planner.searchID)
	result := cmd()
	m.closeDialog()
	if requestContext.Err() == nil {
		t.Fatal("closing planner did not cancel place lookup")
	}
	m.Update(result)
	m.openJourney()
	if m.ui.dialog != dialogJourney {
		t.Fatal("reopening planner did not return to the journey")
	}
	m.editJourneyLocation(1)
	m.ui.search.SetValue("Place")
	m.schedulePlaceSearch()
	cmd = m.requestPlaceSearch(m.planner.searchID)
	m.closeDialog()
	result = cmd()
	if err := m.workspace.removeItem(m.planner.ref); err != nil {
		t.Fatal(err)
	}
	m.Update(result)
	if len(m.workspace.documents) != 0 {
		t.Fatal("late location result revived a removed journey")
	}
}

func TestJourneyViewerRotatesExpiredAndFullSearchSessions(t *testing.T) {
	for _, name := range []string{"expired", "full"} {
		t.Run(name, func(t *testing.T) {
			m := journeyViewer(t)
			m.openJourney()
			m.editJourneyLocation(1)
			m.places.token = "token"
			m.ui.search.SetValue("Place")
			m.schedulePlaceSearch()
			m.planner.session = "old-session"
			m.planner.sessionStarted = time.Now()
			if name == "expired" {
				m.planner.sessionStarted = time.Now().Add(-3 * time.Minute)
			} else {
				m.planner.sessionRequests = 49
			}
			m.requestPlaceSearch(m.planner.searchID)
			if m.planner.session == "old-session" || m.planner.sessionRequests != 1 {
				t.Fatal("ended search session was reused")
			}
			m.closeDialog()
		})
	}
}

func TestJourneyDebounceDoesNotSurviveReplacingPlan(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	m.editJourneyLocation(0)
	m.places.token = "token"
	m.ui.search.SetValue("Old place")
	m.schedulePlaceSearch()
	old := placeSearchReady{id: m.planner.searchID}
	m.closeDialog()
	if err := m.workspace.removeItem(m.planner.ref); err != nil {
		t.Fatal(err)
	}
	m.openJourney()
	m.editJourneyLocation(0)
	m.ui.search.SetValue("New place")
	m.schedulePlaceSearch()
	if _, cmd := m.Update(old); cmd != nil || m.planner.searchBusy {
		t.Fatal("an old plan's debounce started a new plan's request")
	}
}

func TestJourneyClipboardKeepsEditorOwnership(t *testing.T) {
	pasteKey := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	t.Run("same opening after query edits", func(t *testing.T) {
		m := journeyViewer(t)
		m.openJourney()
		m.editJourneyLocation(0)
		queued := m.clipboardCommand(pasteKey, func() tea.Msg { return tea.PasteMsg{Content: " pasted"} })
		m.ui.search.SetValue("typed")
		m.schedulePlaceSearch()
		m.planner.suggestions = []placeSuggestion{{ID: "old", Label: "Old suggestion"}}
		m.Update(queued())
		if m.ui.search.Value() != "typed pasted" || len(m.planner.suggestions) != 0 {
			t.Fatal("owned clipboard result did not insert and invalidate suggestions")
		}
	})
	for _, scenario := range []string{
		"different stop", "same stop reopened", "command palette", "palette to journey", "quick search",
	} {
		t.Run(scenario, func(t *testing.T) {
			m := journeyViewer(t)
			m.openJourney()
			m.editJourneyLocation(0)
			if scenario == "palette to journey" {
				m.openDialog(dialogCommands)
			}
			queued := m.clipboardCommand(pasteKey, func() tea.Msg { return tea.PasteMsg{Content: "old clipboard"} })
			switch scenario {
			case "different stop", "palette to journey":
				m.editJourneyLocation(1)
			case "same stop reopened":
				m.closeDialog()
				m.openJourney()
				m.editJourneyLocation(0)
			case "command palette":
				m.openDialog(dialogCommands)
			case "quick search":
				m.openDialog(dialogSearch)
			}
			m.ui.search.SetValue("new input")
			m.ui.notice = "existing notice"
			generation := m.planner.searchID
			idle := func() tea.Msg { return nil }
			var deliver func(tea.Cmd)
			deliver = func(cmd tea.Cmd) {
				msg := cmd()
				if batch, ok := msg.(tea.BatchMsg); ok {
					for _, child := range batch {
						deliver(child)
					}
					return
				}
				m.Update(msg)
			}
			deliver(tea.Batch(tea.Batch(queued, idle), idle))
			if m.ui.search.Value() != "new input" || m.ui.notice != "existing notice" || m.planner.searchID != generation {
				t.Fatal("stale clipboard result modified a different input")
			}
		})
	}
}

func TestJourneyClipboardOpaqueErrorIsOwnerScoped(t *testing.T) {
	previous := clipboard.Unsupported
	clipboard.Unsupported = true
	t.Cleanup(func() { clipboard.Unsupported = previous })
	for _, stale := range []bool{false, true} {
		m := journeyViewer(t)
		m.openJourney()
		m.editJourneyLocation(0)
		queued := m.clipboardCommand(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, textinput.Paste)
		if stale {
			m.editJourneyLocation(1)
		}
		m.Update(queued())
		if (m.ui.search.Err == nil) != stale {
			t.Fatalf("clipboard error ownership incorrect, stale=%v: %v", stale, m.ui.search.Err)
		}
	}
}

func TestJourneyNoticesKeepSelectedActionVisible(t *testing.T) {
	for _, size := range [][2]int{{70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			m := journeyViewer(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.settings.ASCII = ascii
			m.openJourney()
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			m.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
			m.rebuild()
			if m.ui.notice == "" || !strings.Contains(ansi.Strip(m.content), "> Copy journey to clip") {
				t.Fatalf("notice hid selected action at %v, ASCII %v: %s", size, ascii, ansi.Strip(m.content))
			}
		}
	}
}

func TestJourneyDetailsSeparateStopSections(t *testing.T) {
	m, _ := routingTestViewer(t)
	m.Update(m.calculateJourneyRoute()())
	j, _ := m.workspace.journey(m.planner.ref)
	lines := m.journeyDetails()
	for i := range j.Stops {
		index := slices.Index(lines, journeyStopLabel(i, len(j.Stops)))
		if index <= 0 || lines[index-1] != "" {
			t.Fatal("journey stop details lacked spacing", i)
		}
	}
}

func TestJourneyCoordinateInspectionAndPlaybackCommands(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	enterJourneyCoordinate(m, 0, "51.5, -0.1")
	enterJourneyCoordinate(m, 1, "51.6, -0.2")
	showJourneyFromMenu(m)
	type clickTarget struct {
		name string
		x, y int
	}
	targets := []clickTarget{{name: "off route", x: 2, y: 3}}
	g := renderScene(m.camera, nil, m.workspace.sceneItems(), nil)
	var endpoint, line bool
	for y := range g.Height {
		for x := range g.Width {
			cell := g.Cells[y*g.Width+x]
			name := ""
			unlabelledLine := cell.Bits != 0 && cell.Text == 0
			if cell.Text == 'A' && !endpoint {
				name, endpoint = "endpoint", true
			} else if unlabelledLine && !line {
				name, line = "line", true
			}
			if name != "" {
				targets = append(targets, clickTarget{name: name, x: x + 1, y: y + 2})
			}
		}
	}
	if !endpoint || !line {
		t.Fatal("journey did not render an inspectable line and endpoint")
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			m.Update(tea.MouseClickMsg{X: target.x, Y: target.y, Button: tea.MouseLeft})
			m.Update(tea.MouseReleaseMsg{X: target.x, Y: target.y, Button: tea.MouseLeft})
			if m.inspected == nil {
				t.Fatal("journey click did not inspect coordinates")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.rebuild()
			text := ansi.Strip(m.content)
			if !strings.Contains(text, fmt.Sprintf("Lat %.6f", m.inspected.Y)) ||
				!strings.Contains(text, fmt.Sprintf("Lon %.6f", m.inspected.X)) {
				t.Fatal("journey inspection omitted the clicked coordinates")
			}
			m.closeDialog()
		})
	}
	m.openDialog(dialogCommands)
	for _, command := range m.commands() {
		if command.action == "speed" || command.action == "play" {
			t.Fatal("journey exposed an unavailable playback command")
		}
	}
	d, err := loadDocument("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m.workspace.addDocument(d)
	var playbackCommands int
	for _, command := range m.commands() {
		if command.action == "speed" || command.action == "play" || command.action == "center" {
			playbackCommands++
		}
	}
	if playbackCommands != 3 {
		t.Fatal("recorded activity lost playback commands")
	}
}

func TestJourneyDialogsFitWideNarrowAndASCIITerminals(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			m := journeyViewer(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.settings.ASCII = ascii
			m.openJourney()
			for _, dialog := range []dialogKind{dialogJourney, dialogJourneyLocation} {
				if dialog == dialogJourneyLocation {
					m.editJourneyLocation(0)
				} else {
					m.openDialog(dialog)
				}
				m.rebuild()
				if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
					t.Fatalf("dialog %d overflowed %v, ASCII %v: %dx%d", dialog, size, ascii,
						lipgloss.Width(m.content), lipgloss.Height(m.content))
				}
				if ascii && strings.ContainsFunc(m.content, func(r rune) bool { return r >= 0x2800 && r <= 0x28ff }) {
					t.Fatal("ASCII planner rendered Braille")
				}
			}
		}
	}
}

func TestJourneyMapPickPreservesImportedPlayback(t *testing.T) {
	m := journeyViewer(t)
	ref, err := m.workspace.open("../fixtures/country-ride.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m.openJourney()
	m.editJourneyLocation(0)
	playback := m.activePlayback()
	playback.Playing = true
	m.lastTick = time.Now()
	m.Update(tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 3, Y: 4, Button: tea.MouseLeft})
	j, _ := m.workspace.journey(m.planner.ref)
	if j.Stops[0].Location == nil || !playback.Playing || m.workspace.selection.ref != ref {
		t.Fatal("journey map pick failed or interrupted the imported activity")
	}
}

func TestNestedJourneyHeaderCapturesMouse(t *testing.T) {
	for _, dialog := range []dialogKind{dialogJourneyRouting, dialogJourneyRoutingHelp} {
		for _, button := range []tea.MouseButton{tea.MouseLeft, tea.MouseRight} {
			m := journeyViewer(t)
			m.openJourney()
			m.pushDialog(dialog)
			for y := m.mapOriginY(); y < m.journeyPanelY(); y++ {
				x := m.width - 3
				m.Update(tea.MouseClickMsg{X: x, Y: y, Button: button})
				if m.dragging || m.rotating {
					t.Fatal("header click started a map gesture")
				}
				m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: button})
				if m.inspected != nil {
					t.Fatal("header click inspected the covered map")
				}
			}
		}
	}
}

func TestUndoRestoresCompleteJourneySelection(t *testing.T) {
	for _, imported := range []bool{false, true} {
		m := journeyViewer(t)
		m.openJourney()
		enterJourneyCoordinate(m, 0, "51.3,-2.5")
		enterJourneyCoordinate(m, 1, "51.4,-2.6")
		m.planner.row = 0
		m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
		if m.workspace.selection != nil {
			t.Fatal("incomplete journey remained selected")
		}
		expected := m.planner.ref
		if imported {
			var err error
			expected, err = m.workspace.open("../fixtures/country-ride.gpx")
			if err != nil {
				t.Fatal(err)
			}
			m.activePlayback().Playing = true
		}
		m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
		j, _ := m.workspace.journey(m.planner.ref)
		if !j.hasCompleteLocations() || m.workspace.selection == nil || m.workspace.selection.ref != expected {
			t.Fatal("undo did not restore the journey or preserve the expected selection")
		}
		if imported && !m.activePlayback().Playing {
			t.Fatal("undo interrupted imported playback")
		}
	}
}
