package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func searchWorkspaceSnapshot(t *testing.T, m *viewer) string {
	t.Helper()
	data, err := json.Marshal(m.workspace.items())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestQuickSearchPreservesWorkspace(t *testing.T) {
	for _, selected := range []string{"none", "import", "journey", "import with journey"} {
		for _, accept := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/accept=%v", selected, accept), func(t *testing.T) {
				m := journeyViewer(t)
				if selected != "none" {
					ref, err := m.workspace.open("../fixtures/example.gpx")
					if err != nil {
						t.Fatal(err)
					}
					if selected != "import" {
						m.openJourney()
						enterJourneyCoordinate(m, 0, "51.5, -0.1")
						enterJourneyCoordinate(m, 1, "51.6, -0.2")
						m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
						enterJourneyCoordinate(m, 1, "51.55, -0.15")
						m.closeDialog()
						if selected == "import with journey" {
							if err := m.workspace.selectItem(ref); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				var playbackBefore *playback
				if p := m.activePlayback(); p != nil {
					p.Playing, p.Elapsed = true, 5
					before := *p
					playbackBefore = &before
					m.following = true
				}
				m.camera.Zoom = 13
				m.inspected = &XY{1, 2}
				camera, inspected, following := m.camera, m.inspected, m.following
				workspace := searchWorkspaceSnapshot(t, m)
				selection, nextID := m.workspace.selection, m.workspace.nextID
				ref, row, stopID := m.planner.ref, m.planner.row, m.planner.stopID
				m.directions.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
					t.Fatal("quick search requested directions")
					return nil, nil
				})
				m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
				if m.ui.dialog != dialogSearch || searchWorkspaceSnapshot(t, m) != workspace {
					t.Fatal("opening quick search changed the workspace")
				}
				for _, character := range "qhjklf/as " {
					m.Update(tea.KeyPressMsg{Code: character, Text: string(character)})
				}
				if m.ui.dialog != dialogSearch || m.camera != camera || m.ui.search.Value() != "qhjklf/as " {
					t.Fatal("typing in quick search reached a map shortcut")
				}
				if accept {
					m.ui.search.SetValue("51.62, -0.25")
					m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					if m.camera.Center != project(-0.25, 51.62) || m.following {
						t.Fatal("quick search did not centre the map and disable follow")
					}
					if m.inspected == nil || *m.inspected != (XY{-0.25, 51.62}) {
						t.Fatal("quick search did not expose the chosen coordinates")
					}
				} else {
					m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
					if m.camera != camera || m.inspected != inspected || m.following != following {
						t.Fatal("cancelling quick search changed the map")
					}
				}
				plannerChanged := m.planner.ref != ref || m.planner.row != row || m.planner.stopID != stopID
				selectionChanged := m.workspace.selection != selection || m.workspace.nextID != nextID
				if m.ui.dialog != dialogNone || plannerChanged || selectionChanged {
					t.Fatal("quick search changed planner navigation or workspace selection")
				}
				if searchWorkspaceSnapshot(t, m) != workspace || !reflect.DeepEqual(m.activePlayback(), playbackBefore) {
					t.Fatal("quick search changed imported geometry, journey stops or playback")
				}
			})
		}
	}
}

func TestQuickSearchFrameStaysRenderable(t *testing.T) {
	for _, location := range []XY{
		{179.999, 0}, {-179.999, 0}, {0, 85.05}, {0, -85.05}, {179.999, 90},
		{180, 0}, {-180, 0}, {0, -90}, {180, -90}, {-180, 90},
	} {
		for _, size := range [][2]int{{88, 34}, {28, 3}} {
			for _, bearing := range []float64{0, 45, 120} {
				name := fmt.Sprintf(
					"%v/%v/bearing=%.0f",
					location,
					size,
					bearing,
				)
				t.Run(name, func(t *testing.T) {
					camera := localView(location, Camera{
						Width: size[0], Height: size[1], Aspect: 2, Bearing: bearing,
					})
					a, b := visibleBounds(camera)
					outsideX := a.X < -1e-12 || b.X > 1+1e-12
					outsideY := a.Y < -1e-12 || b.Y > 1+1e-12
					if outsideX || outsideY || len(visible(camera)) == 0 {
						t.Fatal("quick-search frame exceeded renderable tile coverage", a, b)
					}
					point := camera.screen(project(location.X, location.Y))
					outsideX = point.X < 0 || point.X >= float64(camera.Width*2)
					outsideY = point.Y < 0 || point.Y >= float64(camera.Height*4)
					if outsideX || outsideY {
						t.Fatal("world-edge framing lost the search point", point)
					}
					grid := newGrid(camera.Width, camera.Height)
					grid.label(point, "@", roleCursor, false)
					if !strings.Contains(grid.plain(false), "@") {
						t.Fatal("exact-edge search point did not reach a rendered cell")
					}
				})
			}
		}
	}
}

