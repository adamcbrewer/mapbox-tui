package main

import (
	tea "charm.land/bubbletea/v2"
	"math"
	"testing"
)

func testWorkspace(track *Track) Workspace {
	var w Workspace
	w.addDocument(&Document{
		Path:   "test.gpx",
		Name:   track.Name,
		Bounds: track.Bounds,
		Items:  []*DocumentItem{{ID: 1, Kind: "track", Track: track}},
	})
	return w
}

func TestDragMovesOneCellWithoutFirstMotionJumpAndStopsOnRelease(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	c := fit(track, 88, 34, 2)
	m := &viewer{workspace: testWorkspace(track), camera: c}
	m.Update(tea.MouseClickMsg{X: 20, Y: 10, Button: tea.MouseLeft})
	if m.camera.Center != c.Center {
		t.Fatal("press moved map")
	}
	want := c
	want.pan(2, 0)
	m.Update(tea.MouseMotionMsg{X: 21, Y: 10, Button: tea.MouseLeft})
	if math.Hypot(m.camera.Center.X-want.Center.X, m.camera.Center.Y-want.Center.Y) > 1e-12 {
		t.Fatal("first motion jumped")
	}
	m.Update(tea.MouseReleaseMsg{X: 21, Y: 10, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 40, Y: 20, Button: tea.MouseLeft})
	if m.camera.Center != want.Center {
		t.Fatal("pan continued after release")
	}
}
