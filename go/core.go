package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type XY struct{ X, Y float64 }
type Point struct {
	Lon       float64            `json:"lon"`
	Lat       float64            `json:"lat"`
	Elevation *float64           `json:"elevation_m"`
	Time      *string            `json:"time"`
	Name      string             `json:"name"`
	Distance  float64            `json:"distance_m"`
	Segment   int                `json:"segment_index"`
	Metrics   map[string]float64 `json:"metrics,omitempty"`
	World     XY                 `json:"-"`
}
type Segment struct {
	Kind       string `json:"kind"`
	Start, End int
}
type Track struct {
	Name      string
	Points    []Point
	Segments  []Segment
	Waypoints []Point
	Bounds    [4]float64
	Distance  float64
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r > 126 {
			return ' '
		}
		return r
	}, s)
}
func project(lon, lat float64) XY {
	lat = math.Max(-85.05112878, math.Min(85.05112878, lat))
	return XY{(lon + 180) / 360, (1 - math.Asinh(math.Tan(lat*math.Pi/180))/math.Pi) / 2}
}
func unproject(p XY) XY {
	return XY{p.X*360 - 180, math.Atan(math.Sinh(math.Pi*(1-2*p.Y))) * 180 / math.Pi}
}
func haversine(a, b Point) float64 {
	r := math.Pi / 180
	h := math.Pow(math.Sin((b.Lat-a.Lat)*r/2), 2) + math.Cos(a.Lat*r)*math.Cos(b.Lat*r)*math.Pow(math.Sin((b.Lon-a.Lon)*r/2), 2)
	return 12742000 * math.Asin(math.Sqrt(math.Min(1, h)))
}

