package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
)

type Tile struct{ Z, X, Y int }
type cacheEntry struct {
	Features  []Feature
	Expires   time.Time
	FetchedAt time.Time
	Used      uint64
}
type Stats struct {
	Requests, Hits, Errors uint64
	Cached                 int
}
type Provider struct {
	Kind        string
	client      *http.Client
	token       string
	guidance    string
	tileset     string
	unavailable string
	quiet       bool
	cache       map[Tile]cacheEntry
	stats       Stats
	clock       uint64
	retry       time.Time
	mu          sync.Mutex
	batch       chan struct{}
	now         func() time.Time
}

const mapboxTokenMissing = "Mapbox token missing; " + mapboxLoginGuidance + "; or set MAPBOX_ACCESS_TOKEN"
const mapboxDisconnectedNotice = "No basemap · Mapbox not connected"

func providerConnectionNotice(p *Provider) string {
	if p.quiet || p.token != "" {
		return ""
	}
	return mapboxDisconnectedNotice
}

func defaultProvider(token string) string {
	if token != "" {
		return "mapbox"
	}
	return "none"
}

func newProvider(kind string) *Provider {
	return &Provider{
		Kind: kind,
		client: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		token:   accessToken(),
		tileset: "mapbox.mapbox-streets-v8",
		cache:   map[Tile]cacheEntry{},
		batch:   make(chan struct{}, 1),
		now:     time.Now,
	}
}
func accessToken() string {
	return os.Getenv("MAPBOX_ACCESS_TOKEN")
}
func providerCredits(kind string) []sourceCredit {
	if kind == "mapbox" {
		return []sourceCredit{
			{Text: "© Mapbox", URL: "https://www.mapbox.com/about/maps/"},
			{Text: "© OpenStreetMap", URL: "https://www.openstreetmap.org/copyright"},
			{Text: "Improve this map (v credits)", URL: "https://apps.mapbox.com/feedback/"},
		}
	}
	return nil
}
func providerGuidance(p *Provider, hasImports bool) string {
	if p.quiet {
		return ""
	}
	if p.Kind == "none" && p.token == "" && !hasImports {
		if p.guidance != "" {
			return p.guidance
		}
		return mapboxTokenMissing
	}
	return ""
}
func visible(c Camera) []Tile {
	z := int(math.Max(0, math.Min(14, math.Floor(c.Zoom))))
	a, b := visibleBounds(c)
	return tilesForBounds(a, b, z)
}

func visibleBounds(c Camera) (XY, XY) {
	a, b := XY{math.Inf(1), math.Inf(1)}, XY{math.Inf(-1), math.Inf(-1)}
	for _, s := range []XY{{0, 0}, {float64(c.Width * 2), 0}, {0, float64(c.Height * 4)}, {float64(c.Width * 2), float64(c.Height * 4)}} {
		p := c.world(s)
		a.X = math.Min(a.X, p.X)
		a.Y = math.Min(a.Y, p.Y)
		b.X = math.Max(b.X, p.X)
		b.Y = math.Max(b.Y, p.Y)
	}
	return a, b
}

func tilesForBounds(a, b XY, z int) []Tile {
	if a.X > b.X || a.Y > b.Y {
		return nil
	}
	for {
		n := 1 << z
		x0, x1 := max(0, int(math.Floor(a.X*float64(n)))), min(n-1, int(math.Floor(b.X*float64(n))))
		y0, y1 := max(0, int(math.Floor(a.Y*float64(n)))), min(n-1, int(math.Floor(b.Y*float64(n))))
		if (x1-x0+1)*(y1-y0+1) > 64 && z > 0 {
			z--
			continue
		}
		out := []Tile{}
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				out = append(out, Tile{z, x, y})
			}
		}
		return out
	}
}
func (p *Provider) sameRequest(a, b Camera) bool {
	if a == b {
		return true
	}
	usesTiles := p.Kind == "mapbox" && p.token != ""
	if !usesTiles {
		return true
	}
	return slices.Equal(p.requestTiles(a), p.requestTiles(b))
}

