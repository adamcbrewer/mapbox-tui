package main

import (
	"fmt"
	"math"
	"strings"
)

type elevationProfile struct {
	Grid      Grid
	Min, Max  float64
	Available bool
	Distance  float64
}

func profileRows(height int, shown bool) int {
	if !shown {
		return 0
	}
	if height >= 12 {
		return 7
	}
	if height >= 8 {
		return 3
	}
	return 0
}
func newProfile(t *Track, width, height int) *elevationProfile {
	p := &elevationProfile{Grid: newGrid(width, height), Min: math.Inf(1), Max: math.Inf(-1), Distance: t.Distance}
	for _, point := range t.Points {
		if point.Elevation != nil {
			p.Available = true
			p.Min = math.Min(p.Min, *point.Elevation)
			p.Max = math.Max(p.Max, *point.Elevation)
		}
	}
	if !p.Available {
		return p
	}
	for _, s := range t.Segments {
		var last *XY
		for i := s.Start; i < s.End; i++ {
			point := t.Points[i]
			if point.Elevation == nil {
				last = nil
				continue
			}
			xy := p.screen(point.Distance, *point.Elevation)
			p.Grid.dot(int(math.Round(xy.X)), int(math.Round(xy.Y)), roleActiveRoute)
			if last != nil {
				p.Grid.line(*last, xy, roleActiveRoute)
			}
			last = &xy
		}
	}
	return p
}
func (p *elevationProfile) screen(distance, elevation float64) XY {
	y := float64(p.Grid.Height*4-1) / 2
	if p.Max > p.Min {
		y = (p.Max - elevation) / (p.Max - p.Min) * float64(p.Grid.Height*4-1)
	}
	return XY{distance / math.Max(p.Distance, 1) * float64(p.Grid.Width*2-1), y}
}
func (p *elevationProfile) frame(pos *ridePosition) (string, Grid, string) {
	header := "Elevation profile | unavailable | e hide"
	if p.Available {
		header = fmt.Sprintf("Elevation profile | %.1f–%.1f m | e hide", p.Min, p.Max)
	}
	if pos != nil {
		ele := "unavailable"
		if pos.Elevation != nil {
			ele = fmt.Sprintf("%.1f m", *pos.Elevation)
			if pos.Blend > 0 {
				ele = "~" + ele
			}
		}
		header += fmt.Sprintf(" | @ %.2f km / %s", pos.Distance/1000, ele)
	}
	g := p.Grid
	g.Cells = append([]Cell(nil), p.Grid.Cells...)
	if !p.Available {
		g.label(XY{2, 0}, "No recorded elevation", rolePlace, false)
	}
	if pos != nil {
		x := pos.Distance / math.Max(p.Distance, 1) * float64(g.Width*2-1)
		for y := 0; y < g.Height; y++ {
			g.label(XY{x, float64(y * 4)}, "|", roleCursor, false)
		}
		if p.Available && pos.Elevation != nil {
			g.label(p.screen(pos.Distance, *pos.Elevation), "@", roleCursor, false)
		}
	}
	end := fmt.Sprintf("%.2f km", p.Distance/1000)
	axis := "0 km" + strings.Repeat(" ", max(0, g.Width-4-len(end))) + end
	return header, g, axis
}
