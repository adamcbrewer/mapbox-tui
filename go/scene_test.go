package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestSceneOverlapAndPickingPreferActiveItem(t *testing.T) {
	var w Workspace
	body := []byte(`<gpx><rte><rtept lat="51" lon="0"/><rtept lat="51" lon="0.01"/></rte></gpx>`)
	first := openWorkspaceFile(t, &w, importFile(t, "first.gpx", body))
	second := openWorkspaceFile(t, &w, importFile(t, "second.gpx", body))
	if err := w.selectItem(first); err != nil {
		t.Fatal(err)
	}
	c := w.fitVisible(120, 40, 2)
	mid := c.screen(project(.005, 51))
	g := renderScene(
		c,
		nil,
		w.sceneItems(),
		nil,
	)
	cell := g.Cells[int(mid.Y/4)*g.Width+int(mid.X/2)]
	activeRouteHidden := cell.Role != roleActiveRoute ||
		cell.Color != w.items()[0].Color ||
		g.glyph(cell, true) != '#'
	if activeRouteHidden {
		t.Fatal("later inactive geometry hid the active route")
	}
	if ref, index := w.pick(c, mid); ref != first || index < 0 {
		t.Fatal("overlap picking did not match the active drawing priority", ref, index)
	}
	if err := w.setVisible(first, false); err != nil {
		t.Fatal(err)
	}
	g = renderScene(
		c,
		nil,
		w.sceneItems(),
		nil,
	)
	cell = g.Cells[int(mid.Y/4)*g.Width+int(mid.X/2)]
	inactiveRouteChanged := cell.Role != roleRoute ||
		cell.Color != w.items()[1].Color ||
		g.glyph(cell, true) != '*'
	if inactiveRouteChanged {
		t.Fatal("inactive route lost its distinct presentation")
	}
	if ref, _ := w.pick(c, mid); ref != second {
		t.Fatal("hidden geometry remained pickable")
	}
}

func TestScenePlannedEndpointMarkersFollowStopsAndReverse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    int
		resolved []int
		markers  []rune
	}{
		{name: "start only", count: 2, resolved: []int{0}, markers: []rune{'A'}},
		{name: "destination only", count: 2, resolved: []int{1}, markers: []rune{'B'}},
		{name: "unresolved waypoint", count: 3, resolved: []int{0, 2}, markers: []rune{'A', 'B'}},
		{name: "waypoint only", count: 3, resolved: []int{1}, markers: []rune{'W'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w Workspace
			ref := w.newJourney()
			if tc.count == 3 {
				if err := w.addJourneyWaypoint(ref); err != nil {
					t.Fatal(err)
				}
			}
			j, err := w.journey(ref)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range tc.resolved {
				if err := w.setJourneyLocation(
					ref,
					j.Stops[i].ID,
					placeLocation{Lon: float64(i) * .01, Lat: 51},
				); err != nil {
					t.Fatal(err)
				}
			}
			c := w.fitVisible(120, 40, 2)
			checkMarkers := func(reversed bool) {
				t.Helper()
				items := w.sceneItems()
				g := renderScene(
					c,
					nil,
					items,
					nil,
				)
				for i, stopIndex := range tc.resolved {
					point := c.screen(project(float64(stopIndex)*.01, 51))
					cell := g.Cells[int(point.Y/4)*g.Width+int(point.X/2)]
					want := tc.markers[i]
					if reversed {
						switch want {
						case 'A':
							want = 'B'
						case 'B':
							want = 'A'
						}
					}
					if cell.Text != want || cell.owner != ref {
						t.Fatalf("reversed=%t stop=%d: marker %q, want %q", reversed, stopIndex, cell.Text, want)
					}
					probe := renderSceneCell(
						c,
						items,
						nil,
						point,
					)
					if probe != cell {
						t.Fatal("planned marker cell probe differs from the rendered scene")
					}
				}
				if w.selection.track.Distance != 0 {
					t.Fatal("incomplete plan gained connecting distance")
				}
				if len(tc.resolved) == 2 {
					middle := c.screen(project(.01, 51))
					if cell := g.Cells[int(middle.Y/4)*g.Width+int(middle.X/2)]; cell != (Cell{}) {
						t.Fatal("unresolved waypoint gained connecting geometry", cell)
					}
				}
			}
			checkMarkers(false)
			if err := w.reverseJourney(ref); err != nil {
				t.Fatal(err)
			}
			checkMarkers(true)
			if err := w.undoJourney(ref); err != nil {
				t.Fatal(err)
			}
			checkMarkers(false)
		})
	}
}

