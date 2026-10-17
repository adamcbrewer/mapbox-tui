package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const directionsCredits = "© Mapbox\n© OpenStreetMap"

type directionsClient struct {
	client  *http.Client
	token   string
	mu      sync.Mutex
	retryAt time.Time
	now     func() time.Time
}

func newDirectionsClient(token string) *directionsClient {
	return &directionsClient{
		token: token, now: time.Now,
		client: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type directionsWaypoint struct {
	Location []*float64 `json:"location"`
}

type directionsResponse struct {
	Code      string               `json:"code"`
	Waypoints []directionsWaypoint `json:"waypoints"`
	Routes    []struct {
		Distance *float64 `json:"distance"`
		Duration *float64 `json:"duration"`
		Geometry struct {
			Type        string       `json:"type"`
			Coordinates [][]*float64 `json:"coordinates"`
		} `json:"geometry"`
	} `json:"routes"`
}

func (p *directionsClient) resolve(
	ctx context.Context,
	mode journeyMode,
	locations []placeLocation,
) (*journeyRoute, error) {
	stops := make([]journeyStop, 0, len(locations))
	for i := range locations {
		stops = append(stops, journeyStop{Location: &locations[i]})
	}
	if _, err := routingLocations(stops, mode); err != nil {
		return nil, err
	}
	if p.token == "" {
		return nil, fmt.Errorf("Mapbox Directions not connected; use Mapbox settings or choose Offline")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	retryAt := p.retryAt
	p.mu.Unlock()
	if remaining := retryAt.Sub(p.now()); remaining > 0 {
		return nil, fmt.Errorf(
			"Mapbox Directions rate limited; retry in %.0f seconds or choose Offline",
			math.Ceil(remaining.Seconds()),
		)
	}
	coords := make([]string, 0, len(locations))
	for _, location := range locations {
		lon := strconv.FormatFloat(location.Lon, 'f', -1, 64)
		lat := strconv.FormatFloat(location.Lat, 'f', -1, 64)
		coords = append(coords, lon+","+lat)
	}
	values := url.Values{
		"access_token": {p.token}, "geometries": {"geojson"}, "overview": {"full"},
		"steps": {"false"}, "alternatives": {"false"},
	}
	endpoint := "https://api.mapbox.com/directions/v5/mapbox/" + string(mode) + "/" + strings.Join(coords, ";")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("cannot create Directions request")
	}
	req.Header.Set("User-Agent", "mapbox-tui/1 (https://github.com/adamcbrewer/mapbox-tui)")
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Transport errors contain the request URL, including the token and stop coordinates.
		return nil, fmt.Errorf("Mapbox Directions request failed; check connection, retry or choose Offline")
	}
	defer resp.Body.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, p.rateLimited(resp.Header.Get("Retry-After"))
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("Mapbox Directions rejected the token (HTTP 401); reconnect or choose Offline")
	case http.StatusForbidden:
		return nil, fmt.Errorf("Mapbox Directions access denied (HTTP 403); " +
			"check account limits/token permissions or choose Offline")
	case http.StatusUnprocessableEntity:
		return nil, fmt.Errorf(
			"Mapbox rejected this %s plan (HTTP 422); check locations and limits: 25 stops, %.0f km; choose Offline if needed",
			mode.label(), mode.limitKM(),
		)
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("Mapbox Directions returned HTTP %d; retry or choose Offline", resp.StatusCode)
	}
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("cannot read Directions response; retry or choose Offline")
	}
	if len(data) > limit {
		return nil, fmt.Errorf("Directions response exceeds 8 MiB; shorten the plan or choose Offline")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var response directionsResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("invalid Directions response; retry or choose Offline")
	}
	switch response.Code {
	case "NoRoute":
		return nil, fmt.Errorf(
			"Mapbox found no %s route connecting these stops; check access/water crossings or choose Offline",
			mode.label(),
		)
	case "NoSegment":
		return nil, fmt.Errorf("Mapbox could not match a stop to a road/path; move that location or choose Offline")
	case "Ok":
	default:
		return nil, fmt.Errorf("Mapbox could not calculate this plan; check locations/profile limits or choose Offline")
	}
	return response.route(mode, locations)
}

func (p *directionsClient) rateLimited(header string) error {
	now := p.now()
	retryAt := now.Add(time.Minute)
	if seconds, err := strconv.ParseInt(header, 10, 32); err == nil && seconds > 60 {
		retryAt = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(header); err == nil && date.After(retryAt) {
		retryAt = date
	}
	p.mu.Lock()
	if retryAt.After(p.retryAt) {
		p.retryAt = retryAt
	}
	retryAt = p.retryAt
	p.mu.Unlock()
	return fmt.Errorf(
		"Mapbox Directions rate limited (HTTP 429); retry in %.0f seconds or choose Offline",
		retryAt.Sub(now).Seconds(),
	)
}

func (response directionsResponse) route(mode journeyMode, locations []placeLocation) (*journeyRoute, error) {
	invalid := fmt.Errorf("Mapbox returned invalid route geometry/estimates; retry or choose Offline")
	if len(response.Routes) == 0 || len(response.Waypoints) != len(locations) {
		return nil, invalid
	}
	route := response.Routes[0]
	if !validRouteEstimate(route.Distance) || !validRouteEstimate(route.Duration) {
		return nil, invalid
	}
	if route.Geometry.Type != "LineString" || len(route.Geometry.Coordinates) < 2 {
		return nil, invalid
	}
	track := &Track{Name: "Planned journey", RouteCount: 1}
	segment := -1
	for _, coords := range route.Geometry.Coordinates {
		location, ok := directionsCoordinate(coords)
		if !ok {
			return nil, invalid
		}
		track.appendPoint(Point{Lon: location.X, Lat: location.Y}, &segment, "route")
	}
	snapped := make([]XY, 0, len(locations))
	for i, waypoint := range response.Waypoints {
		location, ok := directionsCoordinate(waypoint.Location)
		if !ok {
			return nil, invalid
		}
		snapped = append(snapped, location)
		if i > 0 && i < len(locations)-1 {
			stop := locations[i]
			track.Waypoints = append(track.Waypoints, Point{Lon: stop.Lon, Lat: stop.Lat, Name: stop.Name})
		}
	}
	if err := prepareTrackWithNameCleaner(track, cleanPlaceText); err != nil {
		return nil, fmt.Errorf("Mapbox route cannot be displayed: %w; choose Offline", err)
	}
	for _, location := range locations {
		extendBounds(&track.Bounds, [4]float64{location.Lon, location.Lat, location.Lon, location.Lat})
	}
	return &journeyRoute{
		Track: track, Snapped: snapped, Distance: *route.Distance, Duration: *route.Duration, Mode: mode,
	}, nil
}

func directionsCoordinate(coords []*float64) (XY, bool) {
	present := len(coords) == 2 && coords[0] != nil && coords[1] != nil
	if !present || !validLocation(*coords[0], *coords[1]) {
		return XY{}, false
	}
	return XY{*coords[0], *coords[1]}, true
}

func validRouteEstimate(value *float64) bool {
	if value == nil {
		return false
	}
	finite := !math.IsNaN(*value) && !math.IsInf(*value, 0)
	return finite && *value >= 0
}
