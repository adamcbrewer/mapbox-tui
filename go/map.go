package main

import (
	"context"
	"fmt"
	"math"
)

type mapRequest struct {
	camera     Camera
	items      []sceneItem
	cursor     *XY
	selected   int
	active     *ItemRef
	workspace  bool
	hasImports bool
}

func viewport(t *Track, a Args) (Camera, error) {
	var bounds *[4]float64
	if t != nil {
		bounds = &t.Bounds
	}
	return viewportBounds(bounds, a)
}

func viewportBounds(bounds *[4]float64, a Args) (Camera, error) {
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
	invalidWidth := w < 10 || w > 300
	invalidHeight := h < 5 || h > 100
	invalidAspect := aspect < 1 || aspect > 3 || math.IsNaN(aspect)
	if invalidWidth || invalidHeight || invalidAspect {
		return Camera{}, fmt.Errorf("viewport: width 10..300, height 5..100, cell_aspect 1..3")
	}
	c := fit(
		nil,
		w,
		h,
		aspect,
	)
	if bounds != nil {
		c = fitBounds(
			*bounds,
			w,
			h,
			aspect,
		)
	}
	if (a.Lon == nil) != (a.Lat == nil) {
		return c, fmt.Errorf("supply center_lon and center_lat together")
	}
	if a.Lon != nil {
		invalidLongitude := math.IsNaN(*a.Lon) || math.Abs(*a.Lon) > 180
		invalidLatitude := math.IsNaN(*a.Lat) || math.Abs(*a.Lat) > 85.05112878
		if invalidLongitude || invalidLatitude {
			return c, fmt.Errorf("center outside Mercator range")
		}
		c.Center = project(*a.Lon, *a.Lat)
	}
	if a.Zoom != nil {
		invalidZoom := math.IsNaN(*a.Zoom) || *a.Zoom < minZoom || *a.Zoom > 20
		if invalidZoom {
			return c, fmt.Errorf("zoom outside -8..20")
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

func trackMapRequest(t *Track, a Args) (mapRequest, error) {
	c, err := viewport(t, a)
	if err != nil {
		return mapRequest{}, err
	}
	r := mapRequest{camera: c, selected: -1, hasImports: t != nil}
	if t != nil {
		r.items = []sceneItem{{Track: t, Active: true}}
	}
	if a.PointIndex != nil {
		r.selected = *a.PointIndex
		outsideTrack := t == nil || r.selected < 0 || r.selected >= len(t.Points)
		if outsideTrack {
			return r, fmt.Errorf("point_index outside track")
		}
		p := t.Points[r.selected].World
		r.cursor = &p
	}
	return r, nil
}

func workspaceMapRequest(w *Workspace, a Args) (mapRequest, error) {
	hasSelector := a.TrackID != "" || a.ItemID != nil || a.PointIndex != nil
	if hasSelector {
		return mapRequest{}, fmt.Errorf("workspace rendering uses the active item; omit track_id, item_id and point_index")
	}
	var bounds *[4]float64
	if b, ok := w.visibleBounds(); ok {
		bounds = &b
	}
	c, err := viewportBounds(bounds, a)
	if err != nil {
		return mapRequest{}, err
	}
	r := mapRequest{
		camera:     c,
		items:      w.sceneItems(),
		selected:   -1,
		workspace:  true,
		hasImports: len(w.documents) > 0,
	}
	if w.selection != nil {
		ref := w.selection.ref
		r.active = &ref
		if p := w.position(); p != nil {
			r.cursor = &p.World
			r.selected = w.selection.playback.nearest()
		}
	}
	return r, nil
}

func (r mapRequest) render(ctx context.Context, ascii bool, p *Provider) (map[string]any, error) {
	snapshot, err := p.load(ctx, r.camera)
	if err != nil {
		return nil, err
	}
	sources := sourceView{Sources: []sourceInfo{}}
	if p.Kind == "mapbox" {
		sources.add("basemap", snapshot, false)
	}
	status := sources.status()
	if status == "" {
		status = providerGuidance(p, r.hasImports)
	}
	credits, links := sources.attribution()
	grid := renderScene(
		r.camera,
		sources.Features,
		r.items,
		r.cursor,
	)
	c := r.camera
	center := unproject(c.Center)
	scene := map[string]any{
		"width":                c.Width,
		"height":               c.Height,
		"center_lon":           center.X,
		"center_lat":           center.Y,
		"zoom":                 c.Zoom,
		"bearing":              c.Bearing,
		"cell_aspect":          c.Aspect,
		"provider":             p.Kind,
		"attribution":          credits,
		"attribution_links":    links,
		"tile_stats":           sources.Stats,
		"status":               status,
		"sources":              sources.Sources,
		"selected_point_index": r.selected,
	}
	if r.workspace {
		refs := make([]ItemRef, 0, len(r.items))
		for _, item := range r.items {
			refs = append(refs, item.Ref)
		}
		scene["active_item"], scene["visible_items"] = r.active, refs
	}
	return map[string]any{"text": grid.plain(ascii), "scene": scene}, nil
}

func renderResult(ctx context.Context, t *Track, a Args, p *Provider) (map[string]any, error) {
	r, err := trackMapRequest(t, a)
	if err != nil {
		return nil, err
	}
	return r.render(ctx, a.ASCII, p)
}

func renderFiles(ctx context.Context, paths []string, a Args, p *Provider) (map[string]any, error) {
	if len(paths) <= 1 {
		var track *Track
		if len(paths) == 1 {
			d, err := loadDocument(paths[0])
			if err != nil {
				return nil, err
			}
			track, err = d.selectedTrack(a.ItemID)
			if err != nil {
				return nil, err
			}
		} else if a.ItemID != nil {
			return nil, fmt.Errorf("item_id requires a track file")
		}
		return renderResult(
			ctx,
			track,
			a,
			p,
		)
	}
	var w Workspace
	for _, path := range paths {
		if _, err := w.open(path); err != nil {
			return nil, err
		}
	}
	r, err := workspaceMapRequest(&w, a)
	if err != nil {
		return nil, err
	}
	return r.render(ctx, a.ASCII, p)
}
