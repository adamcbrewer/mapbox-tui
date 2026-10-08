package main

import (
	"fmt"
	"math"
	"strings"
)

type elevationProfile struct {
	Grid           Grid
	Min, Max       float64
	Available      bool
	Distance       float64
	Items          []sceneItem
	AvailableItems map[ItemRef]bool
}

func profileRows(height int, shown bool) int {
	if !shown {
		return 0
	}
	if height >= 12 {
		return 7
	}
	if height >= 8 {
		return 5
	}
	return 0
}
func newProfile(items []sceneItem, width, height int) *elevationProfile {
	p := &elevationProfile{
		Grid:           newGrid(width, height),
		Min:            math.Inf(1),
		Max:            math.Inf(-1),
		Items:          items,
		AvailableItems: make(map[ItemRef]bool),
	}
	for _, item := range items {
		p.Distance += item.Track.Distance
		for _, point := range item.Track.Points {
			if point.Elevation != nil {
				p.Available = true
				p.AvailableItems[item.Ref] = true
				p.Min = math.Min(p.Min, *point.Elevation)
				p.Max = math.Max(p.Max, *point.Elevation)
			}
		}
	}
	if !p.Available {
		return p
	}
	var offset float64
	for _, item := range items {
		p.draw(item, offset)
		if offset > 0 && offset < p.Distance {
			x := int(math.Round(p.screen(offset, p.Min).X)) / 2
			for y := 0; y < p.Grid.Height; y++ {
				p.Grid.Cells[y*p.Grid.Width+x] = Cell{}
			}
		}
		offset += item.Track.Distance
	}
	return p
}

func (p *elevationProfile) draw(item sceneItem, offset float64) {
	role := roleRoute
	if item.Active {
		role = roleActiveRoute
	}
	for _, s := range item.Track.Segments {
		var last *XY
		for i := s.Start; i < s.End; i++ {
			point := item.Track.Points[i]
			if point.Elevation == nil {
				last = nil
				continue
			}
			xy := p.screen(offset+point.Distance, *point.Elevation)
			p.Grid.dot(
				int(math.Round(xy.X)),
				int(math.Round(xy.Y)),
				role,
				item.Color,
			)
			if last != nil {
				p.Grid.line(
					*last,
					xy,
					role,
					item.Color,
				)
			}
			last = &xy
		}
	}
}
func (p *elevationProfile) screen(distance, elevation float64) XY {
	y := float64(p.Grid.Height*4-1) / 2
	if p.Max > p.Min {
		y = (p.Max - elevation) / (p.Max - p.Min) * float64(p.Grid.Height*4-1)
	}
	return XY{distance / math.Max(p.Distance, 1) * float64(p.Grid.Width*2-1), y}
}
func (p *elevationProfile) frame(pos *ridePosition) (string, Grid, string) {
	var distance float64
	var color activityColor
	if pos != nil {
		distance = pos.Distance
		var offset float64
		for _, item := range p.Items {
			if item.Active {
				distance += offset
				color = item.Color
				break
			}
			offset += item.Track.Distance
		}
	}
	title := "Elevation profile"
	if len(p.Items) > 1 {
		title = "Elevation profiles"
	}
	header := title + " | unavailable | e hide"
	if p.Available {
		header = fmt.Sprintf(
			"%s | %.1f–%.1f m | e hide",
			title,
			p.Min,
			p.Max,
		)
	}
	if pos != nil {
		ele := "unavailable"
		if pos.Elevation != nil {
			ele = fmt.Sprintf("%.1f m", *pos.Elevation)
			if pos.Blend > 0 {
				ele = "~" + ele
			}
		}
		header += fmt.Sprintf(" | @ %.2f km / %s", distance/1000, ele)
	}
	g := p.Grid
	g.Cells = append([]Cell(nil), p.Grid.Cells...)
	if !p.Available {
		g.label(
			XY{2, 0},
			"No recorded elevation",
			rolePlace,
			false,
		)
	}
	if pos != nil {
		x := math.Round(p.screen(distance, p.Min).X)
		for y := 0; y < g.Height; y++ {
			g.label(
				XY{x, float64(y * 4)},
				"|",
				roleCursor,
				false,
			)
		}
		if p.Available && pos.Elevation != nil {
			point := p.screen(distance, *pos.Elevation)
			point.X, point.Y = math.Round(point.X), math.Round(point.Y)
			g.label(
				point,
				"@",
				roleCursor,
				false,
				color,
			)
		}
	}
	end := fmt.Sprintf("%.2f km", p.Distance/1000)
	if len(p.Items) > 1 {
		end += " total"
	}
	axis := "0 km" + strings.Repeat(" ", max(0, g.Width-4-len(end))) + end
	return header, g, axis
}
