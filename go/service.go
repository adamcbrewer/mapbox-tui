package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"mapbox-tui/shared"
)

type Args struct {
	Journey     *startupJourney `json:"-"`
	Path        string          `json:"path"`
	TrackID     string          `json:"track_id"`
	ItemID      *int            `json:"item_id"`
	Workspace   bool            `json:"workspace"`
	Visible     *bool           `json:"visible"`
	PointIndex  *int            `json:"point_index"`
	Distance    *float64        `json:"distance_m"`
	Width       *int            `json:"width"`
	Height      *int            `json:"height"`
	Lon         *float64        `json:"center_lon"`
	Lat         *float64        `json:"center_lat"`
	Zoom        *float64        `json:"zoom"`
	Bearing     *float64        `json:"bearing"`
	Aspect      *float64        `json:"cell_aspect"`
	ASCII       bool            `json:"ascii"`
	Provider    string          `json:"provider"`
	ProviderSet bool            `json:"-"`
	NoAnimation bool            `json:"-"`
	Theme       string          `json:"-"`
}

type Service struct {
	mu                sync.Mutex
	workspace         Workspace
	providers         map[string]*Provider
	credentialChecked bool
	credentialPending chan struct{}
}

type trackInfo struct {
	TrackID    string     `json:"track_id"`
	DocumentID int        `json:"document_id"`
	Filename   string     `json:"filename"`
	Path       string     `json:"path"`
	Name       string     `json:"name"`
	Bounds     [4]float64 `json:"bounds"`
	Items      []itemInfo `json:"items"`
}

func describeTrack(entry *workspaceDocument) trackInfo {
	items := entry.document.itemInfos()
	for i := range items {
		items[i].Visible = !entry.hidden[items[i].ID]
	}
	d := entry.document
	return trackInfo{
		TrackID:    "track-" + strconv.Itoa(entry.id),
		DocumentID: entry.id,
		Filename:   d.Filename,
		Path:       d.Path,
		Name:       d.Name,
		Bounds:     d.Bounds,
		Items:      items,
	}
}

func (s *Service) loadedResult(ref ItemRef) map[string]any {
	entry, _, _ := s.workspace.lookup(ref)
	info := describeTrack(entry)
	return map[string]any{
		"track_id":    info.TrackID,
		"bounds":      info.Bounds,
		"summary":     entry.document.track().summary(),
		"document_id": info.DocumentID,
		"filename":    info.Filename,
		"items":       info.Items,
	}
}

func (s *Service) load(path string) (any, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if entry := s.workspace.documentAtPath(path); entry != nil {
		result := s.loadedResult(s.workspace.activateDocument(entry))
		s.mu.Unlock()
		return result, nil
	}
	s.mu.Unlock()
	d, err := loadDocument(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadedResult(s.workspace.addDocument(d)), nil
}

func (s *Service) document(id string) (*workspaceDocument, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "track-"))
	if err == nil && id == "track-"+strconv.Itoa(n) {
		for _, entry := range s.workspace.documents {
			if entry.id == n {
				return entry, nil
			}
		}
	}
	return nil, fmt.Errorf("unknown track_id")
}

func (s *Service) render(ctx context.Context, a Args) (any, error) {
	kind := a.Provider
	if kind != "" && kind != "mapbox" && kind != "none" {
		return nil, fmt.Errorf("unknown provider")
	}
	s.mu.Lock()
	r, err := s.mapRequest(a)
	p := s.providers["mapbox"]
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if kind != "none" {
		p, err = s.mapboxProvider(ctx)
		if err != nil {
			return nil, err
		}
	}
	if kind == "" {
		kind = defaultProvider(p.token)
	}
	if kind == "none" {
		p = providerForCredential("none", renderingCredential{guidance: p.guidance})
		p.quiet = a.Provider == "none"
	}
	return r.render(ctx, a.ASCII, p)
}