func TestQuickSearchInspectionSurvivesPlaybackPulse(t *testing.T) {
	m := journeyViewer(t)
	if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
		t.Fatal(err)
	}
	p := m.activePlayback()
	p.Playing = true
	start := time.Now()
	m.lastTick = start
	selection := m.workspace.selection
	m.openDialog(dialogSearch)
	m.ui.search.SetValue("51.62, -0.25")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	camera := m.camera
	m.Update(pulse(start.Add(16 * time.Millisecond)))
	if !p.Playing || p.Elapsed <= 0 || m.workspace.selection != selection || m.camera != camera {
		t.Fatal("quick search interrupted playback, selection or the searched camera")
	}
	if m.inspected == nil || *m.inspected != (XY{-0.25, 51.62}) {
		t.Fatal("playback pulse discarded the accepted search coordinates")
	}
	if !strings.Contains(strings.Join(m.details(), "\n"), "Lat 51.620000\nLon -0.250000") {
		t.Fatal("searched coordinates disappeared from details during playback")
	}
	for range 2 {
		m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
		if m.inspected == nil || *m.inspected != (XY{-0.25, 51.62}) {
			t.Fatal("playback toggle discarded the accepted search coordinates")
		}
	}
	m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if m.inspected == nil || m.following || m.destination().Center != project(-0.25, 51.62) {
		t.Fatal("centering did not retain the selected search location")
	}
}

func TestQuickSearchFramesMileRadius(t *testing.T) {
	for _, location := range []XY{{-.1, 0}, {-.1, 51.5}, {-.1, 80}, {-.1, -80}} {
		for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
			for _, bearing := range []float64{0, 45, 120} {
				for _, zoom := range []float64{-2, 18} {
					name := fmt.Sprintf(
						"%v/%v/bearing=%.0f/zoom=%.0f",
						location,
						size,
						bearing,
						zoom,
					)
					t.Run(name, func(t *testing.T) {
						m := journeyViewer(t)
						m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
						m.camera.Zoom, m.camera.Bearing = zoom, bearing
						m.openDialog(dialogSearch)
						m.ui.search.SetValue(fmt.Sprintf("%.8f, %.8f", location.Y, location.X))
						m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
						if m.camera.Center != project(location.X, location.Y) || m.camera.Bearing != bearing {
							t.Fatal("quick search lost its precise centre or bearing")
						}
						center := unproject(m.camera.Center)
						nearest := math.Inf(1)
						for _, edge := range []XY{
							{float64(m.camera.Width), 0},
							{float64(m.camera.Width), float64(m.camera.Height * 4)},
							{0, float64(m.camera.Height * 2)},
							{float64(m.camera.Width * 2), float64(m.camera.Height * 2)},
						} {
							point := unproject(m.camera.world(edge))
							distance := haversine(
								Point{Lon: center.X, Lat: center.Y},
								Point{Lon: point.X, Lat: point.Y},
							)
							if distance < 1609 {
								t.Fatalf("quick-search viewport cropped mile-radius context: %.0f m", distance)
							}
							nearest = math.Min(nearest, distance)
						}
						invalidZoom := math.IsNaN(m.camera.Zoom) || math.IsInf(m.camera.Zoom, 0)
						if nearest > 2600 || invalidZoom {
							t.Fatal("quick search did not zoom to local context", nearest, m.camera.Zoom)
						}
					})
				}
			}
		}
	}
}

