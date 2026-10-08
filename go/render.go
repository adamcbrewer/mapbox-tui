package main

import (
	"math"
	"sort"
	"strings"
)

type mapRole int

const (
	roleNone mapRole = iota
	roleWater
	roleRoad
	roleMajorRoad
	rolePlace
	roleRoute
	roleMarker
	roleCursor
	roleActiveRoute
	roleActiveMarker
)

var mapStyles = [...]struct {
	priority int
	ascii    rune
}{
	{priority: 0, ascii: ' '},
	{priority: 1, ascii: '~'},
	{priority: 2, ascii: '.'},
	{priority: 3, ascii: ':'},
	{priority: 4, ascii: '-'},
	{priority: 5, ascii: '*'},
	{priority: 6, ascii: 'S'},
	{priority: 9, ascii: '@'},
	{priority: 7, ascii: '#'},
	{priority: 8, ascii: 'S'},
}

type activityColor uint8

const activityColorCount = 6

type Feature struct {
	Parts   [][]XY
	Role    mapRole
	Polygon bool
	Label   string
}
type Cell struct {
	Bits  uint8
	Role  mapRole
	Text  rune
	Color activityColor
	owner ItemRef
}
type Grid struct {
	Width, Height int
	Cells         []Cell
	owner         ItemRef
	sample        *[2]int
}

type sceneItem struct {
	Ref    ItemRef
	Track  *Track
	Active bool
	Color  activityColor
}

func activitySwatch(colors ...activityColor) activityColor {
	if len(colors) > 0 {
		return colors[0]
	}
	return 0
}

func newGrid(w, h int) Grid { return Grid{Width: w, Height: h, Cells: make([]Cell, w*h)} }
func (g *Grid) cell(x, y int) *Cell {
	if g.sample != nil {
		if x != g.sample[0] || y != g.sample[1] {
			return nil
		}
		return &g.Cells[0]
	}
	return &g.Cells[y*g.Width+x]
}
func (g *Grid) dot(x, y int, role mapRole, colors ...activityColor) {
	outsideWidth := x < 0 || x >= g.Width*2
	outsideHeight := y < 0 || y >= g.Height*4
	if outsideWidth || outsideHeight {
		return
	}
	c := g.cell(x/2, y/4)
	if c == nil || mapStyles[role].priority < mapStyles[c.Role].priority {
		return
	}
	color := activitySwatch(colors...)
	if mapStyles[role].priority > mapStyles[c.Role].priority || c.Color != color {
		c.Bits = 0
		c.Text = 0
		c.Role = role
		c.Color = color
	}
	bits := [2][4]uint8{{1, 2, 4, 64}, {8, 16, 32, 128}}
	c.Bits |= bits[x%2][y%4]
	c.owner = g.owner
}
func (g *Grid) line(a, b XY, role mapRole, colors ...activityColor) {
	if g.sample != nil {
		// Reject distant lines without changing viewport clipping or raster rounding.
		x, y := float64(g.sample[0]*2), float64(g.sample[1]*4)
		outsideColumns := math.Max(a.X, b.X) < x-0.5 || math.Min(a.X, b.X) > x+1.5
		outsideRows := math.Max(a.Y, b.Y) < y-0.5 || math.Min(a.Y, b.Y) > y+3.5
		if outsideColumns || outsideRows {
			return
		}
	}
	dx, dy := b.X-a.X, b.Y-a.Y
	lo, hi := 0.0, 1.0
	for _, v := range [][2]float64{
		{-dx, a.X},
		{dx, float64(g.Width*2-1) - a.X},
		{-dy, a.Y},
		{dy, float64(g.Height*4-1) - a.Y},
	} {
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
		g.dot(
			int(math.Round(a.X)),
			int(math.Round(a.Y)),
			role,
			colors...,
		)
		return
	}
	for i := 0; i <= n; i++ {
		u := float64(i) / float64(n)
		g.dot(
			int(math.Round(a.X+(b.X-a.X)*u)),
			int(math.Round(a.Y+(b.Y-a.Y)*u)),
			role,
			colors...,
		)
	}
}
func (g *Grid) polygon(parts [][]XY, role mapRole) {
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
				g.dot(x, y, role)
			}
		}
	}
}
func (g *Grid) label(p XY, s string, role mapRole, collision bool, colors ...activityColor) {
	x, y := int(math.Floor(p.X/2)), int(math.Floor(p.Y/4))
	r := []rune(clean(s))
	outsideWidth := x < 0 || x+len(r) > g.Width
	outsideHeight := y < 0 || y >= g.Height
	if outsideWidth || outsideHeight {
		return
	}
	for i := range r {
		c := g.cell(x+i, y)
		if c == nil {
			continue
		}
		if mapStyles[c.Role].priority > mapStyles[role].priority {
			return
		}
		collides := collision && (c.Text != 0 || mapStyles[c.Role].priority >= mapStyles[roleRoute].priority)
		if collides {
			return
		}
	}
	for i, v := range r {
		if c := g.cell(x+i, y); c != nil {
			*c = Cell{Bits: 0, Role: role, Text: v, Color: activitySwatch(colors...), owner: g.owner}
		}
	}
}
func render(t *Track, c Camera, features []Feature, selected int, ascii bool) Grid {
	var items []sceneItem
	var cursor *XY
	if t != nil {
		items = []sceneItem{{Track: t, Active: true}}
		if selected >= 0 && selected < len(t.Points) {
			cursor = &t.Points[selected].World
		}
	}
	return renderScene(
		c,
		features,
		items,
		cursor,
	)
}

