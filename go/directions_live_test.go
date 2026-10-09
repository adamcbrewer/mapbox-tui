//go:build integration

package main

import (
	"os"
	"testing"
)

func TestDirectionsAccountAccess(t *testing.T) {
	if os.Getenv("SHOE_DIRECTIONS_CHECK") != "1" {
		t.Skip("set SHOE_DIRECTIONS_CHECK=1 to make three live Mapbox Directions requests")
	}
	credential := resolveCredential(t.Context())
	if credential.token == "" {
		t.Fatal("no selected Mapbox token: " + credential.guidance)
	}
	client := newDirectionsClient(credential.token)
	locations := []placeLocation{{Lon: -.1276, Lat: 51.5072}, {Lon: -.1419, Lat: 51.5014}}
	for _, mode := range []journeyMode{journeyWalking, journeyCycling, journeyDriving} {
		t.Run(string(mode), func(t *testing.T) {
			route, err := client.resolve(t.Context(), mode, locations)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %.2f km, estimated %s, credential source %s", mode.label(), route.Distance/1000,
				journeyDuration(route.Duration), credential.source)
		})
	}
}
