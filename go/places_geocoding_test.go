package main

import (
	"net/http"
	"strings"
	"testing"
)

const geocodedPlaceFixture = `{"attribution":"© Mapbox","features":[{"id":"village-id","geometry":{"coordinates":[-2.54,51.31]},"properties":{"mapbox_id":"village-id","feature_type":"place","name":"Hinton Blewett","place_formatted":"Somerset, England, United Kingdom"}}]}`

func TestGeocodedPlaceUsesSelectedIDAndRetainsOrigin(t *testing.T) {
	p := newPlaceClient("secret-token")
	p.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		query := r.URL.Query()
		if r.URL.Host != "api.mapbox.com" || r.URL.Path != "/search/geocode/v6/forward" {
			t.Fatal("wrong geocoding lookup endpoint", r.URL.Path)
		}
		if query.Get("q") != "village-id" || query.Get("autocomplete") != "false" {
			t.Fatal("save did not request the exact selected feature")
		}
		if query.Has("permanent") {
			t.Fatal("save should use the default geocoding mode")
		}
		if query.Get("access_token") != p.token || query.Get("session_token") != "" {
			t.Fatal("save lost its credential or reused a Search Box session")
		}
		return locationResponse(geocodedPlaceFixture), nil
	})
	location, err := p.geocodedLocation(t.Context(), placeSuggestion{
		ID: "village-id", Label: "Search suggestion label", Type: "place",
	})
	if err != nil || location.ProviderID != "village-id" || location.Source != placeSourceMapboxTemporary {
		t.Fatal("geocoded location lost its identity/origin", location, err)
	}
	if location.Name != "Hinton Blewett, Somerset, England, United Kingdom" || location.Attribution != "© Mapbox" {
		t.Fatal("saved label or attribution did not come from the geocoding response", location)
	}
	if location.Lat != 51.31 || location.Lon != -2.54 {
		t.Fatal("geocoded coordinates changed", location)
	}
}

func TestGeocodedPlaceRejectsUnsupportedAndMismatchedResults(t *testing.T) {
	for _, test := range []struct {
		name, body, kind string
		status           int
	}{
		{name: "venue", body: geocodedPlaceFixture, kind: "poi"},
		{name: "category", body: geocodedPlaceFixture, kind: "category"},
		{name: "no result", body: `{"features":[]}`},
		{name: "different ID", body: strings.ReplaceAll(geocodedPlaceFixture, "village-id", "another-place")},
		{name: "wrong type", body: strings.ReplaceAll(geocodedPlaceFixture, `"feature_type":"place"`, `"feature_type":"poi"`)},
		{name: "invalid coordinates", body: strings.ReplaceAll(geocodedPlaceFixture, "51.31", "91")},
		{name: "missing coordinates", body: strings.ReplaceAll(geocodedPlaceFixture, "[-2.54,51.31]", "[null,null]")},
		{name: "missing credits", body: strings.ReplaceAll(geocodedPlaceFixture, "© Mapbox", "")},
		{name: "account denied", status: http.StatusForbidden},
		{name: "rate limited", status: http.StatusTooManyRequests},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := newPlaceClient("secret-token")
			var requests int
			p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
				requests++
				response := locationResponse(test.body)
				if test.status != 0 {
					response.StatusCode = test.status
				}
				return response, nil
			})
			_, err := p.geocodedLocation(t.Context(), placeSuggestion{ID: "village-id", Type: test.kind})
			if err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatal("unsupported result accepted or credential exposed", err)
			}
			if test.kind != "" && requests != 0 {
				t.Fatal("unsupported venue made a billable lookup")
			}
		})
	}
}