func TestSceneRoutedEndpointsStayAtUserLocations(t *testing.T) {
	var w Workspace
	ref := routedTestJourney(t, &w)
	j, _ := w.journey(ref)
	p := newDirectionsClient("token")
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		locations := directionsRequestLocations(t, r)
		for i := range locations {
			locations[i].Lon += .04
		}
		return locationResponse(directionsTestResponse(locations, 12345, 1200)), nil
	})
	request, err := w.beginJourneyRoute(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !w.acceptJourneyRoute(request.run(p)) || !j.routeCurrent() {
		t.Fatal("route did not resolve")
	}
	c := w.fitVisible(120, 40, 2)
	g := renderScene(
		c,
		nil,
		w.sceneItems(),
		nil,
	)
	for _, tc := range []struct {
		index  int
		marker rune
	}{
		{index: 0, marker: 'A'},
		{index: len(j.Stops) - 1, marker: 'B'},
	} {
		location := j.Stops[tc.index].Location
		point := c.screen(project(location.Lon, location.Lat))
		x, y := int(math.Floor(point.X/2)), int(math.Floor(point.Y/4))
		insideX, insideY := x >= 0 && x < g.Width, y >= 0 && y < g.Height
		if !insideX || !insideY {
			t.Fatal("fit-visible cropped a user-selected endpoint", tc.marker)
		}
		cell := g.Cells[y*g.Width+x]
		if cell.Text != tc.marker || cell.owner != ref {
			t.Fatal("routed endpoint marker moved away from the user's location", tc.marker)
		}
		if j.Route.Snapped[tc.index].X == location.Lon {
			t.Fatal("routing overwrote the separate snapped location")
		}
	}
}

func addScreenItem(
	t *testing.T,
	w *Workspace,
	c Camera,
	points []XY,
	waypoints []XY,
) ItemRef {
	t.Helper()
	d := &Document{Path: fmt.Sprintf("scene-%d.gpx", len(w.documents))}
	track := d.addItem("track").Track
	for _, p := range points {
		ll := unproject(c.world(p))
		track.Points = append(track.Points, Point{Lon: ll.X, Lat: ll.Y})
	}
	if len(points) > 0 {
		track.Segments = []Segment{{Kind: "track", End: len(points)}}
	}
	for _, p := range waypoints {
		ll := unproject(c.world(p))
		track.Waypoints = append(track.Waypoints, Point{Lon: ll.X, Lat: ll.Y})
	}
	if err := d.prepare(); err != nil {
		t.Fatal(err)
	}
	return w.addDocument(d)
}

