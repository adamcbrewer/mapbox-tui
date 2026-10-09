package main

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestJourneyPanelKeepsMapVisibleAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}, {50, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			m := journeyViewer(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.settings.ASCII = ascii
			m.openJourney()
			enterJourneyCoordinate(m, 0, "51.5, -0.1")
			enterJourneyCoordinate(m, 1, "51.6, -0.2")
			m.rebuild()
			if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
				t.Fatal("planner panel overflowed", size, ascii)
			}
			text := ansi.Strip(m.content)
			if !strings.Contains(text, "Journey Planner | Manual") {
				t.Fatal("planner/current mode missing", size, ascii)
			}
			g := renderScene(m.camera, nil, m.workspace.sceneItems(), nil)
			rows := strings.Split(text, "\n")
			for y := range g.Height {
				row := []rune(rows[m.mapOriginY()+y])
				actual := string(row[1 : m.camera.Width+1])
				expected := ansi.Strip(m.gridRow(g, y, ascii))
				if actual != expected {
					t.Fatal("planner covered the live map", size, ascii, y)
				}
			}
			m.editJourneyLocation(0)
			m.rebuild()
			if m.cursor == nil || m.cursor.Y != 3 || m.cursor.X >= m.width {
				t.Fatal("location cursor escaped planner panel", size, ascii, m.cursor)
			}
			if m.journeySidebar() && m.cursor.X < m.camera.Width+6 {
				t.Fatal("sidebar input cursor appeared over the map", size)
			}
		}
	}
}

func TestJourneyPanelSearchSelectionStaysVisible(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		imported      bool
	}{
		{width: 30, height: 12},
		{width: 30, height: 12, imported: true},
		{width: 50, height: 18},
		{width: 70, height: 15, imported: true},
		{width: 70, height: 18, imported: true},
	} {
		for _, ascii := range []bool{false, true} {
			name := fmt.Sprintf("%dx%d-imported-%t-ascii-%t", tc.width, tc.height, tc.imported, ascii)
			t.Run(name, func(t *testing.T) {
				m := journeyViewer(t)
				m.Update(tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
				m.settings.ASCII = ascii
				if tc.imported {
					if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
						t.Fatal(err)
					}
				}
				m.openJourney()
				m.editJourneyLocation(0)
				m.places.token = "token"
				m.places.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
					return locationResponse(`{"suggestions":[{"mapbox_id":"first","name":"First Place"},{"mapbox_id":"second","name":"Second Place"}]}`), nil
				})
				m.ui.search.SetValue("Place")
				m.schedulePlaceSearch()
				m.Update(m.requestPlaceSearch(m.planner.searchID)())
				for i, label := range []string{"First Place", "Second Place"} {
					if i > 0 {
						m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
					}
					for _, notice := range []string{"", "Search feedback"} {
						m.ui.notice = notice
						m.rebuild()
						if !strings.Contains(ansi.Strip(m.content), label) {
							t.Fatal("selected search suggestion hidden", label, notice)
						}
						if lipgloss.Width(m.content) > tc.width || lipgloss.Height(m.content) > tc.height {
							t.Fatal("search panel overflowed the terminal", notice)
						}
					}
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
				m.rebuild()
				if lipgloss.Height(m.content) > tc.height {
					t.Fatal("returning to the planner kept the location-editor layout")
				}
			})
		}
	}
}

func TestJourneyPanelMapAndListHaveIndependentNavigation(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		m := journeyViewer(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.openJourney()
		ref := m.planner.ref
		row, zoom := m.planner.row, m.camera.Zoom
		y := m.mapOriginY()
		m.Update(tea.MouseWheelMsg{X: 3, Y: y, Button: tea.MouseWheelUp})
		if m.camera.Zoom <= zoom || m.planner.row != row {
			t.Fatal("map wheel scrolled the planner or could not zoom", size)
		}
		m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 3, Y: y, Button: tea.MouseLeft})
		j, _ := m.workspace.journey(ref)
		if j.Stops[0].Location == nil || m.workspace.selection.ref != ref || m.ui.dialog != dialogJourney {
			t.Fatal("map point selection closed the planner or changed its item", size)
		}
		center := m.camera.Center
		m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseMotionMsg{X: 8, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 8, Y: y, Button: tea.MouseLeft})
		if m.camera.Center == center || m.planner.row != row {
			t.Fatal("dragging the map did not preserve planner navigation", size)
		}
		center = m.camera.Center
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		if m.camera.Center == center || m.planner.row != row {
			t.Fatal("Tab did not enable keyboard map navigation", size)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		if m.planner.row != row+1 {
			t.Fatal("Tab did not return to planner navigation", size)
		}
		panelX, panelY := 2, 3
		if m.journeySidebar() {
			panelX = m.camera.Width + 6
		}
		zoom = m.camera.Zoom
		m.Update(tea.MouseWheelMsg{X: panelX, Y: panelY, Button: tea.MouseWheelDown})
		if m.planner.row != row+2 || m.camera.Zoom != zoom {
			t.Fatal("planner wheel zoomed the map", size)
		}
		m.editJourneyLocation(0)
		y = m.mapOriginY()
		center, value := m.camera.Center, m.ui.search.Value()
		m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseMotionMsg{X: 8, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 8, Y: y, Button: tea.MouseLeft})
		if m.camera.Center == center || m.ui.search.Value() != value || m.ui.dialog != dialogJourneyLocation {
			t.Fatal("map could not be panned during location entry", size)
		}
	}
}

func TestJourneyPanelFeedbackStaysScrolledAfterRedraw(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	m.openDialog(dialogJourneyRoutingHelp)
	m.rebuild()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.content), "Routes stay in memory") {
		t.Fatal("redrawing the planner reset feedback scrolling")
	}
}

