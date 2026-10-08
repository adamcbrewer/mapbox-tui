package main

import (
	"math"
	"strings"
	"testing"
)

func TestElevationProfileKeepsMissingMeasurementsAndPartGaps(t *testing.T) {
	e0, e10, e20 := 0.0, 10.0, 20.0
	missing := &Track{Distance: 40, Segments: []Segment{{Start: 0, End: 5}}, Points: []Point{{Distance: 0, Elevation: &e0}, {Distance: 10, Elevation: &e10}, {Distance: 20}, {Distance: 30, Elevation: &e20}, {Distance: 40, Elevation: &e0}}}
	p := newProfile([]sceneItem{{Track: missing, Active: true}}, 41, 5)
	for y := 0; y < 5; y++ {
		if p.Grid.Cells[y*41+20].Bits != 0 {
			t.Fatal("profile filled missing elevation")
		}
	}
	parts := &Track{Distance: 20, Segments: []Segment{{Start: 0, End: 2}, {Start: 2, End: 4}}, Points: []Point{{Distance: 0, Elevation: &e0}, {Distance: 10, Elevation: &e20}, {Distance: 10, Elevation: &e0}, {Distance: 20, Elevation: &e20}}}
	p = newProfile([]sceneItem{{Track: parts, Active: true}}, 41, 5)
	if p.Grid.Cells[2*41+20].Bits != 0 {
		t.Fatal("profile connected part boundary vertically")
	}
	flat := &Track{Distance: 10, Segments: []Segment{{Start: 0, End: 2}}, Points: []Point{{Elevation: &e0}, {Distance: 10, Elevation: &e0}}}
	p = newProfile([]sceneItem{{Track: flat, Active: true}}, 41, 5)
	if !p.Available || p.Grid.Cells[2*41+20].Bits == 0 {
		t.Fatal("flat profile failed")
	}
}

func TestElevationProfilesUseOrderedSectionsAndSummedDistance(t *testing.T) {
	e10, e20, e100, e110 := 10.0, 20.0, 100.0, 110.0
	short := &Track{
		Distance: 100,
		Segments: []Segment{{Start: 0, End: 2}},
		Points:   []Point{{Elevation: &e100}, {Distance: 100, Elevation: &e110}},
	}
	long := &Track{
		Distance: 200,
		Segments: []Segment{{Start: 0, End: 2}},
		Points:   []Point{{Elevation: &e10}, {Distance: 200, Elevation: &e20}},
	}
	items := []sceneItem{
		{Ref: ItemRef{DocumentID: 1, ItemID: 1}, Track: short, Color: 1},
		{Ref: ItemRef{DocumentID: 2, ItemID: 1}, Track: long, Active: true, Color: 2},
	}
	p := newProfile(items, 61, 5)
	sharedScaleIncomplete := p.Min != 10 || p.Max != 110 || p.Distance != 300 || len(p.AvailableItems) != 2
	if sharedScaleIncomplete {
		t.Fatal("summed distance or shared elevation scale omitted an activity")
	}
	colourBounds := func(g Grid, colour activityColor) (int, int) {
		start, end := g.Width, -1
		for i, cell := range g.Cells {
			if cell.Bits != 0 && cell.Color == colour {
				start = min(start, i%g.Width)
				end = max(end, i%g.Width)
			}
		}
		return start, end
	}
	shortStart, shortEnd := colourBounds(p.Grid, items[0].Color)
	longStart, longEnd := colourBounds(p.Grid, items[1].Color)
	sectionsMisplaced := shortStart != 0 || shortEnd != 19 || longStart != 21 || longEnd != 60
	if sectionsMisplaced {
		t.Fatal(
			"profiles overlapped or did not occupy distance-proportional sections",
			shortStart,
			shortEnd,
			longStart,
			longEnd,
		)
	}
	for y := 0; y < p.Grid.Height; y++ {
		if p.Grid.Cells[y*p.Grid.Width+20].Bits != 0 {
			t.Fatal("profile connected separate activities")
		}
	}
	e15 := 15.0
	header, chart, axis := p.frame(&ridePosition{Distance: 100, Elevation: &e15})
	marker := chart.Cells[4*chart.Width+40]
	cursorOffsetLost := marker.Text != '@' ||
		marker.Color != items[1].Color ||
		!strings.Contains(header, "@ 0.20 km") ||
		!strings.Contains(axis, "0.30 km total")
	if cursorOffsetLost {
		t.Fatal("cursor or axis omitted the earlier activity's distance", header, axis)
	}
	reversed := newProfile([]sceneItem{items[1], items[0]}, 61, 5)
	longStart, longEnd = colourBounds(reversed.Grid, items[1].Color)
	shortStart, shortEnd = colourBounds(reversed.Grid, items[0].Color)
	reversedSectionsWrong := reversed.Distance != p.Distance ||
		reversed.Min != p.Min ||
		reversed.Max != p.Max ||
		longStart != 0 ||
		longEnd != 39 ||
		shortStart != 41 ||
		shortEnd != 60
	if reversedSectionsWrong {
		t.Fatal("profile ignored input order or changed measurements when order changed")
	}
}

func TestMissingElevationKeepsItsSectionInTheSequence(t *testing.T) {
	e10, e20 := 10.0, 20.0
	first := sceneItem{
		Ref: ItemRef{DocumentID: 1, ItemID: 1},
		Track: &Track{
			Distance: 100,
			Segments: []Segment{{Start: 0, End: 2}},
			Points:   []Point{{Elevation: &e10}, {Distance: 100, Elevation: &e20}},
		},
		Color: 1,
	}
	missing := sceneItem{
		Ref: ItemRef{DocumentID: 2, ItemID: 1},
		Track: &Track{
			Distance: 200,
			Segments: []Segment{{Start: 0, End: 2}},
			Points:   []Point{{}, {Distance: 200}},
		},
		Color: 2,
	}
	last := sceneItem{
		Ref: ItemRef{DocumentID: 3, ItemID: 1},
		Track: &Track{
			Distance: 300,
			Segments: []Segment{{Start: 0, End: 2}},
			Points:   []Point{{Elevation: &e20}, {Distance: 300, Elevation: &e10}},
		},
		Color: 3,
	}
	for _, items := range [][]sceneItem{{missing, first, last}, {first, missing, last}, {first, last, missing}} {
		p := newProfile(items, 61, 5)
		missingSectionChanged := p.Distance != 600 || p.Min != 10 || p.Max != 20 || p.AvailableItems[missing.Ref]
		if missingSectionChanged {
			t.Fatal("missing elevation lost its distance or gained measurements")
		}
		var offset float64
		for _, item := range items {
			if item.Ref == missing.Ref {
				break
			}
			offset += item.Track.Distance
		}
		start := int(math.Ceil(p.screen(offset, 10).X/2)) + 1
		end := int(math.Floor(p.screen(offset+missing.Track.Distance, 10).X/2)) - 1
		for y := 0; y < p.Grid.Height; y++ {
			for x := start; x <= end; x++ {
				if p.Grid.Cells[y*p.Grid.Width+x].Bits != 0 {
					t.Fatal("profile filled a missing activity's elevation section")
				}
			}
		}
	}
}