func TestScenePickingMatchesOccupiedCell(t *testing.T) {
	near := []XY{{20, 62}, {100, 62}}
	far := []XY{{20, 60.25}, {100, 60.25}}
	type geometry struct{ points, waypoints []XY }
	tests := []struct {
		name           string
		items          []geometry
		active, winner int
		role           mapRole
		text           rune
		waypoint       bool
	}{
		{
			name:   "active route",
			items:  []geometry{{points: far}, {points: near}},
			active: 0,
			winner: 0,
			role:   roleActiveRoute,
		},
		{
			name:   "later inactive route",
			items:  []geometry{{points: near}, {points: far}},
			active: -1,
			winner: 1,
			role:   roleRoute,
		},
		{
			name:     "inactive waypoint above route",
			items:    []geometry{{waypoints: []XY{{61, 60.25}}}, {points: near}},
			active:   -1,
			winner:   0,
			role:     roleMarker,
			text:     'W',
			waypoint: true,
		},
		{
			name:   "active route above inactive waypoint",
			items:  []geometry{{points: far}, {waypoints: []XY{{61, 62}}}},
			active: 0,
			winner: 0,
			role:   roleActiveRoute,
		},
		{
			name:     "waypoint above its own route",
			items:    []geometry{{points: near, waypoints: []XY{{61, 60.25}}}},
			active:   0,
			winner:   0,
			role:     roleActiveMarker,
			text:     'W',
			waypoint: true,
		},
		{
			name:   "endpoint above later route",
			items:  []geometry{{points: []XY{{20, 60.25}, {61, 60.25}}}, {points: near}},
			active: -1,
			winner: 0,
			role:   roleMarker,
			text:   'E',
		},
		{
			name:     "later inactive waypoint",
			items:    []geometry{{waypoints: []XY{{61, 62}}}, {waypoints: []XY{{61, 60.25}}}},
			active:   -1,
			winner:   1,
			role:     roleMarker,
			text:     'W',
			waypoint: true,
		},
		{
			name:   "cursor above waypoint",
			items:  []geometry{{points: []XY{{61, 60.25}, {100, 60.25}}, waypoints: []XY{{61, 62}}}},
			active: 0,
			winner: 0,
			role:   roleCursor,
			text:   '@',
		},
		{
			name: "reused colour",
			items: []geometry{
				{points: near},
				{points: []XY{{20, 8}, {100, 8}}},
				{points: []XY{{20, 12}, {100, 12}}},
				{points: []XY{{20, 16}, {100, 16}}},
				{points: []XY{{20, 20}, {100, 20}}},
				{points: []XY{{20, 24}, {100, 24}}},
				{points: far},
			},
			active: -1,
			winner: 6,
			role:   roleRoute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fit(
				nil,
				60,
				30,
				2,
			)
			c.Zoom = 12
			var w Workspace
			var refs []ItemRef
			for _, item := range tt.items {
				refs = append(refs, addScreenItem(
					t,
					&w,
					c,
					item.points,
					item.waypoints,
				))
			}
			w.clearSelection()
			if tt.active >= 0 {
				if err := w.selectItem(refs[tt.active]); err != nil {
					t.Fatal(err)
				}
			}
			var cursor *XY
			if pos := w.position(); pos != nil {
				cursor = &pos.World
			}
			g := renderScene(
				c,
				nil,
				w.sceneItems(),
				cursor,
			)
			cell := g.Cells[15*g.Width+30]
			winner := refs[tt.winner]
			wrongWinner := cell.owner != winner ||
				cell.Role != tt.role ||
				cell.Text != tt.text ||
				cell.Color != w.items()[tt.winner].Color
			if wrongWinner {
				t.Fatalf("unexpected cell winner: %+v", cell)
			}
			ref, index := w.pick(c, XY{61, 62})
			if ref != winner || (index == -1) != tt.waypoint {
				t.Fatalf(
					"picked %v point %d instead of cell owner %v (waypoint=%t)",
					ref,
					index,
					winner,
					tt.waypoint,
				)
			}
			for _, ascii := range []bool{false, true} {
				t.Run(fmt.Sprintf("ascii=%t", ascii), func(t *testing.T) {
					glyph := tt.text
					if glyph == 0 {
						glyph = rune(0x2800) + rune(cell.Bits)
						if ascii {
							glyph = mapStyles[tt.role].ascii
						}
					}
					plain := g.plain(ascii)
					if strings.Contains(plain, "\x1b") || []rune(strings.Split(plain, "\n")[15])[30] != glyph {
						t.Fatal("plain scene lost its winning glyph or included ANSI escapes")
					}
				})
			}
		})
	}
}

func TestSceneCellProbeMatchesClippedAndRotatedRendering(t *testing.T) {
	c := fit(
		nil,
		30,
		12,
		2,
	)
	c.Zoom = 12
	var w Workspace
	first := addScreenItem(
		t,
		&w,
		c,
		[]XY{{-40, 5.5}, {18.25, 24.5}, {18.5, 24.75}, {18.75, 24.5}, {100, 39.5}},
		[]XY{{18.5, 24.25}},
	)
	addScreenItem(
		t,
		&w,
		c,
		[]XY{{20, -40}, {30, 20}, {40, 90}},
		[]XY{{30, 20.5}},
	)
	for _, active := range []bool{false, true} {
		w.clearSelection()
		if active {
			if err := w.selectItem(first); err != nil {
				t.Fatal(err)
			}
			if err := w.seek(1); err != nil {
				t.Fatal(err)
			}
		}
		var cursor *XY
		if pos := w.position(); pos != nil {
			cursor = &pos.World
		}
		for _, bearing := range []float64{0, 37} {
			c.Bearing = bearing
			items := w.sceneItems()
			g := renderScene(
				c,
				nil,
				items,
				cursor,
			)
			for y := 0; y < c.Height; y++ {
				for x := 0; x < c.Width; x++ {
					point := XY{float64(x*2 + 1), float64(y*4 + 2)}
					got := renderSceneCell(
						c,
						items,
						cursor,
						point,
					)
					if want := g.Cells[y*g.Width+x]; got != want {
						t.Fatalf(
							"active=%t bearing=%g cell=(%d,%d): probe %+v; rendered %+v",
							active,
							bearing,
							x,
							y,
							got,
							want,
						)
					}
				}
			}
		}
	}
}

