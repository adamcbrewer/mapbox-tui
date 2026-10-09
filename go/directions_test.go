package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func directionsTestLocations() []placeLocation {
	return []placeLocation{
		{Name: "Start", Lon: -.1, Lat: 51.5},
		{Name: "Café", Lon: -.15, Lat: 51.55},
		{Name: "Destination", Lon: -.2, Lat: 51.6},
	}
}

func directionsTestResponse(locations []placeLocation, distance, duration float64) string {
	var coordinates [][2]float64
	var waypoints []map[string]any
	for _, location := range locations {
		coordinate := [2]float64{location.Lon + .0001, location.Lat + .0001}
		coordinates = append(coordinates, coordinate)
		waypoints = append(waypoints, map[string]any{"location": coordinate})
	}
	data, err := json.Marshal(map[string]any{
		"code": "Ok", "waypoints": waypoints,
		"routes": []map[string]any{{
			"distance": distance, "duration": duration,
			"geometry": map[string]any{"type": "LineString", "coordinates": coordinates},
		}},
	})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func directionsRequestLocations(t *testing.T, r *http.Request) []placeLocation {
	t.Helper()
	parts := strings.Split(r.URL.Path, "/")
	var locations []placeLocation
	for _, pair := range strings.Split(parts[len(parts)-1], ";") {
		coords := strings.Split(pair, ",")
		lon, lonErr := strconv.ParseFloat(coords[0], 64)
		lat, latErr := strconv.ParseFloat(coords[1], 64)
		if lonErr != nil || latErr != nil {
			t.Fatal("invalid request coordinates")
		}
		locations = append(locations, placeLocation{Lon: lon, Lat: lat})
	}
	return locations
}

func TestDirectionsOrderedStopsProfilesAndSeparateEstimates(t *testing.T) {
	for _, mode := range []journeyMode{journeyWalking, journeyCycling, journeyDriving} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			p := newDirectionsClient("test-secret")
			locations := directionsTestLocations()
			before := append([]placeLocation(nil), locations...)
			var requests int
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Scheme != "https" || r.URL.Host != "api.mapbox.com" || r.Header.Get("User-Agent") == "" {
					t.Fatal("Directions request omitted fixed HTTPS host or identification")
				}
				path := "/directions/v5/mapbox/" + string(mode) + "/-0.1,51.5;-0.15,51.55;-0.2,51.6"
				if r.URL.Path != path {
					t.Fatalf("stops/profile changed: %s", r.URL.Path)
				}
				q := r.URL.Query()
				if q.Get("access_token") != p.token || q.Get("geometries") != "geojson" || q.Get("overview") != "full" {
					t.Fatal("wrong credential or route geometry options")
				}
				if q.Get("steps") != "false" || q.Get("alternatives") != "false" {
					t.Fatal("unrequested instructions/alternatives enabled")
				}
				return locationResponse(directionsTestResponse(locations, 12345, 3661)), nil
			})
			route, err := p.resolve(t.Context(), mode, locations)
			if err != nil {
				t.Fatal(err)
			}
			if requests != 1 || route.Distance != 12345 || route.Duration != 3661 || route.Mode != mode {
				t.Fatalf("wrong estimates/profile or multiple requests: %+v, %d", route, requests)
			}
			if !reflect.DeepEqual(locations, before) || route.Snapped[0].X == locations[0].Lon {
				t.Fatal("snapping overwrote stop intent")
			}
			if len(route.Track.Waypoints) != 1 || route.Track.Waypoints[0].Name != "Café" ||
				route.Track.Waypoints[0].Lon != locations[1].Lon {
				t.Fatal("original intermediate waypoint lost")
			}
			for _, point := range route.Track.Points {
				if point.Time != nil || point.Elevation != nil {
					t.Fatal("routing estimates became recorded measurements")
				}
			}
		})
	}
}

func TestDirectionsRejectsLimitsBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      journeyMode
		locations []placeLocation
		want      string
	}{
		{name: "manual", want: "choose Walking"},
		{name: "one stop", mode: journeyWalking, locations: directionsTestLocations()[:1], want: "2–25"},
		{name: "26 stops", mode: journeyDriving, locations: make([]placeLocation, 26), want: "26"},
		{name: "walking distance", mode: journeyWalking, locations: []placeLocation{{}, {Lon: 10}}, want: "1000 km"},
		{name: "cycling distance", mode: journeyCycling, locations: []placeLocation{{}, {Lon: 14}}, want: "1500 km"},
		{name: "driving distance", mode: journeyDriving, locations: []placeLocation{{}, {Lon: 91}}, want: "10000 km"},
		{name: "total through stops", mode: journeyWalking, locations: []placeLocation{{}, {Lon: 5}, {}}, want: "1000 km"},
		{name: "invalid coordinate", mode: journeyWalking, locations: []placeLocation{{Lon: math.NaN()}, {}}, want: "invalid coordinates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newDirectionsClient("token")
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				t.Error("invalid/over-limit plan made a network request")
				return nil, errors.New("unexpected request")
			})
			_, err := p.resolve(t.Context(), tc.mode, tc.locations)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wrong limit feedback: %v", err)
			}
		})
	}
}

