package main

import (
	"fmt"
	"math"
	"testing"
)

func BenchmarkWorkspaceScene(b *testing.B) {
	for _, count := range []int{1, 4} {
		b.Run(fmt.Sprintf("%d_routes_100k", count), func(b *testing.B) {
			var w Workspace
			for route := range count {
				d := &Document{Path: fmt.Sprintf("route-%d.gpx", route), Name: "Synthetic ride"}
				t := d.addItem("track").Track
				t.Points = make([]Point, 100000)
				for i := range t.Points {
					u := float64(i) / 99999 * 2 * math.Pi
					t.Points[i] = Point{
						Lon: -.105 + math.Cos(u)*.018 + float64(route)*.004,
						Lat: 51.505 + math.Sin(u)*.006,
					}
				}
				t.Segments = []Segment{{Kind: "track", Start: 0, End: len(t.Points)}}
				if err := d.prepare(); err != nil {
					b.Fatal(err)
				}
				w.addDocument(d)
			}
			c := w.fitVisible(120, 40, 2)
			b.ReportAllocs()
			for b.Loop() {
				c.pan(.2, 0)
				g := renderScene(
					c,
					nil,
					w.sceneItems(),
					nil,
				)
				_ = g.plain(false)
			}
		})
	}
}