func renderScene(c Camera, features []Feature, items []sceneItem, cursor *XY) Grid {
	g := newGrid(c.Width, c.Height)
	for _, f := range features {
		parts := make([][]XY, len(f.Parts))
		for i, part := range f.Parts {
			for _, p := range part {
				parts[i] = append(parts[i], c.screen(p))
			}
		}
		if f.Polygon {
			g.polygon(parts, f.Role)
		} else {
			for _, part := range parts {
				for i := 1; i < len(part); i++ {
					g.line(part[i-1], part[i], f.Role)
				}
			}
		}
	}
	cursorRef, cursorColor := renderSceneItems(&g, c, items)
	g.owner = ItemRef{}
	for _, f := range features {
		hasLabelPoint := f.Label != "" && len(f.Parts) > 0 && len(f.Parts[0]) > 0
		if hasLabelPoint {
			g.label(
				c.screen(f.Parts[0][0]),
				f.Label,
				rolePlace,
				true,
			)
		}
	}
	if cursor != nil {
		g.owner = cursorRef
		g.label(
			c.screen(*cursor),
			"@",
			roleCursor,
			false,
			cursorColor,
		)
	}
	return g
}

func renderSceneCell(c Camera, items []sceneItem, cursor *XY, point XY) Cell {
	x, y := int(math.Floor(point.X/2)), int(math.Floor(point.Y/4))
	outsideWidth := x < 0 || x >= c.Width
	outsideHeight := y < 0 || y >= c.Height
	if outsideWidth || outsideHeight {
		return Cell{}
	}
	g := Grid{Width: c.Width, Height: c.Height, Cells: make([]Cell, 1), sample: &[2]int{x, y}}
	cursorRef, cursorColor := renderSceneItems(&g, c, items)
	if cursor != nil {
		g.owner = cursorRef
		g.label(
			c.screen(*cursor),
			"@",
			roleCursor,
			false,
			cursorColor,
		)
	}
	return g.Cells[0]
}

func renderSceneItems(g *Grid, c Camera, items []sceneItem) (ItemRef, activityColor) {
	var cursorRef ItemRef
	var cursorColor activityColor
	for _, item := range items {
		g.owner = item.Ref
		t := item.Track
		lineRole, markerRole := roleRoute, roleMarker
		if item.Active {
			lineRole, markerRole = roleActiveRoute, roleActiveMarker
			cursorRef = item.Ref
			cursorColor = item.Color
		}
		for _, s := range t.Segments {
			if s.Start == s.End {
				continue
			}
			last := c.screen(t.Points[s.Start].World)
			for i := s.Start + 1; i < s.End; i++ {
				p := c.screen(t.Points[i].World)
				if math.Hypot(p.X-last.X, p.Y-last.Y) >= 0.75 || i == s.End-1 {
					g.line(
						last,
						p,
						lineRole,
						item.Color,
					)
					last = p
				}
			}
			g.label(
				c.screen(t.Points[s.Start].World),
				"S",
				markerRole,
				false,
				item.Color,
			)
			g.label(
				c.screen(t.Points[s.End-1].World),
				"E",
				markerRole,
				false,
				item.Color,
			)
		}
		for _, p := range t.Waypoints {
			g.label(
				c.screen(p.World),
				"W",
				markerRole,
				false,
				item.Color,
			)
		}
	}
	return cursorRef, cursorColor
}
func (g Grid) glyph(c Cell, ascii bool) rune {
	if c.Text != 0 {
		return c.Text
	}
	if c.Bits == 0 {
		return ' '
	}
	if ascii {
		return mapStyles[c.Role].ascii
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
	c := fit(
		t,
		100,
		40,
		2,
	)
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
	fs = append(fs, Feature{Parts: [][]XY{top}, Role: roleWater, Polygon: true})
	for i := -8; i <= 8; i++ {
		o := float64(i) * d / 6
		role := roleRoad
		if i%3 == 0 {
			role = roleMajorRoad
		}
		fs = append(fs,
			Feature{
				Parts: [][]XY{{{cx - d*2, cy + o}, {cx + d*2, cy + o + d*0.12}}},
				Role:  role,
			},
			Feature{
				Parts: [][]XY{{{cx + o, cy - d*2}, {cx + o + d*0.08, cy + d*2}}},
				Role:  role,
			},
		)
	}
	fs = append(
		fs,
		Feature{
			Parts: [][]XY{{
				{cx - d*0.4, cy - d*0.22},
				{cx, cy - d*0.4},
				{cx + d*0.4, cy - d*0.25},
			}},
			Role: roleRoad,
		},
		Feature{
			Parts: [][]XY{{{cx - d*0.4, cy - d*0.38}}},
			Label: "Synthetic village",
		},
		Feature{
			Parts: [][]XY{{{cx + d*0.25, cy + d*0.28}}},
			Label: "Synthetic river",
		},
	)
	return fs
}
