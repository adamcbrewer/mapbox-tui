package main

import (
	"math"
	"time"

	tea "charm.land/bubbletea/v2"
)

const trafficTileset = "mapbox.mapbox-traffic-v1"
const trafficRadiusMeters = 20 * 1609.344
const trafficRefreshInterval = 5 * time.Minute
const trafficAuthGuidance = "Verify Mapbox via account setup / Check connection to enable traffic"

func newTrafficProvider(token string) *Provider {
	p := newProvider("mapbox")
	p.token, p.tileset = token, trafficTileset
	return p
}

func trafficTiles(c Camera) []Tile {
	centre := unproject(c.Center)
	angle := trafficRadiusMeters / 6371000
	latitude := centre.Y * math.Pi / 180
	deltaLat := angle * 180 / math.Pi
	deltaLon := math.Asin(math.Min(1, math.Sin(angle)/math.Cos(latitude))) * 180 / math.Pi
	westNorth := project(centre.X-deltaLon, centre.Y+deltaLat)
	eastSouth := project(centre.X+deltaLon, centre.Y-deltaLat)
	a, b := visibleBounds(c)
	a = XY{math.Max(a.X, westNorth.X), math.Max(a.Y, westNorth.Y)}
	b = XY{math.Min(b.X, eastSouth.X), math.Min(b.Y, eastSouth.Y)}
	z := int(math.Max(6, math.Min(14, math.Floor(c.Zoom))))
	return tilesForBounds(a, b, z)
}

func trafficContains(centre, point XY) bool {
	a, b := unproject(centre), unproject(point)
	return haversine(Point{Lon: a.X, Lat: a.Y}, Point{Lon: b.X, Lat: b.Y}) <= trafficRadiusMeters
}

func trafficFeatureRole(properties map[string]any) mapRole {
	if properties["closed"] == "yes" {
		return roleTrafficClosed
	}
	switch properties["congestion"] {
	case "low":
		return roleTrafficLow
	case "moderate":
		return roleTrafficModerate
	case "heavy":
		return roleTrafficHeavy
	case "severe":
		return roleTrafficSevere
	}
	return roleNone
}

func (role mapRole) traffic() bool {
	return role >= roleTrafficLow && role <= roleTrafficClosed
}

func (m *viewer) configureTraffic() {
	enabled := m.preferences.Traffic && m.mapboxVerified()
	changed := enabled != m.trafficEnabled
	credentialsChanged := enabled && (m.traffic == nil || m.traffic.token != m.provider.token)
	if !changed && !credentialsChanged {
		return
	}
	if enabled {
		m.traffic = newTrafficProvider(m.provider.token)
	}
	m.sources.set("traffic", m.traffic, enabled)
	m.trafficEnabled = enabled
	m.dirty = true
}

func (m *viewer) toggleTraffic() tea.Cmd {
	if m.trafficCommand().disabled {
		m.ui.notice = trafficAuthGuidance
		m.dirty = true
		return nil
	}
	preferences := m.preferences
	preferences.Traffic = !preferences.Traffic
	if err := saveAppSettings(preferences); err != nil {
		m.ui.notice = "Could not save traffic setting: " + err.Error()
		m.dirty = true
		return nil
	}
	m.preferences = preferences
	return m.fetch()
}

func (m *viewer) trafficCommand() viewerCommand {
	return viewerCommand{
		title: "Toggle traffic", action: "traffic", toggle: true, checked: m.preferences.Traffic,
		disabled: !m.mapboxVerified() && !m.preferences.Traffic,
	}
}

func (m *viewer) trafficLegend() string {
	legend := "Traffic 20 mi: "
	for i, label := range []string{"1 low", "2 moderate", "3 heavy", "4 severe", "X closed"} {
		if i > 0 {
			legend += " · "
		}
		legend += m.paint(label, roleTrafficLow+mapRole(i))
	}
	return legend
}
