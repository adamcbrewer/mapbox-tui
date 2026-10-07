package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
)

type Tile struct{ Z, X, Y int }
type cacheEntry struct {
	Features []Feature
	Expires  time.Time
	Used     uint64
}
type Stats struct {
	Requests, Hits, Errors uint64
	Cached                 int
}
type Provider struct {
	Kind   string
	client *http.Client
	token  string
	cache  map[Tile]cacheEntry
	stats  Stats
	clock  uint64
	retry  time.Time
	mu     sync.Mutex
}

const mapboxTokenMissing = "Mapbox token missing; set MAPBOX_ACCESS_TOKEN or access_token in mapbox.local.json"

func defaultProvider(token string) string {
	if token != "" {
		return "mapbox"
	}
	return "none"
}

func newProvider(kind string) *Provider {
	return &Provider{Kind: kind, client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, token: accessToken(), cache: map[Tile]cacheEntry{}}
}
func accessToken() string {
	if token, ok := os.LookupEnv("MAPBOX_ACCESS_TOKEN"); ok {
		return token
	}
	data, err := os.ReadFile("mapbox.local.json")
	if err != nil {
		return ""
	}
	var config struct {
		Token string `json:"access_token"`
	}
	if json.Unmarshal(data, &config) != nil {
		return ""
	}
	return config.Token
}
func attribution(kind string) string {
	if kind == "mapbox" {
		return "© Mapbox  © OpenStreetMap  Improve this map (v credits)"
	}
	return "No basemap"
}
func visible(c Camera) []Tile {
	z := int(math.Max(0, math.Min(14, math.Floor(c.Zoom))))
	a, b := XY{math.Inf(1), math.Inf(1)}, XY{math.Inf(-1), math.Inf(-1)}
	for _, s := range []XY{{0, 0}, {float64(c.Width * 2), 0}, {0, float64(c.Height * 4)}, {float64(c.Width * 2), float64(c.Height * 4)}} {
		p := c.world(s)
		a.X = math.Min(a.X, p.X)
		a.Y = math.Min(a.Y, p.Y)
		b.X = math.Max(b.X, p.X)
		b.Y = math.Max(b.Y, p.Y)
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
func ttl(header string) time.Duration {
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
			layer := 0
			label := ""
			switch l.Name {
			case "water":
				layer = 1
			case "road":
				layer = 2
				if cl, _ := f.Properties["class"].(string); cl == "motorway" || cl == "trunk" || cl == "primary" || cl == "secondary" {
					layer = 3
				}
			case "place_label":
				layer = 4
				label, _ = f.Properties["name_en"].(string)
				if label == "" {
					label, _ = f.Properties["name"].(string)
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
			add := func(parts [][]XY, polygon bool) { out = append(out, Feature{parts, layer, polygon, clean(label)}) }
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
func (p *Provider) scene(ctx context.Context, t *Track, c Camera) ([]Feature, Stats, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Kind != "mapbox" {
		if t == nil && p.token == "" {
			return nil, p.stats, mapboxTokenMissing
		}
		return nil, p.stats, ""
	}
	if p.token == "" {
		return nil, p.stats, mapboxTokenMissing
	}
	out := []Feature{}
	status := ""
	for _, tile := range visible(c) {
		p.clock++
		entry, ok := p.cache[tile]
		if ok && time.Now().Before(entry.Expires) {
			p.stats.Hits++
			entry.Used = p.clock
			p.cache[tile] = entry
			out = append(out, entry.Features...)
			continue
		}
		if time.Now().Before(p.retry) {
			if status == "" {
				status = "Tile requests paused after provider failure"
			}
			continue
		}
		if ctx.Err() != nil {
			break
		}
		u := fmt.Sprintf("https://api.mapbox.com/v4/mapbox.mapbox-streets-v8/%d/%d/%d.mvt?access_token=%s", tile.Z, tile.X, tile.Y, url.QueryEscape(p.token))
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			status = "Cannot create tile request"
			continue
		}
		p.stats.Requests++
		res, err := p.client.Do(req)
		if err != nil {
			p.stats.Errors++
			status = "Tile network request failed"
			p.retry = time.Now().Add(10 * time.Second)
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			p.stats.Errors++
			status = fmt.Sprintf("Mapbox HTTP %d; basemap unavailable", res.StatusCode)
			delay := 10
			if res.StatusCode == 429 {
				delay = 60
				if v, e := strconv.Atoi(res.Header.Get("Retry-After")); e == nil {
					delay = max(delay, v)
				}
			}
			p.retry = time.Now().Add(time.Duration(delay) * time.Second)
			continue
		}
		data, e := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
		res.Body.Close()
		if e != nil || len(data) > 8*1024*1024 {
			p.stats.Errors++
			status = "Tile body failed or exceeded 8 MiB"
			continue
		}
		fs, e := decodeTile(data, tile)
		if e != nil {
			p.stats.Errors++
			status = e.Error()
			continue
		}
		out = append(out, fs...)
		age := ttl(res.Header.Get("Cache-Control"))
		if elapsed, e := strconv.Atoi(res.Header.Get("Age")); e == nil && elapsed > 0 {
			age -= time.Duration(elapsed) * time.Second
		}
		if age > 0 {
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
			p.cache[tile] = cacheEntry{fs, time.Now().Add(age), p.clock}
		}
	}
	p.stats.Cached = len(p.cache)
	return out, p.stats, status
}
