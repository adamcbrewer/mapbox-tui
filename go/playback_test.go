package main

import (
	"math"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestPlaybackInterpolatesRecordedPositionAndHonorsPauseAndSpeed(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	p := newPlayback(track)
	p.toggle()
	p.advance(track, 60)
	pos := p.position(track)
	if p.Index != 0 || math.Abs(p.Blend-.5) > 1e-12 {
		t.Fatal("recorded time did not determine position")
	}
	if math.Abs(pos.Distance-track.Points[1].Distance/2) > 1e-8 || pos.Elevation == nil || *pos.Elevation != 9 || pos.Time == nil || *pos.Time != "2026-01-01T09:01:00Z" {
		t.Fatal("interpolated details disagree with ride position")
	}
	p.toggle()
	p.advance(track, 100)
	if p.Elapsed != 60 {
		t.Fatal("paused playback advanced")
	}
	p.cycle(1)
	p.toggle()
	p.advance(track, 1)
	if p.Elapsed != 65 {
		t.Fatal("5x speed was not applied")
	}
	p.seek(5)
	if p.position(track).Index != 5 || p.Playing {
		t.Fatal("manual selection did not pause and seek exactly")
	}
}

func TestPlaybackSkipsSegmentGapsAndFinishesWithoutLooping(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	p := newPlayback(track)
	if p.times[5] != 600 || p.times[6] != 600 || p.times[9] != 960 {
		t.Fatal("gap added recorded waiting time")
	}
	expected := 960 + (track.Points[11].Distance-track.Points[10].Distance)/6
	if math.Abs(p.duration()-expected) > 1e-8 || p.Mode != "mixed time/estimate" {
		t.Fatal("untimed route did not use the distance estimate")
	}
	p.seek(5)
	p.toggle()
	p.advance(track, 1)
	if p.Index != 6 || p.position(track).World.X < track.Points[6].World.X {
		t.Fatal("interpolated across disconnected geometry")
	}
	p.advance(track, 1e6)
	if p.Playing || p.Index != 11 || p.Blend != 0 {
		t.Fatal("playback did not stop at final point")
	}
	p.toggle()
	if !p.Playing || p.Index != 0 || p.Elapsed != 0 {
		t.Fatal("finished route did not restart on play")
	}
	waypoints, e := loadTrack("../fixtures/waypoints.gpx")
	if e != nil {
		t.Fatal(e)
	}
	empty := newPlayback(waypoints)
	empty.toggle()
	if empty.Playing || empty.position(waypoints) != nil {
		t.Fatal("waypoint file became a playable ride")
	}
}

func TestProfileAndMapFollowShareInterpolatedCursor(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	m := &viewer{
		workspace: testWorkspace(track),
		width:     120,
		height:    40,
		camera: fit(
			track,
			88,
			34,
			2,
		),
		provider:  &Provider{Kind: "none"},
		lastTick:  now,
		following: true,
	}
	m.initUI()
	m.Update(tea.KeyPressMsg{Code: 'e'})
	if m.profileHeight != 7 || m.camera.Height != 27 {
		t.Fatal("profile did not reserve viewport space")
	}
	m.camera.Zoom = 20
	m.camera.Center = track.Points[0].World
	m.lastTick = now
	m.activePlayback().toggle()
	m.advancePlayback(now.Add(time.Minute))
	pos := m.position()
	if pos.Blend != .5 || m.destination().Center != pos.World {
		t.Fatal("camera target did not follow exact playback position")
	}
	m.motion.advance(&m.camera, 1)
	if m.camera.Center != pos.World {
		t.Fatal("camera did not settle at the playback position")
	}
	header, _, _ := m.visibleProfile().frame(pos)
	if !strings.Contains(header, "9.0 m") || !strings.Contains(header, "@ ") {
		t.Fatal("profile cursor missed playback position")
	}
	m.rebuild()
	if len(strings.Split(m.content, "\n")) != 40 || !strings.Contains(m.content, "Elevation profile") || !strings.Contains(m.content, "Playing") {
		t.Fatal("profile overflowed layout or omitted playback controls")
	}
}

func TestStartingAndResumingPlaybackPreservesCameraUntilManualCenter(t *testing.T) {
	track, err := loadTrack("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	for _, noAnimation := range []bool{false, true} {
		for _, start := range []string{"keyboard", "palette"} {
			m := &viewer{
				workspace: testWorkspace(track),
				width:     120,
				height:    40,
				camera: Camera{
					Center:  project(10, 20),
					Zoom:    18,
					Bearing: 45,
					Aspect:  2,
					Width:   88,
					Height:  34,
				},
				args:     Args{NoAnimation: noAnimation},
				lastTick: time.Now(),
			}
			m.initUI()
			before := m.camera
			if start == "palette" {
				m.runCommand("play")
			} else {
				m.Update(tea.KeyPressMsg{Code: ' '})
			}
			startingMovedCamera := !m.activePlayback().Playing ||
				m.camera != before ||
				m.destination() != before ||
				m.motion.Active ||
				m.following
			if startingMovedCamera {
				t.Fatal("starting playback moved the camera or enabled follow", start, noAnimation)
			}
			m.advancePlayback(m.lastTick.Add(time.Minute))
			if m.position().Blend == 0 || m.camera != before || m.motion.Active || m.following {
				t.Fatal("playback progression moved an unfollowed camera")
			}
			m.Update(tea.KeyPressMsg{Code: ' '})
			m.Update(tea.KeyPressMsg{Code: ' '})
			resumingMovedCamera := !m.activePlayback().Playing ||
				m.camera != before ||
				m.motion.Active ||
				m.following
			if resumingMovedCamera {
				t.Fatal("resuming playback moved the camera or enabled follow")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			m.Update(tea.KeyPressMsg{Code: ' '})
			restartingMovedCamera := !m.activePlayback().Playing ||
				m.selectedPoint() != 0 ||
				m.camera != before ||
				m.motion.Active ||
				m.following
			if restartingMovedCamera {
				t.Fatal("restarting finished playback moved the camera or enabled follow")
			}
			m.Update(tea.KeyPressMsg{Code: 'c'})
			manualCenterFailed := !m.following ||
				m.destination().Center != m.position().World ||
				!m.activePlayback().Playing
			if manualCenterFailed {
				t.Fatal("manual center did not explicitly center and follow the current point")
			}
		}
	}
}

func TestPartialOrNonIncreasingTimesUseEstimateWithoutInventingTimestamp(t *testing.T) {
	for _, missing := range []bool{true, false} {
		track, e := loadTrack("../fixtures/example.gpx")
		if e != nil {
			t.Fatal(e)
		}
		if missing {
			track.Points[1].Time = nil
		} else {
			track.Points[1].Time = track.Points[0].Time
		}
		p := newPlayback(track)
		edge := track.Points[1].Distance / 6
		if math.Abs(p.times[1]-edge) > 1e-8 {
			t.Fatal("incomplete timing was treated as recorded timing")
		}
		p.toggle()
		p.advance(track, edge/2)
		pos := p.position(track)
		if math.Abs(pos.Blend-.5) > 1e-12 || pos.Time != nil {
			t.Fatal("estimated playback invented recorded time")
		}
	}
}
