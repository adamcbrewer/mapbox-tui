package main

import (
	"fmt"
	"math"
	"strings"
)

type Document struct {
	Path, Filename, Format, Name string
	Items                        []*DocumentItem
	Bounds                       [4]float64
	nextID                       int
}

type DocumentItem struct {
	ID    int
	Kind  string
	Group *ImportGroup
	Track *Track
}

type ImportGroup struct {
	Name   string
	Parent *ImportGroup
}

func (d *Document) addItem(kind string) *DocumentItem {
	d.nextID++
	item := &DocumentItem{ID: d.nextID, Kind: kind, Track: &Track{}}
	d.Items = append(d.Items, item)
	return item
}

func (d *Document) prepare() error {
	d.Name = clean(d.Name)
	d.Bounds = [4]float64{180, 90, -180, -90}
	items := make([]*DocumentItem, 0, len(d.Items))
	for _, item := range d.Items {
		t := item.Track
		if len(t.Points)+len(t.Waypoints) == 0 {
			continue
		}
		if strings.TrimSpace(t.Name) == "" {
			t.Name = fmt.Sprintf("%s%s %d", strings.ToUpper(item.Kind[:1]), item.Kind[1:], item.ID)
		}
		if err := prepareTrack(t); err != nil {
			return err
		}
		t.RouteCount = 0
		for _, s := range t.Segments {
			if s.Kind == "route" && s.End > s.Start {
				t.RouteCount = 1
				break
			}
		}
		d.Bounds[0] = math.Min(d.Bounds[0], t.Bounds[0])
		d.Bounds[1] = math.Min(d.Bounds[1], t.Bounds[1])
		d.Bounds[2] = math.Max(d.Bounds[2], t.Bounds[2])
		d.Bounds[3] = math.Max(d.Bounds[3], t.Bounds[3])
		items = append(items, item)
	}
	if len(items) == 0 {
		return fmt.Errorf("contains no supported geometry")
	}
	if d.Bounds[2]-d.Bounds[0] > 180 {
		return fmt.Errorf("antimeridian bounds are not supported")
	}
	d.Items = items
	return nil
}

// Single-file consumers still use a combined snapshot while workspace support is introduced.
func (d *Document) track() *Track {
	out := &Track{Name: d.Name, Bounds: d.Bounds}
	for _, item := range d.Items {
		t := item.Track
		pointOffset, segmentOffset := len(out.Points), len(out.Segments)
		for _, p := range t.Points {
			p.Distance += out.Distance
			p.Segment += segmentOffset
			out.Points = append(out.Points, p)
		}
		for _, s := range t.Segments {
			s.Start += pointOffset
			s.End += pointOffset
			out.Segments = append(out.Segments, s)
		}
		out.Waypoints = append(out.Waypoints, t.Waypoints...)
		out.Distance += t.Distance
		out.RouteCount += t.RouteCount
	}
	return out
}
