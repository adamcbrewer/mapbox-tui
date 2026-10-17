package main

import (
	"context"
	"fmt"
)

type journeyMode string

const (
	journeyManual  journeyMode = ""
	journeyWalking journeyMode = "walking"
	journeyCycling journeyMode = "cycling"
	journeyDriving journeyMode = "driving"
)

func (mode journeyMode) label() string {
	switch mode {
	case journeyWalking:
		return "Walking"
	case journeyCycling:
		return "Cycling"
	case journeyDriving:
		return "Driving"
	default:
		return "Offline"
	}
}

func (mode journeyMode) limitKM() float64 {
	switch mode {
	case journeyWalking:
		return 1000
	case journeyCycling:
		return 1500
	case journeyDriving:
		return 10000
	default:
		return 0
	}
}

type journeyIntent struct {
	Stops []journeyStop
	Mode  journeyMode
}

type journeyRoute struct {
	Track              *Track
	Snapped            []XY
	Distance, Duration float64
	Revision           uint64
	Mode               journeyMode
}

type journeyRouteRequest struct {
	ctx          context.Context
	ref          ItemRef
	id, revision uint64
	mode         journeyMode
	locations    []placeLocation
}

type journeyRouteResult struct {
	ref          ItemRef
	id, revision uint64
	route        *journeyRoute
	err          error
}

func (j *Journey) cancelRoute() {
	if j.cancel != nil {
		j.cancel()
		j.cancel = nil
	}
	j.requestID++
	j.Busy = false
}

func (j *Journey) invalidateRoute() {
	j.cancelRoute()
	j.revision++
	j.Error = ""
}

func (j *Journey) routeCurrent() bool {
	if j.Mode == journeyManual || j.Route == nil {
		return false
	}
	return j.Route.Revision == j.revision && j.Route.Mode == j.Mode
}

func (w *Workspace) setJourneyMode(ref ItemRef, mode journeyMode) error {
	if mode != journeyManual && mode.limitKM() == 0 {
		return fmt.Errorf("unknown journey routing mode")
	}
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	if j.Mode == mode {
		j.modeChosen = true
		return nil
	}
	if err := w.editJourneyIntent(ref, journeyIntent{Stops: j.Stops, Mode: mode}); err != nil {
		return err
	}
	j.modeChosen = true
	return nil
}

func routingLocations(stops []journeyStop, mode journeyMode) ([]placeLocation, error) {
	limit := mode.limitKM()
	if limit == 0 {
		return nil, fmt.Errorf("choose Walking, Cycling or Driving to calculate a route")
	}
	if len(stops) < 2 || len(stops) > 25 {
		return nil, fmt.Errorf("Mapbox accepts 2–25 locations; this plan has %d; remove stops or choose Offline", len(stops))
	}
	locations := make([]placeLocation, 0, len(stops))
	var distance float64
	for i, stop := range stops {
		if stop.Location == nil {
			return nil, fmt.Errorf("%s is not set; choose every location before routing", journeyStopLabel(i, len(stops)))
		}
		location := *stop.Location
		if !validLocation(location.Lon, location.Lat) {
			return nil, fmt.Errorf("%s has invalid coordinates; edit it or choose Offline", journeyStopLabel(i, len(stops)))
		}
		if i > 0 {
			previous := locations[i-1]
			distance += haversine(Point{Lon: previous.Lon, Lat: previous.Lat}, Point{Lon: location.Lon, Lat: location.Lat})
		}
		locations = append(locations, location)
	}
	if distance/1000 > limit {
		return nil, fmt.Errorf(
			"%s plan spans %.0f km between stops; Mapbox limit is %.0f km; shorten it or choose Offline",
			mode.label(),
			distance/1000,
			limit,
		)
	}
	return locations, nil
}

func (w *Workspace) beginJourneyRoute(ctx context.Context, ref ItemRef) (journeyRouteRequest, error) {
	j, err := w.journey(ref)
	if err != nil {
		return journeyRouteRequest{}, err
	}
	j.cancelRoute()
	locations, err := routingLocations(j.Stops, j.Mode)
	if err != nil {
		j.Error = err.Error()
		return journeyRouteRequest{}, err
	}
	ctx, j.cancel = context.WithCancel(ctx)
	j.Busy, j.Error = true, ""
	return journeyRouteRequest{
		ctx: ctx, ref: ref, id: j.requestID, revision: j.revision, mode: j.Mode, locations: locations,
	}, nil
}

func (request journeyRouteRequest) run(client *directionsClient) journeyRouteResult {
	route, err := client.resolve(request.ctx, request.mode, request.locations)
	if route != nil {
		route.Revision = request.revision
	}
	return journeyRouteResult{
		ref: request.ref, id: request.id, revision: request.revision, route: route, err: err,
	}
}

func (w *Workspace) acceptJourneyRoute(result journeyRouteResult) bool {
	j, err := w.journey(result.ref)
	if err != nil {
		return false
	}
	current := result.id == j.requestID && result.revision == j.revision && j.Busy
	if !current {
		return false
	}
	if j.cancel != nil {
		j.cancel()
		j.cancel = nil
	}
	j.Busy = false
	if result.err != nil {
		if j.routeCurrent() {
			j.invalidateRoute()
			if track, err := journeyTrack(j.Stops); err == nil {
				w.replaceJourneyTrack(result.ref, track)
			}
		}
		j.Error = result.err.Error()
		return true
	}
	j.Route, j.Error = result.route, ""
	w.replaceJourneyTrack(result.ref, result.route.Track)
	return true
}

func (w *Workspace) invalidateJourneyRoutes() {
	for _, item := range w.items() {
		j := item.Item.Journey
		if j == nil || j.Mode == journeyManual {
			continue
		}
		j.invalidateRoute()
		track, err := journeyTrack(j.Stops)
		if err == nil {
			w.replaceJourneyTrack(item.Ref, track)
		}
		j.Error = "Mapbox connection changed; calculate again or choose Offline"
	}
}
