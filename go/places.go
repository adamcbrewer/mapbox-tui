package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type placeSuggestion struct {
	ID, Label, Attribution string
}

type placeClient struct {
	client *http.Client
	token  string
}

func newPlaceClient(token string) *placeClient {
	return &placeClient{
		token: token,
		client: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (p *placeClient) suggest(ctx context.Context, query, session string, near XY) ([]placeSuggestion, error) {
	if p.token == "" {
		return nil, fmt.Errorf("%s", mapboxTokenMissing)
	}
	values := url.Values{
		"q": {query}, "session_token": {session}, "access_token": {p.token}, "limit": {"5"},
		"proximity": {fmt.Sprintf("%.6f,%.6f", near.X, near.Y)},
	}
	var response struct {
		Attribution string `json:"attribution"`
		Suggestions []struct {
			ID        string `json:"mapbox_id"`
			Name      string `json:"name"`
			Address   string `json:"full_address"`
			Formatted string `json:"place_formatted"`
		} `json:"suggestions"`
	}
	if err := p.getJSON(ctx, "https://api.mapbox.com/search/searchbox/v1/suggest?"+values.Encode(), &response); err != nil {
		return nil, fmt.Errorf("Mapbox search: %w", err)
	}
	results := make([]placeSuggestion, 0, min(5, len(response.Suggestions)))
	attribution := cleanPlaceText(response.Attribution)
	for _, suggestion := range response.Suggestions[:min(5, len(response.Suggestions))] {
		if suggestion.ID == "" || len(suggestion.ID) > 1024 {
			continue
		}
		label := suggestion.Address
		if label == "" {
			label = suggestion.Formatted
		}
		hasName := label == suggestion.Name || strings.HasPrefix(label, suggestion.Name+", ")
		if suggestion.Name != "" && !hasName {
			label = strings.Trim(suggestion.Name+", "+label, ", ")
		}
		results = append(results, placeSuggestion{
			ID: suggestion.ID, Label: cleanPlaceText(label), Attribution: attribution,
		})
	}
	return results, nil
}

func (p *placeClient) retrieve(ctx context.Context, suggestion placeSuggestion, session string) (placeLocation, error) {
	if p.token == "" {
		return placeLocation{}, fmt.Errorf("%s", mapboxTokenMissing)
	}
	values := url.Values{"session_token": {session}, "access_token": {p.token}}
	var response struct {
		Attribution string `json:"attribution"`
		Features    []struct {
			Geometry struct {
				Coordinates []*float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	endpoint := "https://api.mapbox.com/search/searchbox/v1/retrieve/" + url.PathEscape(suggestion.ID)
	if err := p.getJSON(ctx, endpoint+"?"+values.Encode(), &response); err != nil {
		return placeLocation{}, fmt.Errorf("Mapbox location: %w", err)
	}
	if len(response.Features) == 0 {
		return placeLocation{}, fmt.Errorf("Mapbox returned no location")
	}
	coords := response.Features[0].Geometry.Coordinates
	hasCoordinates := len(coords) == 2 && coords[0] != nil && coords[1] != nil
	if !hasCoordinates || !validLocation(*coords[0], *coords[1]) {
		return placeLocation{}, fmt.Errorf("Mapbox returned invalid coordinates")
	}
	return placeLocation{
		Name: suggestion.Label, Lon: *coords[0], Lat: *coords[1],
		Source: "Mapbox Search Box", Attribution: cleanPlaceText(response.Attribution),
	}, nil
}

func (p *placeClient) getJSON(ctx context.Context, endpoint string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("cannot create location request")
	}
	req.Header.Set("User-Agent", "shoe/1 (https://github.com/adamcbrewer/shell-on-earth)")
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// HTTP errors can include the URL, containing a token or personal search query.
		return fmt.Errorf("location service request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("rate limited; try again later")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("location service returned HTTP %d", resp.StatusCode)
	}
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("cannot read location response")
	}
	if len(data) > limit {
		return fmt.Errorf("location response too large")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("invalid location response")
	}
	return nil
}
