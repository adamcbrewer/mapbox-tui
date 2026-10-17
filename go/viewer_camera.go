package main

import "fmt"

// Pointer fields distinguish a complete saved view from missing coordinates.
type savedView struct {
	Zoom *float64 `json:"zoom"`
	Lat  *float64 `json:"lat"`
	Lon  *float64 `json:"long"`
}

func (v savedView) validate() error {
	if v.Lon == nil || v.Lat == nil || v.Zoom == nil {
		return fmt.Errorf("centre longitude, latitude and zoom are required")
	}
	_, err := viewport(nil, Args{Lon: v.Lon, Lat: v.Lat, Zoom: v.Zoom})
	return err
}

func (m *viewer) startupViewArgs() Args {
	a := m.args
	if m.savedView != nil && !a.ResetView {
		if a.Lon == nil && a.Lat == nil {
			a.Lon, a.Lat = m.savedView.Lon, m.savedView.Lat
		}
		if a.Zoom == nil {
			a.Zoom = m.savedView.Zoom
		}
	}
	return a
}

func (m *viewer) hasStartupView() bool {
	a := m.startupViewArgs()
	return a.Lon != nil || a.Zoom != nil || a.Bearing != nil
}

func (m *viewer) restoreStartupView(c Camera) Camera {
	a := m.startupViewArgs()
	if a.Lon != nil && a.Lat != nil {
		c.Center = project(*a.Lon, *a.Lat)
	}
	if a.Zoom != nil {
		c.Zoom = *a.Zoom
	}
	if a.Bearing != nil {
		c.Bearing = *a.Bearing
	}
	return c
}

func (m *viewer) recordViewChange(before Camera) {
	after := m.destination()
	if before.Center != after.Center || before.Zoom != after.Zoom {
		m.viewChanged = true
	}
}

// Run synchronously after the UI has stopped, so no camera writer can race it.
func (m *viewer) saveViewOnExit() error {
	if !m.viewChanged {
		return nil
	}
	c := m.destination()
	centre := unproject(c.Center)
	view := &savedView{Lon: &centre.X, Lat: &centre.Y, Zoom: &c.Zoom}
	_, err := updateAppSettings(func(s *appSettings) { s.LastView = view })
	if err != nil {
		return fmt.Errorf("view not saved: %w", err)
	}
	return nil
}