func TestWorkspacePickingKeepsNearestGeometryInEmptyCells(t *testing.T) {
	c := fit(
		nil,
		60,
		30,
		2,
	)
	c.Zoom = 12
	var w Workspace
	active := addScreenItem(
		t,
		&w,
		c,
		[]XY{{20, 58}, {100, 58}},
		nil,
	)
	nearer := addScreenItem(
		t,
		&w,
		c,
		[]XY{{20, 66}, {100, 66}},
		nil,
	)
	if err := w.selectItem(active); err != nil {
		t.Fatal(err)
	}
	point := XY{61, 62.5}
	cell := renderSceneCell(
		c,
		w.sceneItems(),
		nil,
		point,
	)
	if cell.owner != (ItemRef{}) {
		t.Fatal("nearest-geometry regression does not click an empty cell", cell)
	}
	if ref, index := w.pick(c, point); ref != nearer || index < 0 {
		t.Fatal("active precedence replaced nearest-geometry picking in an empty cell", ref, index)
	}
	pin := addScreenItem(
		t,
		&w,
		c,
		nil,
		[]XY{{81, 65.25}},
	)
	if ref, index := w.pick(c, XY{81, 62.5}); ref != pin || index != -1 {
		t.Fatal("nearby waypoint lost nearest-geometry picking outside its marker cell", ref, index)
	}
	if ref, _ := w.pick(c, XY{61, 90}); ref != (ItemRef{}) {
		t.Fatal("empty-cell picking lost its distance tolerance", ref)
	}
}

func TestSceneCursorPriorityIsIndependentOfRoleOrdinal(t *testing.T) {
	g := newGrid(1, 1)
	g.label(
		XY{},
		"@",
		roleCursor,
		false,
	)
	g.dot(0, 0, roleActiveRoute)
	g.label(
		XY{},
		"S",
		roleActiveMarker,
		false,
	)
	if g.Cells[0].Text != '@' || g.Cells[0].Role != roleCursor {
		t.Fatal("a higher enum value overwrote the cursor despite lower display priority")
	}
}

func TestWorkspacePickingSelectsWaypointAndNeverConnectsFiles(t *testing.T) {
	var w Workspace
	left := openWorkspaceFile(t, &w, importFile(
		t,
		"left.gpx",
		[]byte(`<gpx><rte><rtept lat="0" lon="-1"/><rtept lat="0" lon="-.9"/></rte></gpx>`),
	))
	openWorkspaceFile(t, &w, importFile(
		t,
		"right.gpx",
		[]byte(`<gpx><rte><rtept lat="0" lon=".9"/><rtept lat="0" lon="1"/></rte></gpx>`),
	))
	c := w.fitVisible(120, 40, 2)
	if ref, _ := w.pick(c, c.screen(project(0, 0))); ref != (ItemRef{}) {
		t.Fatal("picked an invented connection between files", ref, left)
	}
	pin := openWorkspaceFile(t, &w, importFile(t, "pin.gpx", []byte(`<gpx><wpt lat="0" lon="0"/></gpx>`)))
	if ref, index := w.pick(c, c.screen(project(0, 0))); ref != pin || index != -1 {
		t.Fatal("waypoint item did not preserve its identity", ref, index)
	}
}

func TestWideExtentRenderAndZoomStayBelowZeroWithoutJumping(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	left := importFile(t, "left.gpx", []byte(`<gpx><wpt lat="0" lon="-170"/></gpx>`))
	right := importFile(t, "right.gpx", []byte(`<gpx><wpt lat="0" lon="170"/></gpx>`))
	width, height := 30, 12
	result, err := renderFiles(
		context.Background(),
		[]string{left, right},
		Args{Width: &width, Height: &height},
		newProvider("none"),
	)
	if err != nil || result["scene"].(map[string]any)["zoom"].(float64) >= 0 {
		t.Fatal("wide multi-file render could not fit a narrow viewport", err)
	}
	c := fitBounds(
		[4]float64{-170, 0, 170, 0},
		width,
		height,
		2,
	)
	before := c.Zoom
	anchor := XY{float64(width), float64(height * 2)}
	world := c.world(anchor)
	c.zoomAt(anchor, zoomStep)
	if math.Abs(c.Zoom-before-zoomStep) > 1e-12 || c.world(anchor) != world {
		t.Fatal("zooming a fitted negative zoom jumped to zero or moved its anchor")
	}
}
