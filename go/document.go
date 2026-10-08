package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

type Document struct {
	Path, Filename, Format, Name string
	Items                        []*DocumentItem
	Bounds                       [4]float64
	nextID                       int
}

type DocumentItem struct {
	ID      int
	Kind    string
	Group   *ImportGroup
	Track   *Track
	Journey *Journey
}

type ImportGroup struct {
	Name   string
	Parent *ImportGroup
}

type itemInfo struct {
	ID            int        `json:"item_id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	Groups        []string   `json:"groups"`
	Bounds        [4]float64 `json:"bounds"`
	PointCount    int        `json:"point_count"`
	WaypointCount int        `json:"waypoint_count"`
	Visible       bool       `json:"visible"`
}

func (d *Document) itemInfos() []itemInfo {
	items := make([]itemInfo, 0, len(d.Items))
	for _, item := range d.Items {
		groups := []string{}
		for group := item.Group; group != nil; group = group.Parent {
			if group.Name != "" {
				groups = append(groups, group.Name)
			}
		}
		slices.Reverse(groups)
		t := item.Track
		items = append(items, itemInfo{
			ID:            item.ID,
			Name:          t.Name,
			Kind:          item.Kind,
			Groups:        groups,
			Bounds:        t.Bounds,
			PointCount:    len(t.Points),
			WaypointCount: len(t.Waypoints),
			Visible:       true,
		})
	}
	return items
}

func (d *Document) selectedTrack(id *int) (*Track, error) {
	if id == nil {
		return d.track(), nil
	}
	for _, item := range d.Items {
		if item.ID == *id {
			return item.Track, nil
		}
	}
	return nil, fmt.Errorf("unknown item_id")
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
			t.Name = fmt.Sprintf(
				"%s%s %d",
				strings.ToUpper(item.Kind[:1]),
				item.Kind[1:],
				item.ID,
			)
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

// track preserves the combined snapshot used by single-file CLI and MCP consumers.
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
