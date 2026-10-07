package main

import (
	"context"
	"math"
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
	g := renderScene(c, nil, w.sceneItems(), nil)
	cell := g.Cells[int(mid.Y/4)*g.Width+int(mid.X/2)]
	if cell.Role != roleActiveRoute || g.glyph(cell, true) != '#' {
		t.Fatal("later inactive geometry hid the active route")
	}
	if ref, index := w.pick(c, mid); ref != first || index < 0 {
		t.Fatal("overlap picking did not match the active drawing priority", ref, index)
	}
	if err := w.setVisible(first, false); err != nil {
		t.Fatal(err)
	}
	g = renderScene(c, nil, w.sceneItems(), nil)
	cell = g.Cells[int(mid.Y/4)*g.Width+int(mid.X/2)]
	if cell.Role != roleRoute || g.glyph(cell, true) != '*' {
		t.Fatal("inactive route lost its distinct presentation")
	}
	if ref, _ := w.pick(c, mid); ref != second {
		t.Fatal("hidden geometry remained pickable")
	}
}

func TestSceneCursorPriorityIsIndependentOfRoleOrdinal(t *testing.T) {
	g := newGrid(1, 1)
	g.label(XY{}, "@", roleCursor, false)
	g.dot(0, 0, roleActiveRoute)
	g.label(XY{}, "S", roleActiveMarker, false)
	if g.Cells[0].Text != '@' || g.Cells[0].Role != roleCursor {
		t.Fatal("a higher enum value overwrote the cursor despite lower display priority")
	}
}

func TestWorkspacePickingSelectsWaypointAndNeverConnectsFiles(t *testing.T) {
	var w Workspace
	left := openWorkspaceFile(t, &w, importFile(t, "left.gpx", []byte(`<gpx><rte><rtept lat="0" lon="-1"/><rtept lat="0" lon="-.9"/></rte></gpx>`)))
	openWorkspaceFile(t, &w, importFile(t, "right.gpx", []byte(`<gpx><rte><rtept lat="0" lon=".9"/><rtept lat="0" lon="1"/></rte></gpx>`)))
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
	result, err := renderFiles(context.Background(), []string{left, right}, Args{Width: &width, Height: &height}, newProvider("none"))
	if err != nil || result["scene"].(map[string]any)["zoom"].(float64) >= 0 {
		t.Fatal("wide multi-file render could not fit a narrow viewport", err)
	}
	c := fitBounds([4]float64{-170, 0, 170, 0}, width, height, 2)
	before := c.Zoom
	anchor := XY{float64(width), float64(height * 2)}
	world := c.world(anchor)
	c.zoomAt(anchor, zoomStep)
	if math.Abs(c.Zoom-before-zoomStep) > 1e-12 || c.world(anchor) != world {
		t.Fatal("zooming a fitted negative zoom jumped to zero or moved its anchor")
	}
}