func TestQuickSearchSuggestRetrieveAndCredits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := journeyViewer(t)
		m.places.token = "token"
		var session string
		var requests int
		m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			if strings.HasSuffix(r.URL.Path, "/suggest") {
				session = r.URL.Query().Get("session_token")
				if session == "" || r.URL.Query().Get("q") != "Brighton" {
					t.Fatal("quick search lost its query or session")
				}
				return locationResponse(`{"attribution":"Suggestion credits","suggestions":[{"mapbox_id":"station","name":"Station"},{"mapbox_id":"park","name":"Park"}]}`), nil
			}
			if r.URL.Query().Get("session_token") != session || !strings.HasSuffix(r.URL.Path, "/station") {
				t.Fatal("quick search retrieved the wrong result or session")
			}
			return locationResponse(`{"attribution":"Mapbox search credits","features":[{"geometry":{"coordinates":[-0.14,50.82]}}]}`), nil
		})
		m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
		_, ready := m.Update(tea.PasteMsg{Content: "Brighton"})
		if ready == nil || requests != 0 {
			t.Fatal("quick search did not wait for its debounce")
		}
		start := time.Now()
		_, request := m.Update(ready())
		if request == nil || time.Since(start) != 350*time.Millisecond {
			t.Fatal("quick search did not use the normal debounce")
		}
		m.Update(request())
		if len(m.planner.suggestions) != 2 || !strings.Contains(m.journeyCredits(), "Suggestion credits") {
			t.Fatal("quick search lost suggestions or their attribution")
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		if m.planner.result != 1 {
			t.Fatal("quick-search results did not wrap upwards")
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		_, retrieve := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if retrieve == nil {
			t.Fatal("accepting a suggestion did not retrieve its location")
		}
		m.Update(retrieve())
		if requests != 2 || m.camera.Center != project(-0.14, 50.82) || len(m.workspace.documents) != 0 {
			t.Fatal("search retrieval did not centre the map without creating a journey")
		}
		if m.ui.dialog != dialogNone || m.journeyCredits() != "Mapbox search credits" {
			t.Fatal("quick search stayed open or lost selected-place attribution")
		}
		m.openDialog(dialogCredits)
		m.rebuild()
		if !strings.Contains(ansi.Strip(m.content), "Mapbox search credits") {
			t.Fatal("selected search attribution was missing from map credits")
		}
	})
}

func TestQuickSearchRejectsLateRetrievals(t *testing.T) {
	for _, scenario := range []string{"cancelled", "reopened", "new query", "journey editor", "command menu"} {
		t.Run(scenario, func(t *testing.T) {
			m := journeyViewer(t)
			m.places.token = "token"
			var requestContext context.Context
			m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/suggest") {
					return locationResponse(`{"suggestions":[{"mapbox_id":"station","name":"Station"}]}`), nil
				}
				requestContext = r.Context()
				return locationResponse(`{"features":[{"geometry":{"coordinates":[-0.14,50.82]}}]}`), nil
			})
			m.openDialog(dialogSearch)
			m.ui.search.SetValue("Brighton")
			m.schedulePlaceSearch()
			m.Update(m.requestPlaceSearch(m.planner.searchID)())
			_, retrieve := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			late := retrieve()
			switch scenario {
			case "cancelled", "reopened", "journey editor":
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				if scenario == "reopened" {
					m.openDialog(dialogSearch)
				}
				if scenario == "journey editor" {
					m.openJourney()
					m.editJourneyLocation(1)
				}
			case "new query":
				m.Update(tea.PasteMsg{Content: " newer"})
			case "command menu":
				m.openDialog(dialogCommands)
			}
			camera, dialog, query := m.camera, m.ui.dialog, m.ui.search.Value()
			workspace := searchWorkspaceSnapshot(t, m)
			m.Update(late)
			contextChanged := m.camera != camera || m.ui.dialog != dialog || m.ui.search.Value() != query
			if requestContext.Err() == nil || contextChanged {
				t.Fatal("obsolete retrieval was not cancelled or changed a newer search context")
			}
			if searchWorkspaceSnapshot(t, m) != workspace || len(m.planner.suggestions) != 0 || m.planner.searchBusy {
				t.Fatal("obsolete quick-search retrieval changed the workspace or revived results")
			}
		})
	}
}

