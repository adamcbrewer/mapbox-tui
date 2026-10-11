package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCameraRoundTripAndZoomAnchor(t *testing.T) {
	for _, aspect := range []float64{1, 2, 2.4, 3} {
		for _, bearing := range []float64{0, 45, 90, -130} {
			c := Camera{project(-0.1, 51.5), 14, bearing, aspect, 120, 40}
			p := project(-0.12, 51.51)
			s := c.screen(p)
			back := c.world(s)
			if math.Hypot(back.X-p.X, back.Y-p.Y) > 1e-12 {
				t.Fatal("round trip changed geographic point")
			}
			mouse := XY{33, 117}
			before := c.world(mouse)
			c.zoomAt(mouse, 1.5)
			after := c.world(mouse)
			if math.Hypot(before.X-after.X, before.Y-after.Y) > 1e-12 {
				t.Fatal("zoom moved pointer anchor")
			}
		}
	}
}
func TestGPXPreservesGapsRoutesAndMeasurements(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	if len(track.Segments) != 3 || len(track.Points) != 12 || len(track.Waypoints) != 2 {
		t.Fatal("geometry topology changed")
	}
	if track.Points[6].Distance != track.Points[5].Distance {
		t.Fatal("distance includes disconnected gap")
	}
	if track.Points[8].Elevation != nil || track.Points[10].Time != nil {
		t.Fatal("invented measurements")
	}
	if track.Segments[2].Kind != "route" {
		t.Fatal("route became track")
	}
	sum := 0.0
	for _, s := range track.Segments {
		for i := s.Start + 1; i < s.End; i++ {
			sum += haversine(track.Points[i-1], track.Points[i])
		}
	}
	if math.Abs(sum-track.Distance) > 1e-8 {
		t.Fatal("distance differs from original parts")
	}
}
func TestPickingUsesSegmentToleranceWithoutConnectingGaps(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	c := fit(track, 160, 50, 2)
	a, b := c.screen(track.Points[1].World), c.screen(track.Points[2].World)
	if pick(track, c, XY{(a.X + b.X) / 2, (a.Y + b.Y) / 2}) < 0 {
		t.Fatal("visible segment not pickable")
	}
	a, b = c.screen(track.Points[5].World), c.screen(track.Points[6].World)
	if pick(track, c, XY{(a.X + b.X) / 2, (a.Y + b.Y) / 2}) != -1 {
		t.Fatal("invented gap is pickable")
	}
}
func TestInvalidAndWaypointOnlyGPX(t *testing.T) {
	for _, name := range []string{"empty.gpx", "invalid.gpx"} {
		if _, e := loadTrack("../fixtures/" + name); e == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	track, e := loadTrack("../fixtures/waypoints.gpx")
	if e != nil {
		t.Fatal(e)
	}
	if len(track.Points) != 0 || track.Distance != 0 {
		t.Fatal("waypoint became invented route")
	}
	for _, body := range []string{"<gpx><trk>", "<gpx><wpt lat=\"NaN\" lon=\"0\"/></gpx>", "<gpx><rte><rtept lat=\"0\" lon=\"179\"/><rtept lat=\"0\" lon=\"-179\"/></rte></gpx>"} {
		p := filepath.Join(t.TempDir(), "bad.gpx")
		if e := os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := loadTrack(p); e == nil {
			t.Fatal("accepted malformed or unsupported geometry")
		}
	}
}
func TestDistanceInspectionAndTilePolicy(t *testing.T) {
	track, e := loadTrack("../fixtures/example.gpx")
	if e != nil {
		t.Fatal(e)
	}
	d := track.Distance * 0.4
	v, e := track.inspect(nil, &d)
	if e != nil || v["point_index"] == nil {
		t.Fatal("distance inspection failed")
	}
	i := 0
	if _, e := track.inspect(&i, &d); e == nil {
		t.Fatal("accepted ambiguous selector")
	}
	if ttl("public,max-age=43200,s-maxage=300", "") != 12*time.Hour || ttl("no-store,max-age=100", "") != 0 {
		t.Fatal("cache policy ignored")
	}
	c := fit(track, 160, 50, 2)
	c.Bearing = 45
	if n := len(visible(c)); n == 0 || n > 64 {
		t.Fatalf("invalid coverage: %d", n)
	}
	if _, e := decodeTile([]byte{0xff}, Tile{}); e == nil {
		t.Fatal("accepted malformed PBF")
	}
}
