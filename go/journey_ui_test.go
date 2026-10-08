package main

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
		width: 120, height: 40, args: Args{NoAnimation: true}, showProfile: true,
	}
	if err := m.initUI(); err != nil {
		t.Fatal(err)
	}
	m.layout()
	return m
}

func enterJourneyCoordinate(m *viewer, index int, text string) {
	m.editJourneyLocation(index)
	m.ui.search.SetValue(text)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.content), "Journey plan") || m.activePlayback() != nil || m.profileHeight != 0 {
		t.Fatal("manual journey became a recorded activity or elevation profile")
	}
	ref := m.planner.ref
	m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.ui.dialog != dialogJourney || m.planner.ref != ref || requests != 0 {
		t.Fatal("reopening created a new journey or made a location request")
	}
}

func TestJourneyViewerSearchOptInSessionsAndSupersededResults(t *testing.T) {
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
	if m.schedulePlaceSearch() != nil || requests != 0 {
		t.Fatal("search requested Mapbox without opting in")
	}
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.ui.dialog != dialogJourneySearch {
		t.Fatal("search did not explain account-dependent option")
	}
	m.Update(tea.KeyPressMsg{Code: 'y'})
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
	if j.Stops[1].Location.Name != "Station, Brighton, UK" || j.Stops[1].Location.Source != "Mapbox Search Box" {
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
	m.planner.online = true
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
	m.planner.online = true
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
			m.planner.online = true
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

func TestJourneyDialogsFitWideNarrowAndASCIITerminals(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			m := journeyViewer(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.settings.ASCII = ascii
			m.openJourney()
			for _, dialog := range []dialogKind{dialogJourney, dialogJourneyLocation, dialogJourneySearch} {
				m.ui.dialog = dialog
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
