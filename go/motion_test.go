package main

import (
	"math"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestCameraMotionSettlesWithoutOvershoot(t *testing.T) {
	c := Camera{Center: XY{.5, .4}, Zoom: 13, Aspect: 2, Width: 88, Height: 34}
	target := c
	target.pan(32, -16)
	motion := cameraMotion{Target: target, Active: true}
	previous := math.Hypot(c.Center.X-target.Center.X, c.Center.Y-target.Center.Y)
	for range 60 {
		motion.advance(&c, 1.0/60)
		gap := math.Hypot(c.Center.X-target.Center.X, c.Center.Y-target.Center.Y)
		if gap > previous+1e-12 || c.Center.X < target.Center.X || c.Center.Y > target.Center.Y {
			t.Fatal("camera spring overshot or moved away from its target")
		}
		previous = gap
	}
	if motion.Active || c != target {
		t.Fatal("camera did not settle exactly at its target")
	}
}

func TestNavigationRetargetsMotionWithoutPausingPlayback(t *testing.T) {
	track, err := loadTrack("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m := &viewer{
		workspace: testWorkspace(track),
		camera: fit(
			track,
			88,
			34,
			2,
		),
		width:     120,
		height:    40,
		following: true,
		lastTick:  time.Now(),
	}
	m.initUI()
	m.activePlayback().toggle()
	initial := m.camera
	m.Update(tea.KeyPressMsg{Code: 'h'})
	first := m.destination()
	m.Update(tea.KeyPressMsg{Code: 'h'})
	panInterruptedPlayback := !m.activePlayback().Playing ||
		m.following ||
		!m.motion.Active ||
		m.camera != initial ||
		m.destination().Center.X >= first.Center.X
	if panInterruptedPlayback {
		t.Fatal("keyboard pan paused playback, snapped, or lost repeated input")
	}
	m.Update(tea.MouseWheelMsg{X: 20, Y: 10, Button: tea.MouseWheelUp})
	if !m.activePlayback().Playing || m.motion.Active {
		t.Fatal("wheel zoom paused playback or left an old camera target active")
	}
	m.Update(tea.MouseClickMsg{X: 20, Y: 10, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 21, Y: 10, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 110, Y: 10, Button: tea.MouseLeft})
	if !m.activePlayback().Playing || m.dragging {
		t.Fatal("drag paused playback or remained active after release outside the map")
	}
	m.Update(tea.KeyPressMsg{Code: 'c'})
	followNotResumed := !m.activePlayback().Playing || !m.following || m.destination().Center != m.position().World
	if followNotResumed {
		t.Fatal("center did not resume follow while keeping playback running")
	}
	m.Update(tea.KeyPressMsg{Code: ']'})
	if m.activePlayback().Playing {
		t.Fatal("point seeking no longer pauses playback")
	}
}

func TestDialogsCaptureNavigationAndStayInsideTerminal(t *testing.T) {
	track, err := loadTrack("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{120, 40}, {70, 24}, {30, 12}} {
		for _, kind := range []dialogKind{
			dialogHelp, dialogCredits, dialogFiles, dialogCommands, dialogSearch, dialogSpeed, dialogInspect, dialogWorkspace,
		} {
			m := &viewer{
				workspace: testWorkspace(track),
				camera: fit(
					track,
					88,
					34,
					2,
				),
				provider: &Provider{Kind: "none"},
			}
			m.initUI()
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.openDialog(kind)
			before := m.camera
			m.Update(tea.KeyPressMsg{Code: 'h'})
			if m.camera != before || m.motion.Active {
				t.Fatal("dialog navigation reached the map")
			}
			m.rebuild()
			if lipgloss.Height(m.content) > size[1] {
				t.Fatalf("dialog %d height %d exceeds terminal height at %v", kind, lipgloss.Height(m.content), size)
			}
			if lipgloss.Width(m.content) > size[0] {
				t.Fatalf("dialog %d exceeds terminal width at %v", kind, size)
			}
		}
	}
}

func TestCommandActionsAndSidebarFocusKeepPlaybackControlsWorking(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	track, err := loadTrack("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m := &viewer{
		workspace: testWorkspace(track),
		camera: fit(
			track,
			88,
			34,
			2,
		),
		width:     120,
		height:    40,
		following: true,
		lastTick:  time.Now(),
	}
	m.initUI()
	m.activePlayback().toggle()
	m.openDialog(dialogCommands)
	m.ui.search.SetValue("fit all")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	fitInterruptedPlayback := m.ui.dialog != dialogNone || !m.activePlayback().Playing || m.following
	if fitInterruptedPlayback {
		t.Fatal("command palette did not run fit without pausing playback")
	}
	m.openDialog(dialogCommands)
	m.ui.search.SetValue("elevation")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.profileEnabled() || !m.activePlayback().Playing {
		t.Fatal("command palette did not hide the default profile while playing")
	}
	m.ui.detailsOpen = true
	m.Update(tea.KeyPressMsg{Code: ' '})
	if m.activePlayback().Playing {
		t.Fatal("sidebar focus swallowed the playback shortcut")
	}
}