func TestDirectionsFailuresAndRedaction(t *testing.T) {
	valid := directionsTestResponse(directionsTestLocations(), 12345, 1200)
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{name: "no route", body: `{"code":"NoRoute","message":"test-secret"}`, want: "no Walking route"},
		{name: "no segment", body: `{"code":"NoSegment"}`, want: "match a stop"},
		{name: "invalid input", body: `{"code":"InvalidInput","message":"test-secret"}`, want: "profile limits"},
		{name: "token rejected", status: 401, want: "rejected the token"},
		{name: "account denied", status: 403, want: "account limits/token permissions"},
		{name: "provider limit", status: 422, want: "25 stops, 1000 km"},
		{name: "unavailable", status: 503, want: "HTTP 503"},
		{name: "malformed JSON", body: `{test-secret`, want: "invalid Directions response"},
		{name: "oversize", body: strings.Repeat("x", (8<<20)+1), want: "8 MiB"},
		{name: "null distance", body: strings.Replace(valid, `"distance":12345`, `"distance":null`, 1), want: "invalid route"},
		{name: "negative duration", body: strings.Replace(valid, `"duration":1200`, `"duration":-1`, 1), want: "invalid route"},
		{name: "null coordinate", body: strings.Replace(valid, "-0.0999", "null", 1), want: "invalid route"},
		{name: "wrong geometry", body: strings.Replace(valid, "LineString", "Polygon", 1), want: "invalid route"},
		{name: "no route object", body: `{"code":"Ok","routes":[]}`, want: "invalid route"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newDirectionsClient("test-secret")
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				response := locationResponse(tc.body)
				if tc.status != 0 {
					response.StatusCode = tc.status
				}
				return response, nil
			})
			_, err := p.resolve(t.Context(), journeyWalking, directionsTestLocations())
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("wrong or unsafe error: %v", err)
			}
		})
	}
	p := newDirectionsClient("test-secret")
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failed %s", r.URL)
	})
	_, err := p.resolve(t.Context(), journeyWalking, directionsTestLocations())
	if err == nil || strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "51.5") {
		t.Fatalf("transport error exposed token/locations: %v", err)
	}
	_, err = newDirectionsClient("").resolve(t.Context(), journeyWalking, directionsTestLocations())
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatal("missing credentials did not offer manual fallback", err)
	}
}

func TestDirectionsRateLimitBackoffAndExplicitRetry(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		wait         time.Duration
	}{
		{name: "minimum", header: "5", wait: time.Minute},
		{name: "seconds", header: "120", wait: 2 * time.Minute},
		{name: "date", header: "Thu, 01 Jan 2026 00:03:00 GMT", wait: 3 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newDirectionsClient("token")
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			p.now = func() time.Time { return now }
			var requests int
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if requests > 1 {
					return locationResponse(directionsTestResponse(directionsTestLocations(), 100, 100)), nil
				}
				return &http.Response{
					StatusCode: 429, Header: http.Header{"Retry-After": {tc.header}}, Body: io.NopCloser(strings.NewReader("")),
				}, nil
			})
			_, err := p.resolve(t.Context(), journeyWalking, directionsTestLocations())
			if err == nil || !strings.Contains(err.Error(), "429") {
				t.Fatal("rate limiting not explained", err)
			}
			now = now.Add(tc.wait - time.Second)
			_, err = p.resolve(t.Context(), journeyDriving, directionsTestLocations())
			if err == nil || requests != 1 {
				t.Fatal("backoff did not cover all modes", err, requests)
			}
			now = now.Add(time.Second)
			_, err = p.resolve(t.Context(), journeyWalking, directionsTestLocations())
			if err != nil || requests != 2 {
				t.Fatal("explicit retry failed after backoff", err, requests)
			}
		})
	}
}

func TestDirectionsCancellationAndRedirects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newDirectionsClient("token")
		ctx, cancel := context.WithCancel(t.Context())
		p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return locationResponse(`{"code":"NoRoute"}`), nil
		})
		var err error
		go func() { _, err = p.resolve(ctx, journeyWalking, directionsTestLocations()) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("transport completion ignored cancellation", err)
		}
	})
	p := newDirectionsClient("token")
	var requests int
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: 302, Header: http.Header{"Location": {"https://other.example/"}},
			Body: io.NopCloser(strings.NewReader("")),
		}, nil
	})
	_, err := p.resolve(t.Context(), journeyWalking, directionsTestLocations())
	if err == nil || requests != 1 || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatal("Directions followed a redirect", err, requests)
	}
}

func TestDirectionsTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newDirectionsClient("secret")
		p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		start := time.Now()
		_, err := p.resolve(t.Context(), journeyWalking, directionsTestLocations())
		if err == nil || strings.Contains(err.Error(), "secret") || time.Since(start) != 8*time.Second {
			t.Fatal("Directions request timeout is missing or unsafe", err, time.Since(start))
		}
	})
}
