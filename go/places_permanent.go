package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const permanentGeocodingCredit = "© Mapbox"

// Resolve the exact feature by ID. Never replace an unmatched selection with a
// similarly named place, or persist the temporary Search Box response itself.
func (p *placeClient) permanentLocation(ctx context.Context, suggestion placeSuggestion) (placeLocation, error) {
	if p.token == "" {
		return placeLocation{}, fmt.Errorf("%s", mapboxTokenMissing)
	}
	if suggestion.Type == "poi" || suggestion.Type == "category" {
		return placeLocation{}, fmt.Errorf("Mapbox cannot save named venues; choose an address or town")
	}
	if suggestion.ID == "" {
		return placeLocation{}, fmt.Errorf("this search result has no location ID")
	}
	query := url.Values{
		"q": {suggestion.ID}, "access_token": {p.token}, "permanent": {"true"},
		"autocomplete": {"false"}, "limit": {"1"},
	}
	var response struct {
		Attribution string `json:"attribution"`
		Features    []struct {
			ID       string `json:"id"`
			Geometry struct {
				Coordinates []*float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties struct {
				ID        string `json:"mapbox_id"`
				Type      string `json:"feature_type"`
				Name      string `json:"name"`
				Preferred string `json:"name_preferred"`
				Full      string `json:"full_address"`
				Context   string `json:"place_formatted"`
			} `json:"properties"`
		} `json:"features"`
	}
	endpoint := "https://api.mapbox.com/search/geocode/v6/forward?" + query.Encode()
	if err := p.getJSON(ctx, endpoint, &response); err != nil {
		if status, ok := err.(placeHTTPError); ok && status == http.StatusForbidden {
			return placeLocation{}, fmt.Errorf("saving needs Mapbox Permanent Geocoding access (HTTP 403)")
		}
		return placeLocation{}, fmt.Errorf("save failed: %w", err)
	}
	if len(response.Features) != 1 {
		return placeLocation{}, fmt.Errorf("Mapbox could not provide a storable version of this result")
	}
	feature := response.Features[0]
	id := feature.Properties.ID
	if id == "" {
		id = feature.ID
	}
	if id != suggestion.ID {
		return placeLocation{}, fmt.Errorf("Mapbox returned a different place; nothing saved")
	}
	if feature.Properties.Type == "poi" || feature.Properties.Type == "category" {
		return placeLocation{}, fmt.Errorf("Mapbox cannot save named venues; choose an address or town")
	}
	coords := feature.Geometry.Coordinates
	complete := len(coords) == 2 && coords[0] != nil && coords[1] != nil
	if !complete || !validLocation(*coords[0], *coords[1]) {
		return placeLocation{}, fmt.Errorf("Mapbox returned invalid coordinates; nothing saved")
	}
	name := feature.Properties.Full
	if name == "" {
		name = feature.Properties.Preferred
		if name == "" {
			name = feature.Properties.Name
		}
		if context := feature.Properties.Context; context != "" && context != name {
			name = strings.Trim(name+", "+context, ", ")
		}
	}
	name = strings.TrimSpace(cleanPlaceText(name))
	credit := strings.TrimSpace(cleanPlaceText(response.Attribution))
	if name == "" || credit == "" {
		return placeLocation{}, fmt.Errorf("Mapbox returned incomplete place details; nothing saved")
	}
	return placeLocation{
		Name: name, Lon: *coords[0], Lat: *coords[1],
		Source: placeSourceMapboxPermanent, ProviderID: id, Attribution: credit,
	}, nil
}
