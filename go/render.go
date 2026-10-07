package main

import (
	"math"
	"sort"
	"strings"
)

type Feature struct {
	Parts   [][]XY
	Layer   int
	Polygon bool
	Label   string
}
type Cell struct {
	Bits  uint8
	Layer int
	Text  rune
}
type Grid struct {
	Width, Height int
	Cells         []Cell
}

func newGrid(w, h int) Grid { return Grid{w, h, make([]Cell, w*h)} }
func (g *Grid) dot(x, y, layer int) {
	if x < 0 || y < 0 || x >= g.Width*2 || y >= g.Height*4 {
		return
	}
	c := &g.Cells[y/4*g.Width+x/2]
	if layer < c.Layer {
		return
	}
	if layer > c.Layer {
		c.Bits = 0
		c.Text = 0
		c.Layer = layer
	}
	bits := [2][4]uint8{{1, 2, 4, 64}, {8, 16, 32, 128}}
	c.Bits |= bits[x%2][y%4]
}
func (g *Grid) line(a, b XY, layer int) {
	dx, dy := b.X-a.X, b.Y-a.Y
	lo, hi := 0.0, 1.0
	for _, v := range [][2]float64{{-dx, a.X}, {dx, float64(g.Width*2-1) - a.X}, {-dy, a.Y}, {dy, float64(g.Height*4-1) - a.Y}} {
		if v[0] == 0 {
			if v[1] < 0 {
				return
			}
			continue
		}
		r := v[1] / v[0]
		if v[0] < 0 {
			lo = math.Max(lo, r)
		} else {
			hi = math.Min(hi, r)
		}
	}
	if lo > hi {
		return
	}
	b = XY{a.X + dx*hi, a.Y + dy*hi}
	a = XY{a.X + dx*lo, a.Y + dy*lo}
	n := int(math.Ceil(math.Max(math.Abs(b.X-a.X), math.Abs(b.Y-a.Y))))
	if n == 0 {
		g.dot(int(math.Round(a.X)), int(math.Round(a.Y)), layer)
		return
	}
	for i := 0; i <= n; i++ {
		u := float64(i) / float64(n)
		g.dot(int(math.Round(a.X+(b.X-a.X)*u)), int(math.Round(a.Y+(b.Y-a.Y)*u)), layer)
	}
}
func (g *Grid) polygon(parts [][]XY, layer int) {
	for y := 0; y < g.Height*4; y++ {
		xs := []float64{}
		sy := float64(y) + 0.5
		for _, ring := range parts {
			for i, a := range ring {
				b := ring[(i+1)%len(ring)]
				if (a.Y > sy) != (b.Y > sy) {
					xs = append(xs, a.X+(sy-a.Y)*(b.X-a.X)/(b.Y-a.Y))
				}
			}
		}
		sort.Float64s(xs)
		for i := 0; i+1 < len(xs); i += 2 {
			for x := int(math.Max(0, math.Ceil(xs[i]))); x <= int(math.Min(float64(g.Width*2-1), xs[i+1])); x++ {
				g.dot(x, y, layer)
			}
		}
	}
}
func (g *Grid) label(p XY, s string, layer int, collision bool) {
	x, y := int(math.Floor(p.X/2)), int(math.Floor(p.Y/4))
	r := []rune(clean(s))
	if x < 0 || y < 0 || x+len(r) > g.Width || y >= g.Height {
		return
	}
	for i := range r {
		c := g.Cells[y*g.Width+x+i]
		if c.Layer > layer || (collision && (c.Text != 0 || c.Layer >= 5)) {
			return
		}
	}
	for i, v := range r {
		g.Cells[y*g.Width+x+i] = Cell{0, layer, v}
	}
}
func render(t *Track, c Camera, features []Feature, selected int, ascii bool) Grid {
	g := newGrid(c.Width, c.Height)
	for _, f := range features {
		parts := make([][]XY, len(f.Parts))
		for i, part := range f.Parts {
			for _, p := range part {
				parts[i] = append(parts[i], c.screen(p))
			}
		}
		if f.Polygon {
			g.polygon(parts, f.Layer)
		} else {
			for _, part := range parts {
				for i := 1; i < len(part); i++ {
					g.line(part[i-1], part[i], f.Layer)
				}
			}
		}
	}
	if t != nil {
		for _, s := range t.Segments {
			if s.Start == s.End {
				continue
			}
			last := c.screen(t.Points[s.Start].World)
			for i := s.Start + 1; i < s.End; i++ {
				p := c.screen(t.Points[i].World)
				if math.Hypot(p.X-last.X, p.Y-last.Y) >= 0.75 || i == s.End-1 {
					g.line(last, p, 5)
					last = p
				}
			}
			g.label(c.screen(t.Points[s.Start].World), "S", 6, false)
			g.label(c.screen(t.Points[s.End-1].World), "E", 6, false)
		}
		for _, p := range t.Waypoints {
			g.label(c.screen(p.World), "W", 6, false)
		}
	}
	for _, f := range features {
		if f.Label != "" && len(f.Parts) > 0 && len(f.Parts[0]) > 0 {
			g.label(c.screen(f.Parts[0][0]), f.Label, 4, true)
		}
	}
	if t != nil && selected >= 0 && selected < len(t.Points) {
		g.label(c.screen(t.Points[selected].World), "@", 7, false)
	}
	_ = ascii
	return g
}
func (g Grid) glyph(c Cell, ascii bool) rune {
	if c.Text != 0 {
		return c.Text
	}
	if c.Bits == 0 {
		return ' '
	}
	if ascii {
		return []rune(" ~.:-#S@")[min(c.Layer, 7)]
	}
	return rune(0x2800) + rune(c.Bits)
}
func (g Grid) plain(ascii bool) string {
	var b strings.Builder
	for y := 0; y < g.Height; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := 0; x < g.Width; x++ {
			b.WriteRune(g.glyph(g.Cells[y*g.Width+x], ascii))
		}
	}
	return b.String()
}
func benchmarkFeatures(t *Track) []Feature {
	c := fit(t, 100, 40, 2)
	cx, cy := c.Center.X, c.Center.Y
	d := math.Max(project(t.Bounds[2], t.Bounds[1]).X-project(t.Bounds[0], t.Bounds[3]).X, 0.00008)
	fs := []Feature{}
	top, bottom := []XY{}, []XY{}
	for i := 0; i <= 80; i++ {
		u := float64(i) / 80
		x := cx + (u-0.5)*d*4
		y := cy + math.Sin(u*8)*d*0.12 + d*0.20
		top = append(top, XY{x, y})
		bottom = append(bottom, XY{x, y + d*0.055})
	}
	for i := len(bottom) - 1; i >= 0; i-- {
		top = append(top, bottom[i])
	}
	fs = append(fs, Feature{Parts: [][]XY{top}, Layer: 1, Polygon: true})
	for i := -8; i <= 8; i++ {
		o := float64(i) * d / 6
		layer := 2
		if i%3 == 0 {
			layer = 3
		}
		fs = append(fs, Feature{Parts: [][]XY{{{cx - d*2, cy + o}, {cx + d*2, cy + o + d*0.12}}}, Layer: layer}, Feature{Parts: [][]XY{{{cx + o, cy - d*2}, {cx + o + d*0.08, cy + d*2}}}, Layer: layer})
	}
	fs = append(fs, Feature{Parts: [][]XY{{{cx - d*0.4, cy - d*0.22}, {cx, cy - d*0.4}, {cx + d*0.4, cy - d*0.25}}}, Layer: 2}, Feature{Parts: [][]XY{{{cx - d*0.4, cy - d*0.38}}}, Label: "Synthetic village"}, Feature{Parts: [][]XY{{{cx + d*0.25, cy + d*0.28}}}, Label: "Synthetic river"})
	return fs
}
