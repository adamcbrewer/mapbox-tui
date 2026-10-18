package main

import (
	"context"
	"errors"
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
	if err != nil || location.Name != suggestions[0].Label || location.Lat != 51.6 ||
		location.Lon != -.2 || location.Attribution != "Mapbox credit" || location.ProviderID != "place-id" {
		t.Fatalf("retrieve: %+v, %v", location, err)
	}
	if requests != 2 {
		t.Fatalf("search/retrieval made %d requests, want 2", requests)
	}
}

func TestPlaceClientPreservesSuggestionNames(t *testing.T) {
	for _, tc := range []struct {
		name, body, label string
	}{
		{
			name:  "named POI with address",
			body:  `{"suggestions":[{"mapbox_id":"id","name":"Michigan Stadium","full_address":"1300 South Main Street, Ann Arbor, MI","place_formatted":"Ann Arbor, MI"}]}`,
			label: "Michigan Stadium, 1300 South Main Street, Ann Arbor, MI",
		},
		{
			name:  "address already includes name",
			body:  `{"suggestions":[{"mapbox_id":"id","name":"1300 South Main Street","full_address":"1300 South Main Street, Ann Arbor, MI"}]}`,
			label: "1300 South Main Street, Ann Arbor, MI",
		},
		{
			name:  "address equals name",
			body:  `{"suggestions":[{"mapbox_id":"id","name":"1300 South Main Street","full_address":"1300 South Main Street"}]}`,
			label: "1300 South Main Street",
		},
		{
			name:  "distinct context",
			body:  `{"suggestions":[{"mapbox_id":"id","name":"Station","place_formatted":"London, UK"}]}`,
			label: "Station, London, UK",
		},
		{
			name:  "context equals name",
			body:  `{"suggestions":[{"mapbox_id":"id","name":"London","place_formatted":"London"}]}`,
			label: "London",
		},
		{
			name:  "name without address or context",
			body:  `{"suggestions":[{"mapbox_id":"id","name":"Station"}]}`,
			label: "Station",
		},
		{
			name:  "address starts with a distinct name",
			body:  `{"suggestions":[{"mapbox_id":"id","name":"Station","full_address":"Station Road, London, UK"}]}`,
			label: "Station, Station Road, London, UK",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPlaceClient("token")
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				return locationResponse(tc.body), nil
			})
			suggestions, err := p.suggest(
				t.Context(),
				"query",
				"session",
				XY{},
			)
			if err != nil || len(suggestions) != 1 || suggestions[0].Label != tc.label {
				t.Fatalf("search: %v, %v; want label %q", suggestions, err, tc.label)
			}
		})
	}
}

func TestPlaceClientPreservesPrintableUnicode(t *testing.T) {
	p := newPlaceClient("token")
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/suggest") {
			return locationResponse(`{"attribution":"© Mapbox suggestions\n","suggestions":[{"mapbox_id":"id","name":"Café Zürich\u001b","place_formatted":"日本\n"}]}`), nil
		}
		return locationResponse(`{"attribution":"© Mapbox\r\n© OpenStreetMap\u009b","features":[{"geometry":{"coordinates":[8.54,47.37]}}]}`), nil
	})
	suggestions, err := p.suggest(
		t.Context(),
		"Zürich",
		"session",
		XY{},
	)
	if err != nil || len(suggestions) != 1 || suggestions[0].Label != "Café Zürich , 日本 " {
		t.Fatalf("Unicode suggestion was changed or controls retained: %v, %v", suggestions, err)
	}
	if suggestions[0].Attribution != "© Mapbox suggestions " {
		t.Fatalf("suggestion attribution was discarded or not sanitized: %q", suggestions[0].Attribution)
	}
	location, err := p.retrieve(t.Context(), suggestions[0], "session")
	if err != nil || location.Name != suggestions[0].Label || location.Attribution != "© Mapbox  © OpenStreetMap " {
		t.Fatalf("Unicode location/attribution was changed or controls retained: %+v, %v", location, err)
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
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestPlaceClientLimitsValidResponseBodies(t *testing.T) {
	const limit = 1 << 20
	const prefix = `{"suggestions":[],"padding":"`
	const suffix = `"}`
	for _, tc := range []struct {
		name string
		size int
	}{
		{name: "within limit", size: limit},
		{name: "oversized", size: limit + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := prefix + strings.Repeat("x", tc.size-len(prefix)-len(suffix)) + suffix
			p := newPlaceClient("test-secret")
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				return locationResponse(body), nil
			})
			suggestions, err := p.suggest(
				t.Context(),
				"private query",
				"session",
				XY{},
			)
			if tc.size > limit {
				if err == nil {
					t.Fatal("accepted an oversized response")
				}
				return
			}
			if err != nil || len(suggestions) != 0 {
				t.Fatalf("within-limit response: %v, %v", suggestions, err)
			}
		})
	}
}

type placeResponseReader func([]byte) (int, error)

func (read placeResponseReader) Read(p []byte) (int, error) { return read(p) }

func TestPlaceClientPreservesBodyCancellationAndRedactsReadErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		canceled bool
	}{
		{name: "read failure"},
		{name: "cancellation during read", canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p := newPlaceClient("test-secret")
			p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				response := locationResponse("")
				response.Body = io.NopCloser(placeResponseReader(func([]byte) (int, error) {
					if tc.canceled {
						cancel()
					}
					return 0, fmt.Errorf("failed %s: secret response", r.URL)
				}))
				return response, nil
			})
			_, err := p.suggest(
				ctx,
				"private query",
				"session",
				XY{},
			)
			if err == nil {
				t.Fatal("accepted a failed response-body read")
			}
			if errors.Is(err, context.Canceled) != tc.canceled {
				t.Fatalf("cancellation identity lost: %v", err)
			}
			for _, private := range []string{"test-secret", "private", "secret response", "https://"} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("response-body error leaked %q: %v", private, err)
				}
			}
		})
	}
}

func TestPlaceClientRejectsMissingOrInvalidCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{name: "no features", body: `{"features":[]}`},
		{name: "missing coordinate", body: `{"features":[{"geometry":{"coordinates":[0]}}]}`},
		{name: "null longitude", body: `{"features":[{"geometry":{"coordinates":[null,51.6]}}]}`},
		{name: "null latitude", body: `{"features":[{"geometry":{"coordinates":[-0.2,null]}}]}`},
		{name: "null coordinates", body: `{"features":[{"geometry":{"coordinates":[null,null]}}]}`},
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

func TestPlaceClientAcceptsZeroCoordinates(t *testing.T) {
	p := newPlaceClient("token")
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return locationResponse(`{"attribution":"Mapbox credit","features":[{"geometry":{"coordinates":[0,0]}}]}`), nil
	})
	location, err := p.retrieve(t.Context(), placeSuggestion{ID: "id", Label: "Origin"}, "session")
	if err != nil || location.Lon != 0 || location.Lat != 0 || location.Name != "Origin" ||
		location.Source != placeSourceMapboxSearch || location.Attribution != "Mapbox credit" {
		t.Fatalf("zero coordinates: %+v, %v", location, err)
	}
}