func (s *Service) mapboxProvider(ctx context.Context) (*Provider, error) {
	for {
		s.mu.Lock()
		p := s.providers["mapbox"]
		if p.token != "" || s.credentialChecked {
			s.mu.Unlock()
			return p, nil
		}
		if pending := s.credentialPending; pending != nil {
			s.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		s.credentialPending = make(chan struct{})
		s.mu.Unlock()

		credential := resolveCredential(ctx)
		s.mu.Lock()
		if ctx.Err() == nil {
			p = providerForCredential("mapbox", credential)
			s.providers["mapbox"], s.credentialChecked = p, true
		}
		close(s.credentialPending)
		s.credentialPending = nil
		s.mu.Unlock()
		return p, ctx.Err()
	}
}

func (s *Service) mapRequest(a Args) (mapRequest, error) {
	if a.Workspace {
		return workspaceMapRequest(&s.workspace, a)
	}
	var t *Track
	if a.TrackID != "" {
		entry, err := s.document(a.TrackID)
		if err != nil {
			return mapRequest{}, err
		}
		t, err = entry.document.selectedTrack(a.ItemID)
		if err != nil {
			return mapRequest{}, err
		}
	} else if a.ItemID != nil {
		return mapRequest{}, fmt.Errorf("item_id requires track_id")
	}
	return trackMapRequest(t, a)
}

func (s *Service) call(ctx context.Context, name string, a Args) (any, error) {
	if name == "load_track" {
		return s.load(a.Path)
	}
	if name == "render_map" {
		return s.render(ctx, a)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "list_tracks" {
		tracks := make([]trackInfo, 0, len(s.workspace.documents))
		for _, entry := range s.workspace.documents {
			tracks = append(tracks, describeTrack(entry))
		}
		var active *ItemRef
		if s.workspace.selection != nil {
			ref := s.workspace.selection.ref
			active = &ref
		}
		return map[string]any{"tracks": tracks, "active_item": active}, nil
	}
	entry, err := s.document(a.TrackID)
	if err != nil {
		return nil, err
	}
	switch name {
	case "summarize_track", "inspect_track":
		t, err := entry.document.selectedTrack(a.ItemID)
		if err != nil {
			return nil, err
		}
		if name == "summarize_track" {
			return t.summary(), nil
		}
		return t.inspect(a.PointIndex, a.Distance)
	case "unload_track":
		err = s.workspace.removeDocument(entry.id)
	case "set_item_visibility":
		if a.Visible == nil {
			return nil, fmt.Errorf("visible is required")
		}
		if a.ItemID == nil {
			err = s.workspace.setDocumentVisible(entry.id, *a.Visible)
		} else {
			err = s.workspace.setVisible(ItemRef{DocumentID: entry.id, ItemID: *a.ItemID}, *a.Visible)
		}
	case "select_item", "remove_item":
		if a.ItemID == nil {
			return nil, fmt.Errorf("item_id is required")
		}
		ref := ItemRef{DocumentID: entry.id, ItemID: *a.ItemID}
		if name == "select_item" {
			err = s.workspace.selectItem(ref)
		} else {
			err = s.workspace.removeItem(ref)
		}
	default:
		return nil, fmt.Errorf("unknown tool")
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"track_id": a.TrackID, "ok": true}, nil
}

func serveMCP() error {
	s := &Service{providers: map[string]*Provider{
		"mapbox": newProvider("mapbox"),
		"none":   newProvider("none"),
	}}
	server := mcp.NewServer(&mcp.Implementation{Name: "mapbox-tui", Version: "0.1.0"}, nil)
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
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: e.Error()}},
				}, nil
			}
			b, e := json.Marshal(v)
			if e != nil {
				return nil, e
			}
			return &mcp.CallToolResult{
				StructuredContent: v,
				Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
			}, nil
		})
	}
	return server.Run(context.Background(), &mcp.StdioTransport{})
}
