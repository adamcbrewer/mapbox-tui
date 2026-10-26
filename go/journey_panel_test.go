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

func TestJourneyPanelOverlaysMapAndCollapsesAtEverySize(t *testing.T) {
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
			if !strings.Contains(text, "Context · Files · Journey") || strings.Contains(text, "Journey Planner |") {
				t.Fatal("planner tabs missing or redundant title retained", size, ascii)
			}
			camera := m.camera
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			m.rebuild()
			if m.camera != camera || m.journeyPanelVisible() {
				t.Fatal("collapsing planner changed the camera", size)
			}
			text = ansi.Strip(m.content)
			g := renderScene(m.camera, nil, m.workspace.sceneItems(), nil)
			rows := strings.Split(text, "\n")
			for y := range g.Height {
				row := []rune(rows[m.mapOriginY()+y])
				actual := string(row[:m.camera.Width])
				expected := ansi.Strip(m.gridRow(g, y, ascii))
				if actual != expected {
					t.Fatal("planner covered the live map", size, ascii, y)
				}
			}
			m.editJourneyLocation(0)
			m.rebuild()
			if m.cursor == nil || m.cursor.Y < m.journeyPanelY()+1 || m.cursor.X >= m.width {
				t.Fatal("location cursor escaped planner panel", size, ascii, m.cursor)
			}
			if m.journeySidebar() && m.cursor.X < m.journeyPanelX()+2 {
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
		if m.width < 70 {
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		}
		y := m.mapOriginY()
		m.Update(tea.MouseWheelMsg{X: 3, Y: y, Button: tea.MouseWheelUp})
		if m.camera.Zoom <= zoom || m.planner.row != row {
			t.Fatal("map wheel scrolled the planner or could not zoom", size)
		}
		m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 3, Y: y, Button: tea.MouseLeft})
		j, _ := m.workspace.journey(ref)
		if j.Stops[0].Location != nil || m.inspected == nil || m.ui.dialog != dialogJourney {
			t.Fatal("ordinary map click edited a stop instead of inspecting", size)
		}
		center := m.camera.Center
		m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseMotionMsg{X: 8, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 8, Y: y, Button: tea.MouseLeft})
		if m.camera.Center == center || m.planner.row != row {
			t.Fatal("dragging the map did not preserve planner navigation", size)
		}
		center = m.camera.Center
		if !m.planner.collapsed {
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		if m.camera.Center == center || m.planner.row != row {
			t.Fatal("Ctrl-B did not enable keyboard map navigation", size)
		}
		m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		if m.planner.row != 2+len(journeyActions) {
			t.Fatal("Ctrl-B did not return to planner navigation", size)
		}
		panelX, panelY := 2, 2
		if m.journeySidebar() {
			panelX = m.journeyPanelX() + 2
		}
		zoom = m.camera.Zoom
		m.Update(tea.MouseWheelMsg{X: panelX, Y: panelY, Button: tea.MouseWheelDown})
		if m.planner.row != 1 || m.camera.Zoom != zoom {
			t.Fatal("planner wheel zoomed the map", size)
		}
		m.editJourneyLocation(0)
		m.startJourneyPick()
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
	if !strings.Contains(ansi.Strip(m.content), "docs/MAPBOX.md") {
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
			if err := m.workspace.selectItem(m.planner.ref); err != nil {
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
			clickJourneyRow(t, m, "No waypoints")
		} else {
			clickJourneyRow(t, m, tc.label)
		}
		if m.ui.dialog != dialogJourneyLocation || (!m.planner.addingWaypoint && m.planner.stopID != j.Stops[tc.index].ID) {
			t.Fatal("mouse did not open the chosen location", tc.label)
		}
		m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
		x, y := m.camera.Width/2+3, m.mapOriginY()+m.camera.Height/2
		geo := unproject(m.camera.world(XY{float64(x*2 + 1), float64((y-m.mapOriginY())*4 + 2)}))
		cmd := clickJourneyMap(m, x, y)
		location := j.Stops[tc.index].Location
		if location == nil || location.Source != placeSourceMapPoint ||
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
	clickJourneyRow(t, m, "Offline · straight lines")
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

func TestJourneyPanelSectionsAndInsetTitle(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	m.rebuild()
	rows := strings.Split(ansi.Strip(m.journeyPanel()), "\n")
	if !strings.Contains(rows[0], "Context · Files · Journey") || len(rows) != m.camera.Height+1-m.journeyPanelY() {
		t.Fatal("planner title or vertical inset is incorrect")
	}
	text := strings.Join(rows, "\n")
	if strings.Contains(text, "set all locations") || strings.Contains(text, "works offline") {
		t.Fatal("planner repeats its mode in a non-selectable status")
	}
	start, empty, end := -1, -1, -1
	for i, row := range rows {
		switch {
		case strings.Contains(row, "A Start:"):
			start = i
		case strings.Contains(row, "No waypoints"):
			empty = i
		case strings.Contains(row, "B Destination:"):
			end = i
		}
	}
	if start < 0 || empty != start+2 || end != empty+2 {
		t.Fatal("start, empty waypoints and destination are not separated by space")
	}
	if !strings.Contains(rows[len(rows)-3], "del remove / alt+up/down order") {
		t.Fatal("stop editing hints are not anchored to the footer")
	}
	// Blank rows must not open a stop or shift mouse targets.
	for _, y := range []int{start + 2, empty + 2} {
		m.clickJourneyPanelRow(m.journeyPanelX()+2, y+m.journeyPanelY()-1)
		if m.ui.dialog != dialogJourney {
			t.Fatal("non-selectable spacing opened an editor")
		}
	}
	m.clickJourneyPanelRow(m.journeyPanelX()+2, end+m.journeyPanelY())
	if m.ui.dialog != dialogJourneyLocation || m.planner.row != 1 {
		t.Fatal("destination click targeted the wrong stop after adding spacing")
	}
}

func TestInlineWaypointEntryKeepsDestinationAndSupportsSuccessiveStops(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	enterJourneyCoordinate(m, 0, "51.5, -0.1")
	enterJourneyCoordinate(m, 1, "51.6, -0.2")
	j, _ := m.workspace.journey(m.planner.ref)
	destination := j.Stops[1]
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	for i, value := range []string{"51.52, -0.12", "51.54, -0.14"} {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m.rebuild()
		label := "> No waypoints"
		if i > 0 {
			label = "> Add another waypoint"
		}
		if !strings.Contains(ansi.Strip(m.content), label) {
			t.Fatal("inline waypoint row is not keyboard-selectable", label)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if m.ui.dialog != dialogJourneyLocation || m.planner.row != i+1 {
			t.Fatal("inline waypoint did not open its location editor")
		}
		m.ui.search.SetValue(value)
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if len(j.Stops) != i+3 || j.Stops[i+1].Location == nil || j.Stops[len(j.Stops)-1] != destination {
			t.Fatal("adding a waypoint lost its location or changed the destination")
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.planner.row != len(j.Stops)-1 {
		t.Fatal("destination is not reachable immediately after the inline add row")
	}
	m.rebuild()
	if strings.Contains(ansi.Strip(m.content), "Show journey on map") ||
		strings.Contains(ansi.Strip(m.content), "Add waypoint (ctrl+n)") {
		t.Fatal("redundant actions remain in the planner")
	}
}

func TestJourneyLocationEntryIsImmediateAndUncommittedUntilChosen(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "51.3,-2.5" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.ui.search.Value() != "51.3,-2.5" {
		t.Fatal("location entry needed a second activation before typing")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	enterJourneyCoordinate(m, 1, "51.4,-2.6")
	j, _ := m.workspace.journey(m.planner.ref)
	before, history := j.revision, len(j.history)
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if len(j.Stops) != 2 || j.revision != before || len(j.history) != history || !j.hasCompleteLocations() {
		t.Fatal("cancelling a new waypoint changed the existing journey")
	}
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	clickJourneyMap(m, 5, m.mapOriginY()+2)
	if len(j.Stops) != 3 || j.Stops[1].Location == nil || m.ui.dialog != dialogJourney {
		t.Fatal("map click from the input did not commit the new waypoint")
	}
	m.editJourneyLocation(0)
	clickJourneyMap(m, 8, m.mapOriginY()+3)
	if m.ui.dialog != dialogJourney || j.Stops[0].Location.Source != placeSourceMapPoint {
		t.Fatal("map click from the input did not update the start")
	}
}

func TestSidebarShiftTabCyclesBackwards(t *testing.T) {
	m := journeyViewer(t)
	m.ui.detailsOpen = true
	for _, want := range []dialogKind{dialogJourney, dialogWorkspace, dialogNone} {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		if m.ui.dialog != want || !m.panelVisible() {
			t.Fatal("Shift-Tab did not select the previous visible tab", want, m.ui.dialog)
		}
	}
}