func TestQuickSearchStartsWhenCredentialsArriveAndCancelsOnDisconnect(t *testing.T) {
	m := journeyViewer(t)
	m.args.Provider, m.args.ProviderSet = "none", true
	m.openDialog(dialogSearch)
	_, command := m.Update(tea.PasteMsg{Content: "Hinton"})
	if command != nil {
		t.Fatal("disconnected quick search scheduled a network request")
	}
	ready := m.acceptCredentials(credentialResult{credential: renderingCredential{token: "token", source: "login"}})
	if ready == nil || m.ui.dialog != dialogSearch || m.planner.query != "Hinton" {
		t.Fatal("credential discovery did not resume the quick-search query")
	}
	var requestContext context.Context
	m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requestContext = r.Context()
		return locationResponse(`{"suggestions":[{"mapbox_id":"village","name":"Hinton"}]}`), nil
	})
	late := m.requestPlaceSearch(m.planner.searchID)()
	m.acceptCredentials(credentialResult{credential: renderingCredential{source: "disabled"}})
	m.Update(late)
	if requestContext.Err() == nil || len(m.planner.suggestions) != 0 || m.planner.searchBusy {
		t.Fatal("disconnect did not cancel and reject old quick-search results")
	}
	if len(m.workspace.documents) != 0 || m.ui.dialog != dialogSearch {
		t.Fatal("credential discovery created a journey or replaced quick search")
	}
}

func TestQuickSearchEarlyEnterKeepsCompactResultsVisible(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		t.Run(fmt.Sprintf("ascii=%v", ascii), func(t *testing.T) {
			m := journeyViewer(t)
			m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
			m.settings.ASCII = ascii
			m.places.token = "token"
			m.places.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/suggest") {
					return locationResponse(`{"suggestions":[{"mapbox_id":"station","name":"Station"},{"mapbox_id":"park","name":"Park"}]}`), nil
				}
				if !strings.HasSuffix(r.URL.Path, "/park") {
					t.Fatal("compact search retrieved a different selection")
				}
				return locationResponse(`{"features":[{"geometry":{"coordinates":[-0.14,50.82]}}]}`), nil
			})
			m.openDialog(dialogSearch)
			m.Update(tea.PasteMsg{Content: "Brighton"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.ui.notice == "" {
				t.Fatal("early Enter did not exercise a search notice")
			}
			m.Update(m.requestPlaceSearch(m.planner.searchID)())
			m.rebuild()
			if !strings.Contains(ansi.Strip(m.content), "Station") {
				t.Fatal("early Enter notice hid arriving search results", ansi.Strip(m.content))
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			m.rebuild()
			if !strings.Contains(ansi.Strip(m.content), "Park") {
				t.Fatal("compact search hid its selected result")
			}
			_, retrieve := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.Update(retrieve())
			if m.ui.dialog != dialogNone || m.inspected == nil || *m.inspected != (XY{-0.14, 50.82}) {
				t.Fatal("visible compact result was not accepted")
			}
		})
	}
}

func TestQuickSearchValidationAndLayouts(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/ascii=%v", size[0], size[1], ascii), func(t *testing.T) {
				m := journeyViewer(t)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.settings.ASCII = ascii
				camera := m.camera
				m.openDialog(dialogSearch)
				m.ui.search.SetValue("91, 0")
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				m.rebuild()
				if m.ui.notice == "" || m.ui.dialog != dialogSearch || m.camera != camera {
					t.Fatal("invalid coordinates changed the map or closed quick search")
				}
				if !strings.Contains(ansi.Strip(m.content), "Quick search") || m.cursor == nil {
					t.Fatal("quick search or its text cursor was missing")
				}
				if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
					t.Fatal("quick search overflowed the terminal")
				}
			})
		}
	}
}
