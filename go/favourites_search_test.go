package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func prepareFavouriteSearch(t *testing.T, m *viewer) {
	t.Helper()
	m.openDialog(dialogSearch)
	m.ui.search.SetValue("hinton blew")
	m.planner.suggestions = []placeSuggestion{
		{ID: "other-id", Label: "Other place", Type: "place"},
		{ID: "village-id", Label: "Hinton Blewett", Type: "place"},
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
}

func TestSaveHighlightedSearchFavourite(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/ascii=%v", size[0], size[1], ascii), func(t *testing.T) {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				m := journeyViewer(t)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.settings.ASCII = ascii
				m.places.token = "test-token"
				var requests int
				m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
					requests++
					if r.URL.Query().Get("q") != "village-id" || r.URL.Query().Get("permanent") != "true" {
						t.Fatal("Alt-S saved the typed query or the wrong result")
					}
					return locationResponse(permanentPlaceFixture), nil
				})
				prepareFavouriteSearch(t, m)
				camera := m.camera
				_, save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
				if save == nil || !m.planner.savingFavourite {
					t.Fatal("Alt-S still demands coordinates", m.ui.notice)
				}
				_, duplicate := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
				if duplicate != nil || m.ui.feedback != "Saving favourite..." {
					t.Fatal("repeated save started a second request or cleared progress")
				}
				m.Update(save())
				if m.ui.dialog != dialogSearch || m.ui.search.Value() != "hinton blew" || m.planner.result != 1 {
					t.Fatal("save lost the query or selected result")
				}
				if m.camera != camera || len(m.workspace.documents) != 0 {
					t.Fatal("saving navigated the map or changed the workspace")
				}
				m.rebuild()
				marker := "★ Hinton Blewett"
				if ascii {
					marker = "* Hinton Blewett"
				}
				if !strings.Contains(ansi.Strip(m.content), marker) {
					t.Fatal("saved result has no visible star", ansi.Strip(m.content))
				}
				store, _ := localFavouriteStore()
				entries, err := store.read()
				if err != nil || len(entries) != 1 || entries[0].ProviderID != "village-id" {
					t.Fatal("selected result was not persisted", entries, err)
				}
				fresh := journeyViewer(t)
				fresh.runCommand("favourites")
				if !strings.Contains(fresh.journeyCredits(), "© Mapbox") {
					t.Fatal("offline favourites picker lost attribution")
				}
				fresh.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if fresh.searchLocation == nil || *fresh.searchLocation != entries[0].location() {
					t.Fatal("offline restart lost the saved location or its attribution")
				}
				_, remove := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
				if remove != nil || requests != 1 || m.ui.feedback != "Favourite removed" {
					t.Fatal("toggling off fetched the place again or failed", requests, m.ui.notice)
				}
				entries, err = store.read()
				if err != nil || len(entries) != 0 {
					t.Fatal("toggle did not remove persisted favourite", entries, err)
				}
				m.rebuild()
				if strings.Contains(ansi.Strip(m.content), marker) {
					t.Fatal("removed favourite retained its star")
				}
				_, saveAgain := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
				if saveAgain == nil {
					t.Fatal("could not toggle the favourite on again", m.ui.notice)
				}
				m.Update(saveAgain())
				entries, err = store.read()
				if err != nil || len(entries) != 1 || requests != 2 {
					t.Fatal("toggle-on did not save exactly one favourite", entries, err, requests)
				}
			})
		}
	}
}

func TestSaveSearchFavouriteRejectsCancelledAndFailedLookups(t *testing.T) {
	for _, scenario := range []string{
		"close", "new query", "reopen", "commands", "favourites", "HTTP failure", "broken store", "venue",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			m := journeyViewer(t)
			m.places.token = "token"
			var requestContext context.Context
			m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				requestContext = r.Context()
				response := locationResponse(permanentPlaceFixture)
				if scenario == "HTTP failure" {
					response.StatusCode = http.StatusForbidden
				}
				return response, nil
			})
			prepareFavouriteSearch(t, m)
			if scenario == "venue" {
				m.planner.suggestions[1].Type = "poi"
			}
			_, save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
			if scenario == "venue" {
				if save != nil || !strings.Contains(m.ui.notice, "named venues") {
					t.Fatal("venue was fetched or had a misleading coordinate prompt", m.ui.notice)
				}
				return
			}
			result := save()
			switch scenario {
			case "close":
				m.closeDialog()
			case "new query":
				m.Update(tea.PasteMsg{Content: "new"})
			case "reopen":
				m.openDialog(dialogSearch)
			case "commands":
				m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
			case "favourites":
				m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
				_, back := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				deliverFavouriteSearchCommands(m, back)
				if m.planner.savingFavourite || m.planner.searchBusy || m.ui.feedback != "" {
					t.Fatal("returning from favourites revived the cancelled save")
				}
			case "broken store":
				store, _ := localFavouriteStore()
				writeBrokenFavouriteFile(t, store.path)
			}
			m.Update(result)
			store, _ := localFavouriteStore()
			entries, err := store.read()
			if scenario == "broken store" {
				if err == nil || m.ui.feedback == "Favourite saved" {
					t.Fatal("failed save was reported as successful")
				}
				return
			}
			if err != nil || len(entries) != 0 || m.savedSearchFavourite("village-id") {
				t.Fatal("cancelled/failed lookup saved a favourite", entries, err)
			}
			if requestContext.Err() == nil {
				t.Fatal("completed or abandoned lookup context was not cancelled")
			}
		})
	}
}

func writeBrokenFavouriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken JSON"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSavedSearchStarSurvivesRenameAndReopen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, _ := localFavouriteStore()
	entry := newFavourite("Home", placeLocation{
		Name: "Hinton Blewett", Lon: -2.54, Lat: 51.31,
		Source: placeSourceMapboxPermanent, ProviderID: "village-id", Attribution: "© Mapbox",
	})
	if err := store.change(nil, &entry); err != nil {
		t.Fatal(err)
	}
	m := journeyViewer(t)
	m.runCommand("favourites")
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m.ui.search.SetValue("My village")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.openDialog(dialogSearch)
	m.acceptPlaceSearch(placeSearchResult{
		id: m.planner.searchID, ref: m.planner.ref, stopID: m.planner.stopID,
		suggestions: []placeSuggestion{{ID: "village-id", Label: "Hinton Blewett", Type: "place"}},
	})
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.content), "★ Hinton Blewett") {
		t.Fatal("renamed favourite lost its star after searching again")
	}
	_, request := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
	if request != nil || m.ui.feedback != "Favourite removed" {
		t.Fatal("renamed favourite could not be toggled off by ID")
	}
	entries, err := store.read()
	if err != nil || len(entries) != 0 {
		t.Fatal("toggle left a renamed favourite behind", entries, err)
	}
}
