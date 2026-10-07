package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"shell-on-earth/shared"
)

type Args struct {
	Path        string   `json:"path"`
	TrackID     string   `json:"track_id"`
	PointIndex  *int     `json:"point_index"`
	Distance    *float64 `json:"distance_m"`
	Width       *int     `json:"width"`
	Height      *int     `json:"height"`
	Lon         *float64 `json:"center_lon"`
	Lat         *float64 `json:"center_lat"`
	Zoom        *float64 `json:"zoom"`
	Bearing     *float64 `json:"bearing"`
	Aspect      *float64 `json:"cell_aspect"`
	ASCII       bool     `json:"ascii"`
	Provider    string   `json:"provider"`
	NoAnimation bool     `json:"-"`
}

func viewport(t *Track, a Args) (Camera, error) {
	w, h, aspect := 120, 40, 2.0
	if a.Width != nil {
		w = *a.Width
	}
	if a.Height != nil {
		h = *a.Height
	}
	if a.Aspect != nil {
		aspect = *a.Aspect
	}
	if w < 10 || w > 300 || h < 5 || h > 100 || aspect < 1 || aspect > 3 || math.IsNaN(aspect) {
		return Camera{}, fmt.Errorf("viewport: width 10..300, height 5..100, cell_aspect 1..3")
	}
	c := fit(t, w, h, aspect)
	if (a.Lon == nil) != (a.Lat == nil) {
		return c, fmt.Errorf("supply center_lon and center_lat together")
	}
	if a.Lon != nil {
		if math.IsNaN(*a.Lon) || math.IsNaN(*a.Lat) || math.Abs(*a.Lon) > 180 || math.Abs(*a.Lat) > 85.05112878 {
			return c, fmt.Errorf("center outside Mercator range")
		}
		c.Center = project(*a.Lon, *a.Lat)
	}
	if a.Zoom != nil {
		if math.IsNaN(*a.Zoom) || *a.Zoom < 0 || *a.Zoom > 20 {
			return c, fmt.Errorf("zoom outside 0..20")
		}
		c.Zoom = *a.Zoom
	}
	if a.Bearing != nil {
		if math.IsNaN(*a.Bearing) || math.Abs(*a.Bearing) > 360 {
			return c, fmt.Errorf("bearing outside -360..360")
		}
		c.Bearing = *a.Bearing
	}
	return c, nil
}
func renderResult(ctx context.Context, t *Track, a Args, p *Provider) (map[string]any, error) {
	c, err := viewport(t, a)
	if err != nil {
		return nil, err
	}
	sel := -1
	if a.PointIndex != nil {
		sel = *a.PointIndex
		if t == nil || sel < 0 || sel >= len(t.Points) {
			return nil, fmt.Errorf("point_index outside track")
		}
	}
	fs, stats, status := p.scene(ctx, t, c)
	grid := render(t, c, fs, sel, a.ASCII)
	center := unproject(c.Center)
	return map[string]any{"text": grid.plain(a.ASCII), "scene": map[string]any{"width": c.Width, "height": c.Height, "center_lon": center.X, "center_lat": center.Y, "zoom": c.Zoom, "bearing": c.Bearing, "cell_aspect": c.Aspect, "provider": p.Kind, "attribution": attribution(p.Kind), "attribution_links": []string{"https://www.mapbox.com/about/maps/", "https://www.openstreetmap.org/copyright", "https://apps.mapbox.com/feedback/"}, "tile_stats": stats, "status": status, "selected_point_index": sel}}, nil
}

type Service struct {
	mu        sync.Mutex
	tracks    map[string]*Track
	providers map[string]*Provider
	next      int
}

func (s *Service) call(ctx context.Context, name string, a Args) (any, error) {
	if name == "load_track" {
		t, e := loadTrack(a.Path)
		if e != nil {
			return nil, e
		}
		s.mu.Lock()
		s.next++
		id := fmt.Sprintf("track-%d", s.next)
		s.tracks[id] = t
		s.mu.Unlock()
		return map[string]any{"track_id": id, "bounds": t.Bounds, "summary": t.summary()}, nil
	}
	s.mu.Lock()
	t := s.tracks[a.TrackID]
	s.mu.Unlock()
	if t == nil && (name != "render_map" || a.TrackID != "") {
		return nil, fmt.Errorf("unknown track_id")
	}
	switch name {
	case "summarize_track":
		return t.summary(), nil
	case "inspect_track":
		return t.inspect(a.PointIndex, a.Distance)
	case "render_map":
		kind := a.Provider
		if kind == "" {
			kind = defaultProvider(s.providers["mapbox"].token)
		}
		if kind != "mapbox" && kind != "none" {
			return nil, fmt.Errorf("unknown provider")
		}
		return renderResult(ctx, t, a, s.providers[kind])
	}
	return nil, fmt.Errorf("unknown tool")
}
func serveMCP() error {
	s := &Service{tracks: map[string]*Track{}, providers: map[string]*Provider{"mapbox": newProvider("mapbox"), "none": newProvider("none")}}
	server := mcp.NewServer(&mcp.Implementation{Name: "shoe", Version: "0.1.0"}, nil)
	var tools []*mcp.Tool
	if e := json.Unmarshal(shared.Tools, &tools); e != nil {
		return e
	}
	for _, tool := range tools {
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var a Args
			if e := json.Unmarshal(req.Params.Arguments, &a); e != nil {
				return nil, e
			}
			v, e := s.call(ctx, req.Params.Name, a)
			if e != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: e.Error()}}}, nil
			}
			b, e := json.Marshal(v)
			if e != nil {
				return nil, e
			}
			return &mcp.CallToolResult{StructuredContent: v, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
		})
	}
	return server.Run(context.Background(), &mcp.StdioTransport{})
}
