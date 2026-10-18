package main

import (
	"math"

	"github.com/charmbracelet/harmonica"
)

type cameraMotion struct {
	ID                            string
	Target                        Camera
	Velocity                      XY
	ZoomVelocity, BearingVelocity float64
	Active                        bool
}

func (m *cameraMotion) advance(c *Camera, seconds float64) bool {
	if !m.Active || seconds <= 0 {
		return false
	}
	spring := harmonica.NewSpring(seconds, 30, 1)
	c.Center.X, m.Velocity.X = spring.Update(c.Center.X, m.Velocity.X, m.Target.Center.X)
	c.Center.Y, m.Velocity.Y = spring.Update(c.Center.Y, m.Velocity.Y, m.Target.Center.Y)
	c.Zoom, m.ZoomVelocity = spring.Update(c.Zoom, m.ZoomVelocity, m.Target.Zoom)
	c.Bearing, m.BearingVelocity = spring.Update(c.Bearing, m.BearingVelocity, m.Target.Bearing)
	gap := math.Hypot(c.Center.X-m.Target.Center.X, c.Center.Y-m.Target.Center.Y) * c.scale()
	if gap < 0.05 && math.Abs(c.Zoom-m.Target.Zoom) < 0.001 && math.Abs(c.Bearing-m.Target.Bearing) < 0.01 {
		c.Center, c.Zoom, c.Bearing = m.Target.Center, m.Target.Zoom, m.Target.Bearing
		m.Active = false
	}
	return true
}

func (m *viewer) destination() Camera {
	if m.motion.Active {
		return m.motion.Target
	}
	return m.camera
}

func (m *viewer) moveCamera(target Camera) {
	m.moveCameraAnimated(target, !m.settings.NoAnimation)
}

func (m *viewer) moveCameraAnimated(target Camera, animate bool) {
	// Record an already-settled transition before assigning its replacement.
	m.reconcileOperations()
	m.motion.ID = newSessionID()
	m.motion.Target = target
	if !animate {
		m.camera = target
		m.motion.Active = false
	} else {
		if !m.motion.Active {
			m.motion.Velocity = XY{}
			m.motion.ZoomVelocity, m.motion.BearingVelocity = 0, 0
		}
		target.Bearing = m.camera.Bearing + math.Remainder(target.Bearing-m.camera.Bearing, 360)
		m.motion.Target = target
		m.motion.Active = true
	}
	m.dirty = true
}

func (m *viewer) panCamera(dx, dy float64) {
	target := m.destination()
	target.pan(dx, dy)
	m.setFollowing(false)
	m.moveCamera(target)
}

func (m *viewer) setCameraAnimation(enabled bool) {
	if m.settings.NoAnimation == enabled {
		m.settings.NoAnimation = !enabled
		m.workspace.edited()
	}
	if !enabled {
		m.camera = m.destination()
		m.motion.Active = false
	}
	m.dirty = true
}