func (p *Provider) requestTiles(c Camera) []Tile {
	if p.tileset == trafficTileset {
		return trafficTiles(c)
	}
	return visible(c)
}
func ttl(header, ageHeader string) time.Duration {
	age := 0
	for _, s := range strings.Split(header, ",") {
		s = strings.TrimSpace(s)
		if s == "no-store" || s == "no-cache" {
			return 0
		}
		if strings.HasPrefix(s, "max-age=") {
			age, _ = strconv.Atoi(strings.TrimPrefix(s, "max-age="))
		}
	}
	if elapsed, err := strconv.Atoi(ageHeader); err == nil && elapsed > 0 {
		age -= min(age, elapsed)
	}
	return time.Duration(max(0, min(age, 43200))) * time.Second
}
func decodeTile(data []byte, t Tile) ([]Feature, error) {
	layers, err := mvt.Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("invalid vector tile")
	}
	out := []Feature{}
	n := math.Exp2(float64(t.Z))
	for _, l := range layers {
		if l.Extent == 0 {
			continue
		}
		for _, f := range l.Features {
			role := roleNone
			label := ""
			switch l.Name {
			case "water":
				role = roleWater
			case "road":
				role = roleRoad
				if cl, _ := f.Properties["class"].(string); cl == "motorway" || cl == "trunk" || cl == "primary" || cl == "secondary" {
					role = roleMajorRoad
				}
			case "place_label":
				role = rolePlace
				label, _ = f.Properties["name_en"].(string)
				if label == "" {
					label, _ = f.Properties["name"].(string)
				}
			case "traffic":
				role = trafficFeatureRole(f.Properties)
				if role == roleNone {
					continue
				}
			default:
				continue
			}
			convert := func(line orb.LineString) []XY {
				p := make([]XY, len(line))
				for i, v := range line {
					p[i] = XY{(float64(t.X) + v[0]/float64(l.Extent)) / n, (float64(t.Y) + v[1]/float64(l.Extent)) / n}
				}
				return p
			}
			add := func(parts [][]XY, polygon bool) {
				out = append(out, Feature{Parts: parts, Role: role, Polygon: polygon, Label: clean(label)})
			}
			switch g := f.Geometry.(type) {
			case orb.Point:
				add([][]XY{convert(orb.LineString{g})}, false)
			case orb.LineString:
				add([][]XY{convert(g)}, false)
			case orb.MultiLineString:
				for _, v := range g {
					add([][]XY{convert(v)}, false)
				}
			case orb.Polygon:
				parts := [][]XY{}
				for _, v := range g {
					parts = append(parts, convert(orb.LineString(v)))
				}
				add(parts, true)
			case orb.MultiPolygon:
				for _, poly := range g {
					parts := [][]XY{}
					for _, v := range poly {
						parts = append(parts, convert(orb.LineString(v)))
					}
					add(parts, true)
				}
			}
		}
	}
	return out, nil
}
func (p *Provider) load(ctx context.Context, c Camera) (sourceSnapshot, error) {
	snapshot := sourceSnapshot{Credits: providerCredits(p.Kind), Stats: p.snapshotStats()}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	if p.Kind != "mapbox" {
		return snapshot, nil
	}
	if p.token == "" {
		snapshot.Status = mapboxTokenMissing
		if p.tileset == trafficTileset {
			snapshot.Status = "Traffic unavailable; connect Mapbox in settings"
			snapshot.Credits = nil
			return snapshot, nil
		}
		if p.guidance != "" {
			snapshot.Status = p.guidance
		}
		return snapshot, nil
	}
	p.mu.Lock()
	unavailable := p.unavailable
	p.mu.Unlock()
	if unavailable != "" {
		snapshot.Status, snapshot.Failed, snapshot.Unavailable = unavailable, true, true
		return snapshot, nil
	}
	select {
	case p.batch <- struct{}{}:
		defer func() { <-p.batch }()
	case <-ctx.Done():
		return snapshot, ctx.Err()
	}
	var retryAt time.Time
	for _, tile := range p.requestTiles(c) {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		now := p.now()
		p.mu.Lock()
		p.clock++
		entry, ok := p.cache[tile]
		if ok && now.Before(entry.Expires) {
			p.stats.Hits++
			entry.Used = p.clock
			p.cache[tile] = entry
			p.mu.Unlock()
			snapshot.Features = append(snapshot.Features, entry.Features...)
			snapshot.RefreshAt = earlier(snapshot.RefreshAt, entry.Expires)
			snapshot.FetchedAt = earlier(snapshot.FetchedAt, entry.FetchedAt)
			continue
		}
		delete(p.cache, tile)
		if now.Before(p.retry) {
			retryAt = p.retry
			p.mu.Unlock()
			if snapshot.Status == "" {
				snapshot.Status = "Tile requests paused after provider failure"
			}
			continue
		}
		p.mu.Unlock()
		u := fmt.Sprintf(
			"https://api.mapbox.com/v4/%s/%d/%d/%d.mvt?access_token=%s",
			p.tileset,
			tile.Z,
			tile.X,
			tile.Y,
			url.QueryEscape(p.token),
		)
		req, err := http.NewRequestWithContext(
			ctx,
			"GET",
			u,
			nil,
		)
		if err != nil {
			snapshot.Status = "Cannot create tile request"
			retryAt = p.fail(10 * time.Second)
			continue
		}
		p.mu.Lock()
		p.stats.Requests++
		p.mu.Unlock()
		res, err := p.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return snapshot, ctx.Err()
			}
			snapshot.Status = "Tile network request failed"
			retryAt = p.fail(10 * time.Second)
			continue
		}
		received := p.now()
		if res.StatusCode != 200 {
			res.Body.Close()
			snapshot.AccessDenied = res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden
			snapshot.Status = fmt.Sprintf("Mapbox HTTP %d; basemap unavailable", res.StatusCode)
			if p.tileset == trafficTileset {
				snapshot.Status = fmt.Sprintf("Mapbox Traffic HTTP %d; check account access or turn traffic off", res.StatusCode)
				if snapshot.AccessDenied {
					snapshot.Status += "; toggle traffic to retry"
					p.mu.Lock()
					p.unavailable = snapshot.Status
					p.stats.Errors++
					p.mu.Unlock()
					snapshot.Failed, snapshot.Unavailable = true, true
					break
				}
			}
			delay := 10
			if res.StatusCode == 429 {
				delay = 60
				if v, e := strconv.Atoi(res.Header.Get("Retry-After")); e == nil {
					delay = max(delay, v)
				}
			}
			retryAt = p.fail(time.Duration(delay) * time.Second)
			continue
		}
		data, e := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
		res.Body.Close()
		if ctx.Err() != nil {
			return snapshot, ctx.Err()
		}
		if e != nil || len(data) > 8*1024*1024 {
			snapshot.Status = "Tile body failed or exceeded 8 MiB"
			retryAt = p.fail(10 * time.Second)
			continue
		}
		fs, e := decodeTile(data, tile)
		if ctx.Err() != nil {
			return snapshot, ctx.Err()
		}
		if e != nil {
			snapshot.Status = e.Error()
			retryAt = p.fail(10 * time.Second)
			continue
		}
		snapshot.Features = append(snapshot.Features, fs...)
		snapshot.AccessVerified = true
		if p.tileset == trafficTileset {
			snapshot.FetchedAt = earlier(snapshot.FetchedAt, received)
		}
		age := ttl(res.Header.Get("Cache-Control"), res.Header.Get("Age"))
		if p.tileset == trafficTileset {
			age = min(age, trafficRefreshInterval)
		}
		if age <= 0 {
			fallback := time.Minute
			if p.tileset == trafficTileset {
				fallback = trafficRefreshInterval
			}
			snapshot.RefreshAt = earlier(snapshot.RefreshAt, received.Add(fallback))
			continue
		}
		expires := received.Add(age)
		snapshot.RefreshAt = earlier(snapshot.RefreshAt, expires)
		if !p.now().Before(expires) {
			continue
		}
		p.mu.Lock()
		if len(p.cache) >= 64 {
			var oldest Tile
			used := uint64(math.MaxUint64)
			for key, v := range p.cache {
				if v.Used < used {
					used = v.Used
					oldest = key
				}
			}
			delete(p.cache, oldest)
		}
		fetchedAt := time.Time{}
		if p.tileset == trafficTileset {
			fetchedAt = received
		}
		p.cache[tile] = cacheEntry{Features: fs, Expires: expires, FetchedAt: fetchedAt, Used: p.clock}
		p.mu.Unlock()
	}
	if !retryAt.IsZero() {
		snapshot.RefreshAt = retryAt
		snapshot.Failed = true
	}
	if p.tileset == trafficTileset && snapshot.Failed {
		snapshot.Features = nil
		snapshot.FetchedAt = time.Time{}
		snapshot.AccessVerified = false
	}
	if snapshot.Unavailable {
		snapshot.RefreshAt = time.Time{}
	}
	if p.tileset == trafficTileset && snapshot.Status == "" && len(snapshot.Features) == 0 {
		snapshot.Status = "No traffic data in this 20-mile view"
	}
	if !snapshot.RefreshAt.IsZero() && snapshot.RefreshAt.Before(p.now().Add(time.Second)) {
		snapshot.RefreshAt = p.now().Add(time.Second)
	}
	snapshot.Stats = p.snapshotStats()
	return snapshot, ctx.Err()
}

func (p *Provider) snapshotStats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stats.Cached = len(p.cache)
	return p.stats
}

func (p *Provider) fail(delay time.Duration) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stats.Errors++
	p.retry = p.now().Add(delay)
	return p.retry
}
