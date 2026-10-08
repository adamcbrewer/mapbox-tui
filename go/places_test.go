package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func locationResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

func TestPlaceSearchAndRetrievalShareSession(t *testing.T) {
	p := newPlaceClient("test-secret")
	var requests int
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Scheme != "https" || r.URL.Host != "api.mapbox.com" || r.Header.Get("User-Agent") == "" {
			t.Fatal("location request omitted HTTPS, the Mapbox host or identification")
		}
		q := r.URL.Query()
		if q.Get("session_token") != "session" || q.Get("access_token") != p.token {
			t.Fatal("Mapbox search and retrieve did not share session/credentials")
		}
		if strings.HasSuffix(r.URL.Path, "/suggest") {
			if q.Get("q") != "station & cafe" || q.Get("proximity") != "-0.100000,51.500000" {
				t.Fatal("search query/proximity was not encoded correctly")
			}
			return locationResponse(`{"suggestions":[{"mapbox_id":"place-id","name":"Station","place_formatted":"London, UK"}]}`), nil
		}
		if !strings.HasSuffix(r.URL.Path, "/retrieve/place-id") {
			t.Fatal("unexpected retrieval endpoint")
		}
		return locationResponse(`{"attribution":"Mapbox credit","features":[{"geometry":{"coordinates":[-0.2,51.6]}}]}`), nil
	})
	suggestions, err := p.suggest(t.Context(), "station & cafe", "session", XY{-.1, 51.5})
	if err != nil || len(suggestions) != 1 || suggestions[0].Label != "Station, London, UK" {
		t.Fatalf("search: %v, %v", suggestions, err)
	}
	location, err := p.retrieve(t.Context(), suggestions[0], "session")
	if err != nil || location.Lat != 51.6 || location.Lon != -.2 || location.Attribution != "Mapbox credit" {
		t.Fatalf("retrieve: %+v, %v", location, err)
	}
	if requests != 2 {
		t.Fatalf("search/retrieval made %d requests, want 2", requests)
	}
}

func TestPlaceClientRejectsBadResponsesAndRedactsTransportErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{name: "rate limit", status: 429},
		{name: "bad token", status: 401, body: "secret response"},
		{name: "malformed", status: 200, body: "secret response"},
		{name: "oversized", status: 200, body: strings.Repeat("x", (1<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPlaceClient("test-secret")
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				response := locationResponse(tc.body)
				response.StatusCode = tc.status
				return response, nil
			})
			_, err := p.suggest(t.Context(), "private query", "session", XY{})
			if err == nil || strings.Contains(err.Error(), "secret response") || strings.Contains(err.Error(), p.token) {
				t.Fatalf("invalid/unredacted error: %v", err)
			}
		})
	}
	p := newPlaceClient("test-secret")
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failed %s", r.URL)
	})
	_, err := p.suggest(t.Context(), "private query", "session", XY{})
	if err == nil || strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "private") {
		t.Fatalf("transport error leaked credentials/query: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = p.suggest(ctx, "query", "session", XY{})
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestPlaceClientRejectsMissingOrInvalidCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{name: "no features", body: `{"features":[]}`},
		{name: "missing coordinate", body: `{"features":[{"geometry":{"coordinates":[0]}}]}`},
		{name: "out of bounds", body: `{"features":[{"geometry":{"coordinates":[0,91]}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPlaceClient("token")
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				return locationResponse(tc.body), nil
			})
			if _, err := p.retrieve(t.Context(), placeSuggestion{ID: "id"}, "session"); err == nil {
				t.Fatal("accepted invalid retrieved coordinates")
			}
		})
	}
}