func loadTrack(path string) (*Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open GPX: %w", err)
	}
	defer f.Close()
	d := xml.NewDecoder(f)
	t := &Track{Name: "Imported GPX"}
	stack := []string{}
	seg := -1
	var p *Point
	pointTag := ""
	text := ""
	root := false
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("malformed GPX: %w", e)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			tag := v.Name.Local
			if len(stack) == 0 {
				if root || tag != "gpx" {
					return nil, fmt.Errorf("expected one gpx root")
				}
				root = true
			}
			stack = append(stack, tag)
			text = ""
			if tag == "trkseg" || tag == "rte" {
				seg = len(t.Segments)
				kind := "track"
				if tag == "rte" {
					kind = "route"
				}
				t.Segments = append(t.Segments, Segment{kind, len(t.Points), len(t.Points)})
			}
			if tag == "trkpt" || tag == "rtept" || tag == "wpt" {
				p = &Point{Segment: seg}
				pointTag = tag
				lat, lon := false, false
				for _, a := range v.Attr {
					if a.Name.Local == "lat" {
						p.Lat, e = strconv.ParseFloat(a.Value, 64)
						lat = e == nil
					}
					if a.Name.Local == "lon" {
						p.Lon, e = strconv.ParseFloat(a.Value, 64)
						lon = e == nil
					}
				}
				if !lat || !lon || math.IsNaN(p.Lat) || math.IsNaN(p.Lon) || math.Abs(p.Lat) > 90 || math.Abs(p.Lon) > 180 {
					return nil, fmt.Errorf("invalid GPX coordinates")
				}
				p.World = project(p.Lon, p.Lat)
			}
		case xml.CharData:
			text += string(v)
		case xml.EndElement:
			tag := v.Name.Local
			parent := ""
			if len(stack) > 1 {
				parent = stack[len(stack)-2]
			}
			val := strings.TrimSpace(text)
			if p != nil && parent == pointTag {
				switch tag {
				case "ele":
					x, e := strconv.ParseFloat(val, 64)
					if e != nil || math.IsNaN(x) || math.IsInf(x, 0) {
						return nil, fmt.Errorf("invalid elevation")
					}
					p.Elevation = &x
				case "time":
					if _, e := time.Parse(time.RFC3339Nano, val); e != nil {
						return nil, fmt.Errorf("invalid timestamp")
					}
					p.Time = &val
				case "name":
					p.Name = clean(val)
				}
			}
			if tag == pointTag && p != nil {
				if tag == "wpt" {
					p.Segment = -1
					t.Waypoints = append(t.Waypoints, *p)
				} else {
					if seg < 0 {
						return nil, fmt.Errorf("point outside segment")
					}
					s := &t.Segments[seg]
					if len(t.Points) > s.Start {
						prev := t.Points[len(t.Points)-1]
						if math.Abs(prev.Lon-p.Lon) > 180 {
							return nil, fmt.Errorf("antimeridian segments are not supported")
						}
						t.Distance += haversine(prev, *p)
					}
					p.Distance = t.Distance
					t.Points = append(t.Points, *p)
					s.End = len(t.Points)
				}
				p = nil
				pointTag = ""
			}
			if tag == "name" && (parent == "metadata" || parent == "trk") && val != "" {
				t.Name = clean(val)
			}
			if tag == "trkseg" || tag == "rte" {
				seg = -1
			}
			stack = stack[:len(stack)-1]
			text = ""
		}
	}
	if !root || len(t.Points)+len(t.Waypoints) == 0 {
		return nil, fmt.Errorf("GPX contains no geometry")
	}
	t.Bounds = [4]float64{180, 90, -180, -90}
	for _, ps := range [][]Point{t.Points, t.Waypoints} {
		for _, p := range ps {
			t.Bounds[0] = math.Min(t.Bounds[0], p.Lon)
			t.Bounds[1] = math.Min(t.Bounds[1], p.Lat)
			t.Bounds[2] = math.Max(t.Bounds[2], p.Lon)
			t.Bounds[3] = math.Max(t.Bounds[3], p.Lat)
		}
	}
	if t.Bounds[2]-t.Bounds[0] > 180 {
		return nil, fmt.Errorf("antimeridian bounds are not supported")
	}
	return t, nil
}
func (t *Track) summary() map[string]any {
	elev, times, routes, segments := 0, 0, 0, 0
	var lo, hi *float64
	var first, last *string
	for _, s := range t.Segments {
		if s.Kind == "route" {
			routes++
		} else {
			segments++
		}
	}
	for _, p := range t.Points {
		if p.Elevation != nil {
			elev++
			if lo == nil || *p.Elevation < *lo {
				v := *p.Elevation
				lo = &v
			}
			if hi == nil || *p.Elevation > *hi {
				v := *p.Elevation
				hi = &v
			}
		}
		if p.Time != nil {
			times++
			if first == nil {
				first = p.Time
			}
			last = p.Time
		}
	}
	waypoints := t.Waypoints
	if waypoints == nil {
		waypoints = []Point{}
	}
	return map[string]any{"name": t.Name, "bounds": t.Bounds, "distance_m": t.Distance, "point_count": len(t.Points), "segment_count": segments, "route_count": routes, "waypoint_count": len(t.Waypoints), "waypoints": waypoints, "elevation_point_count": elev, "time_point_count": times, "elevation_min_m": lo, "elevation_max_m": hi, "start_time": first, "end_time": last}
}
func (t *Track) inspect(index *int, distance *float64) (map[string]any, error) {
	if (index == nil) == (distance == nil) {
		return nil, fmt.Errorf("supply exactly one of point_index or distance_m")
	}
	i := 0
	if distance != nil {
		if math.IsNaN(*distance) || *distance < 0 || *distance > t.Distance {
			return nil, fmt.Errorf("distance outside track")
		}
		i = sort.Search(len(t.Points), func(i int) bool { return t.Points[i].Distance >= *distance })
		if i > 0 && (i == len(t.Points) || *distance-t.Points[i-1].Distance < t.Points[i].Distance-*distance) {
			i--
		}
	} else {
		i = *index
	}
	if i < 0 || i >= len(t.Points) {
		return nil, fmt.Errorf("point_index outside track")
	}
	return map[string]any{"point_index": i, "point": t.Points[i]}, nil
}