func TestJourneyViewIncludesMileContextAndEntirePlan(t *testing.T) {
	for _, latitude := range []float64{0, 51.5, 80} {
		for _, size := range [][2]int{{76, 34}, {30, 12}, {28, 3}} {
			m := journeyViewer(t)
			m.openJourney()
			j, _ := m.workspace.journey(m.planner.ref)
			if err := m.workspace.setJourneyLocation(m.planner.ref, j.Stops[0].ID,
				placeLocation{Lon: -.1, Lat: latitude}); err != nil {
				t.Fatal(err)
			}
			view := Camera{Width: size[0], Height: size[1], Aspect: 2}
			camera := journeyView(j, m.activeTrack(), view)
			center := Point{Lon: -.1, Lat: latitude}
			for _, edge := range []XY{
				{float64(camera.Width), 0}, {float64(camera.Width), float64(camera.Height * 4)},
				{0, float64(camera.Height * 2)}, {float64(camera.Width * 2), float64(camera.Height * 2)},
			} {
				location := unproject(camera.world(edge))
				distance := haversine(center, Point{Lon: location.X, Lat: location.Y})
				if distance < 1609 {
					t.Fatalf("location lacked mile-radius context at latitude %.1f, size %v: %.0f m", latitude, size, distance)
				}
			}
			if err := m.workspace.setJourneyLocation(m.planner.ref, j.Stops[1].ID,
				placeLocation{Lon: .4, Lat: latitude}); err != nil {
				t.Fatal(err)
			}
			camera = journeyView(j, m.activeTrack(), view)
			for _, point := range m.activeTrack().Points {
				screen := camera.screen(point.World)
				outside := screen.X < 0 || screen.X > float64(camera.Width*2)
				outside = outside || screen.Y < 0 || screen.Y > float64(camera.Height*4)
				if outside {
					t.Fatal("mile context cropped the longer plan", latitude, size)
				}
			}
		}
	}
}

func clickJourneyMap(m *viewer, x, y int) tea.Cmd {
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return cmd
}

func clickJourneyRow(t *testing.T, m *viewer, label string) tea.Cmd {
	t.Helper()
	m.rebuild()
	for y, line := range strings.Split(ansi.Strip(m.content), "\n") {
		if index := strings.Index(line, label); index >= 0 {
			x := len([]rune(line[:index]))
			_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			return cmd
		}
	}
	t.Fatalf("planner row %q not visible", label)
	return nil
}

func TestJourneyMousePlacesEndpointsAndWaypointAndRecalculates(t *testing.T) {
	m := journeyViewer(t)
	m.directions.token = "token"
	var requests int
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 100, 100)), nil
	})
	t.Cleanup(m.workspace.cancelJourneyRoutes)
	m.openJourney()
	j, _ := m.workspace.journey(m.planner.ref)
	for _, tc := range []struct {
		label string
		index int
	}{
		{label: "A Start", index: 0}, {label: "B Destination", index: 1}, {label: "1 Waypoint", index: 1},
	} {
		if tc.label == "1 Waypoint" {
			clickJourneyRow(t, m, "Add waypoint")
		}
		clickJourneyRow(t, m, tc.label)
		if m.ui.dialog != dialogJourneyLocation || m.planner.stopID != j.Stops[tc.index].ID {
			t.Fatal("mouse did not open the chosen location", tc.label)
		}
		x, y := m.camera.Width/2+3, m.mapOriginY()+m.camera.Height/2
		geo := unproject(m.camera.world(XY{float64((x-1)*2 + 1), float64((y-m.mapOriginY())*4 + 2)}))
		cmd := clickJourneyMap(m, x, y)
		location := j.Stops[tc.index].Location
		if location == nil || location.Source != "map point" ||
			math.Abs(location.Lon-geo.X)+math.Abs(location.Lat-geo.Y) > 1e-10 {
			t.Fatal("mouse point did not commit the correct map coordinate", tc.label)
		}
		if m.ui.dialog != dialogJourney || m.dragging {
			t.Fatal("mouse placement did not return to the planner", tc.label)
		}
		if tc.index == 0 {
			if cmd != nil || requests != 0 {
				t.Fatal("first mouse point routed an incomplete plan")
			}
		} else {
			if cmd == nil {
				t.Fatal("mouse point did not automatically recalculate")
			}
			m.Update(cmd())
			if !j.routeCurrent() {
				t.Fatal("mouse-selected plan did not resolve")
			}
		}
	}
	if requests != 2 || len(j.Stops) != 3 {
		t.Fatal("mouse workflow routed more than once per completed plan", requests)
	}
	clickJourneyRow(t, m, "Travel mode:")
	clickJourneyRow(t, m, "Manual · offline")
	if j.Mode != journeyManual {
		t.Fatal("travel mode was not mouse-selectable")
	}
}

func TestJourneyMapDragDoesNotPlaceOrReplaceLocation(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	m.editJourneyLocation(0)
	j, _ := m.workspace.journey(m.planner.ref)
	y := m.mapOriginY() + 1
	m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 8, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 8, Y: y, Button: tea.MouseLeft})
	if j.Stops[0].Location != nil || m.ui.dialog != dialogJourneyLocation {
		t.Fatal("dragging the map placed a stop")
	}
}

func TestJourneyRowNavigationPreservesLocationContextAnimation(t *testing.T) {
	m := journeyViewer(t)
	m.settings.NoAnimation = false
	m.openJourney()
	enterJourneyCoordinate(m, 0, "51.5, -0.1")
	if !m.motion.Active {
		t.Fatal("location context did not start fitting")
	}
	target := m.motion.Target
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !m.motion.Active || m.motion.Target != target {
		t.Fatal("selecting the destination interrupted the location's context zoom")
	}
}
