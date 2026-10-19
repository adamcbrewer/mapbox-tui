package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m *viewer) journeyStatus(ref ItemRef, j *Journey) map[string]any {
	stops := make([]map[string]any, 0, len(j.Stops))
	for _, stop := range j.Stops {
		value := map[string]any{"stop_id": stop.ID, "location": nil}
		if l := stop.Location; l != nil {
			value["location"] = map[string]any{
				"lat": l.Lat, "lon": l.Lon, "name": controlText(l.Name, 256), "source": l.Source, "attribution": controlText(l.Attribution, 512)}
		}
		stops = append(stops, value)
	}
	result := m.journeySummary(ref, j)
	result["stops"] = stops
	return result
}

func (m *viewer) journeySummary(ref ItemRef, j *Journey) map[string]any {
	mode := string(j.Mode)
	if mode == "" {
		mode = "offline"
	}
	state := "unresolved"
	switch {
	case j.Mode == journeyManual:
		state = "offline"
	case j.Busy:
		state = "pending"
	case j.routeCurrent():
		state = "ready"
	case j.Error != "":
		state = "failed"
	case j.Route != nil:
		state = "stale"
	}
	result := map[string]any{"ref": ref, "revision": j.revision, "stop_count": len(j.Stops), "mode": mode,
		"route_state": state, "error": controlText(j.Error, 512), "geometry": "straight_line_preview"}
	if state == "ready" {
		result["geometry"] = "road_route"
		result["distance_m"], result["duration_s"] = j.Route.Distance, j.Route.Duration
		result["route_revision"], result["attribution"] = j.Route.Revision, directionsCredits
	}
	return result
}

func controlJourneyMode(value string) (journeyMode, error) {
	switch value {
	case "offline":
		return journeyManual, nil
	case "walking", "cycling", "driving":
		return journeyMode(value), nil
	default:
		return "", fmt.Errorf("mode must be offline, walking, cycling or driving")
	}
}

func controlLocation(lat, lon *float64) (placeLocation, error) {
	if lat == nil || lon == nil || !validLocation(*lon, *lat) {
		return placeLocation{}, fmt.Errorf("valid lat and lon are required")
	}
	return placeLocation{Lat: *lat, Lon: *lon, Source: placeSourceCoordinates,
		Name: fmt.Sprintf("%.6f, %.6f", *lat, *lon)}, nil
}

func (m *viewer) executeJourneyControl(
	r controlRequest,
	a controlArgs,
	op *controlOperation,
) (controlResponse, tea.Cmd) {
	response := m.response(r)
	ref := a.Ref
	var err error
	var j *Journey
	if r.Action == "journey.create" {
		mode, modeErr := controlJourneyMode(a.Mode)
		if modeErr != nil {
			return m.fail(
				r,
				"invalid_arguments",
				modeErr.Error(),
				"choose a supported mode",
			), nil
		}
		ref, err = m.workspace.createJourneyWithLocations(controlLocations(a.Stops), mode)
		if err != nil {
			return m.fail(
				r,
				"invalid_arguments",
				err.Error(),
				"correct the stops",
			), nil
		}
		j, _ = m.workspace.journey(ref)
	} else {
		j, err = m.workspace.journey(ref)
		if err != nil {
			return m.fail(
				r,
				"item_not_found",
				err.Error(),
				"read status for current journey references",
			), nil
		}
		switch r.Action {
		case "journey.reverse":
			err = m.workspace.reverseJourney(ref)
		case "journey.mode":
			var mode journeyMode
			mode, err = controlJourneyMode(a.Mode)
			if err == nil {
				err = m.workspace.setJourneyMode(ref, mode)
			}
		case "journey.stops":
			err = m.workspace.replaceJourneyLocations(ref, controlLocations(a.Stops))
		case "journey.stop.add":
			var location placeLocation
			location, err = controlLocation(a.Lat, a.Lon)
			if err != nil {
				break
			}
			err = m.workspace.insertJourneyWaypoint(ref, a.After, &location)
		case "journey.stop.set":
			var location placeLocation
			location, err = controlLocation(a.Lat, a.Lon)
			if err == nil {
				location = m.workspace.journeyCoordinateEdit(ref, a.StopID, location)
				err = m.workspace.setJourneyLocation(ref, a.StopID, location)
			}
		case "journey.stop.remove", "journey.stop.move":
			index := slices.IndexFunc(j.Stops, func(stop journeyStop) bool { return stop.ID == a.StopID })
			if r.Action == "journey.stop.remove" {
				err = m.workspace.removeJourneyLocation(ref, index)
			} else {
				err = m.workspace.moveJourneyWaypoint(ref, index, a.Direction)
			}
		case "journey.show":
			entry, index, _ := m.workspace.lookup(ref)
			if len(entry.document.Items[index].Track.Points) == 0 {
				err = fmt.Errorf("journey has no geometry to show")
				break
			}
			err = m.workspace.selectItem(ref)
			if err == nil {
				m.selectionChanged()
				before := m.destination()
				m.controlMoveCamera(journeyView(j, entry.document.Items[index].Track, before), a.Animate)
				m.setFollowing(false)
				m.recordViewChange(before)
				m.layout()
				m.dirty = true
				response.Result = controlJSON(m.journeyStatus(ref, j))
				return response, m.fetch()
			}
		case "journey.calculate":
		default:
			err = fmt.Errorf("unsupported journey action")
		}
	}
	if err != nil {
		return m.fail(
			r,
			"invalid_arguments",
			err.Error(),
			"inspect the journey and correct the arguments",
		), nil
	}
	m.layout()
	m.dirty = true
	response.Result = controlJSON(m.journeyStatus(ref, j))
	response.Message = "journey updated"
	if j.Mode == journeyManual {
		response.Message = "offline journey applied; geometry is a straight-line preview"
		return response, nil
	}
	if !j.hasCompleteLocations() {
		response.Message = "journey updated; set all stop locations before routing"
		return response, nil
	}
	if j.routeCurrent() && r.Action != "journey.calculate" {
		return response, nil
	}
	if m.directions == nil || m.preferences.productAccess(m.directions.token) != mapboxAllowed {
		response.State = "failed"
		response.Message = "journey retained; routing is unavailable while offline, unacknowledged or disconnected"
		if j.Error != response.Message {
			j.Error = response.Message
			m.workspace.edited()
		}
		response.Result = controlJSON(m.journeyStatus(ref, j))
		response.Error = &controlError{Code: "routing_unavailable", Message: response.Message,
			Recovery: "enable Mapbox through account settings or choose offline mode"}
		return response, nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	request, err := m.workspace.beginJourneyRoute(ctx, ref)
	if err != nil {
		cancel()
		response.State = "failed"
		response.Message = "journey retained; " + err.Error()
		response.Result = controlJSON(m.journeyStatus(ref, j))
		response.Error = &controlError{
			Code:     "routing_failed",
			Message:  response.Message,
			Recovery: "inspect the stops and correct the route",
		}
		return response, nil
	}
	op.ref, op.routeID, op.routeRevision, op.cancel = ref, request.id, request.revision, cancel
	response.State, response.Message = "pending", "journey applied; route calculation pending"
	response.Result = controlJSON(m.journeyStatus(ref, j))
	client := m.directions
	return response, func() tea.Msg { defer cancel(); return request.run(client) }
}

func controlLocations(stops []controlStop) []placeLocation {
	locations := make([]placeLocation, len(stops))
	for i, stop := range stops {
		locations[i], _ = controlLocation(&stop.Lat, &stop.Lon)
	}
	return locations
}