type Camera struct {
	Center                XY
	Zoom, Bearing, Aspect float64
	Width, Height         int
}

func (c Camera) scale() float64 { return 256 * math.Exp2(c.Zoom) }
func (c Camera) screen(p XY) XY {
	dx, dy := (p.X-c.Center.X)*c.scale(), (p.Y-c.Center.Y)*c.scale()
	a := c.Bearing * math.Pi / 180
	return XY{float64(c.Width) + (dx*math.Cos(a) + dy*math.Sin(a)), float64(c.Height)*2 + (-dx*math.Sin(a)+dy*math.Cos(a))*2/c.Aspect}
}
func (c Camera) world(p XY) XY {
	dx, dy := p.X-float64(c.Width), (p.Y-float64(c.Height)*2)*c.Aspect/2
	a := c.Bearing * math.Pi / 180
	return XY{c.Center.X + (dx*math.Cos(a)-dy*math.Sin(a))/c.scale(), c.Center.Y + (dx*math.Sin(a)+dy*math.Cos(a))/c.scale()}
}
func (c *Camera) zoomAt(p XY, delta float64) {
	before := c.world(p)
	c.Zoom = math.Max(0, math.Min(20, c.Zoom+delta))
	after := c.world(p)
	c.Center.X += before.X - after.X
	c.Center.Y += before.Y - after.Y
	c.clampCenter()
}
func (c *Camera) pan(dx, dy float64) {
	a := c.world(XY{0, 0})
	b := c.world(XY{dx, dy})
	c.Center.X += a.X - b.X
	c.Center.Y += a.Y - b.Y
	c.clampCenter()
}
func (c *Camera) clampCenter() {
	c.Center.X = math.Max(0, math.Min(1, c.Center.X))
	c.Center.Y = math.Max(0, math.Min(1, c.Center.Y))
}
func fit(t *Track, w, h int, aspect float64) Camera {
	if t == nil {
		return Camera{Center: project(0, 0), Aspect: aspect, Width: w, Height: h}
	}
	a, b := project(t.Bounds[0], t.Bounds[3]), project(t.Bounds[2], t.Bounds[1])
	scale := math.Min(float64(w)*1.6/math.Max(b.X-a.X, 1e-7), float64(h)*1.6*aspect/math.Max(b.Y-a.Y, 1e-7))
	return Camera{XY{(a.X + b.X) / 2, (a.Y + b.Y) / 2}, math.Max(0, math.Min(20, math.Log2(scale/256))), 0, aspect, w, h}
}
func pointSegment(p, a, b XY) (float64, float64) {
	dx, dy := b.X-a.X, b.Y-a.Y
	u := 0.0
	if dx*dx+dy*dy > 0 {
		u = math.Max(0, math.Min(1, ((p.X-a.X)*dx+(p.Y-a.Y)*dy)/(dx*dx+dy*dy)))
	}
	return math.Hypot(p.X-a.X-u*dx, p.Y-a.Y-u*dy), u
}
func pick(t *Track, c Camera, p XY) int {
	if t == nil {
		return -1
	}
	best := 6.0
	idx := -1
	p.Y *= c.Aspect / 2
	for _, s := range t.Segments {
		for i := s.Start; i < s.End; i++ {
			a := c.screen(t.Points[i].World)
			a.Y *= c.Aspect / 2
			b := a
			if i+1 < s.End {
				b = c.screen(t.Points[i+1].World)
				b.Y *= c.Aspect / 2
			}
			d, u := pointSegment(p, a, b)
			if d < best {
				best = d
				idx = i
				if u > 0.5 && i+1 < s.End {
					idx++
				}
			}
		}
	}
	return idx
}
