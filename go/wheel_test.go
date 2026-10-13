package main

import (
	"math"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestWheelZoomUsesFineStepsAndLimitsBursts(t *testing.T) {
	now := time.Unix(100, 0)
	var wheel wheelZoom
	if delta := wheel.step(-1, now); math.Abs(delta+0.1) > 1e-12 {
		t.Fatalf("single step: %v", delta)
	}
	rest := 0.0
	for range 7 {
		rest += wheel.step(-1, now)
	}
	if math.Abs(rest+0.1) > 1e-12 {
		t.Fatalf("burst remainder: %v", rest)
	}
	if wheel.step(-1, now) != 0 {
		t.Fatal("exhausted burst still zoomed")
	}
	if wheel.step(-1, now.Add(20*time.Millisecond)) != 0 {
		t.Fatal("emitted a partial zoom step")
	}
	if delta := wheel.step(-1, now.Add(40*time.Millisecond)); math.Abs(delta+0.1) > 1e-12 {
		t.Fatalf("sustained scroll: %v", delta)
	}
	if delta := wheel.step(-1, now.Add(200*time.Millisecond)); math.Abs(delta+0.1) > 1e-12 {
		t.Fatalf("fresh scroll: %v", delta)
	}
}

func TestWheelBurstAndImmediateReversePreservePointerAnchor(t *testing.T) {
	now := time.Unix(100, 0)
	m := &viewer{camera: Camera{project(-0.1, 51.5), 14, 45, 2.4, 120, 40}}
	pointer := XY{33, 117}
	before := m.camera.world(pointer)
	for range 8 {
		m.zoomWheel(pointer, -1, 0, now)
	}
	if math.Abs(m.camera.Zoom-13.8) > 1e-12 {
		t.Fatalf("burst jumped to %v", m.camera.Zoom)
	}
	m.zoomWheel(pointer, 1, 0, now)
	if math.Abs(m.camera.Zoom-13.9) > 1e-12 {
		t.Fatal("direction reversal delayed")
	}
	after := m.camera.world(pointer)
	if math.Hypot(before.X-after.X, before.Y-after.Y) > 1e-12 {
		t.Fatal("wheel burst moved anchor")
	}
}

func TestModifiedWheelDoublesZoomAndPreservesPointerAnchor(t *testing.T) {
	for _, modifier := range []struct {
		mod  tea.KeyMod
		step float64
	}{{0, 0.1}, {tea.ModCtrl, 0.2}, {tea.ModSuper, 0.2}, {tea.ModMeta, 0.2}, {tea.ModCtrl | tea.ModSuper, 0.2}, {tea.ModCtrl | tea.ModShift, 0.2}, {tea.ModAlt | tea.ModShift, 0.1}} {
		for _, button := range []tea.MouseButton{tea.MouseWheelUp, tea.MouseWheelDown} {
			m := &viewer{camera: Camera{project(-0.1, 51.5), 14, 45, 2.4, 120, 40}}
			pointer := XY{39, float64((10-m.mapOriginY())*4 + 2)}
			before := m.camera.world(pointer)
			m.Update(tea.MouseWheelMsg{X: 20, Y: 10, Button: button, Mod: modifier.mod})
			want := 14 + modifier.step
			if button == tea.MouseWheelDown {
				want = 14 - modifier.step
			}
			if math.Abs(m.camera.Zoom-want) > 1e-12 {
				t.Fatalf("modifier %v, button %v: zoom %v, want %v", modifier.mod, button, m.camera.Zoom, want)
			}
			after := m.camera.world(pointer)
			if math.Hypot(before.X-after.X, before.Y-after.Y) > 1e-12 {
				t.Fatal("modified wheel zoom moved the pointer anchor")
			}
		}
	}
}

func TestModifiedWheelDoublesBurstAndSustainedZoomWithoutResettingBudget(t *testing.T) {
	now := time.Unix(100, 0)
	pointer := XY{33, 117}
	normal := &viewer{camera: Camera{project(-0.1, 51.5), 14, 45, 2.4, 120, 40}}
	fast := &viewer{camera: normal.camera}
	for range 8 {
		normal.zoomWheel(pointer, -1, 0, now)
		fast.zoomWheel(pointer, -1, tea.ModCtrl, now)
	}
	if math.Abs(fast.camera.Zoom-13.6) > 1e-12 {
		t.Fatalf("modified burst escaped the limiter: %v", fast.camera.Zoom)
	}
	for i := 1; i <= 10; i++ {
		at := now.Add(time.Duration(i) * 40 * time.Millisecond)
		normal.zoomWheel(pointer, -1, 0, at)
		fast.zoomWheel(pointer, -1, tea.ModCtrl, at)
	}
	if math.Abs((14-fast.camera.Zoom)-2*(14-normal.camera.Zoom)) > 1e-12 {
		t.Fatal("modified sustained scrolling was not twice as fast")
	}
	before := fast.camera.Zoom
	fast.zoomWheel(pointer, -1, 0, now.Add(400*time.Millisecond))
	fast.zoomWheel(pointer, -1, tea.ModSuper, now.Add(400*time.Millisecond))
	if fast.camera.Zoom != before {
		t.Fatal("changing modifiers reset the scroll budget")
	}
}

func TestHorizontalWheelDoesNotZoomGoViewer(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	c := fit(track, 88, 34, 2)
	m := &viewer{workspace: testWorkspace(track), camera: c}
	for _, button := range []tea.MouseButton{tea.MouseWheelLeft, tea.MouseWheelRight} {
		m.Update(tea.MouseWheelMsg{X: 20, Y: 10, Button: button})
	}
	if m.camera != c || m.dirty {
		t.Fatal("horizontal scrolling zoomed map")
	}
}
