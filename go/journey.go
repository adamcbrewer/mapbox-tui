package main

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

const maxJourneyStops = 5000

var coordinateNumberPrefix = regexp.MustCompile(`^[+-]?(([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]*)?|\.)?$`)

type placeLocation struct {
	Name, Attribution string
	Source            placeSource
	ProviderID        string
	Lon, Lat          float64
}

func (location placeLocation) userEntered() bool {
	return location.Source.userEntered() && location.Attribution == ""
}

type journeyStop struct {
	ID       int
	Location *placeLocation
}

func journeyStopLabel(index, count int) string {
	if index == 0 {
		return "A Start"
	}
	if index == count-1 {
		return "B Destination"
	}
	return fmt.Sprintf("%d Waypoint", index)
}

type Journey struct {
	Stops                      []journeyStop
	Mode                       journeyMode
	Route                      *journeyRoute
	Error                      string
	Busy                       bool
	modeChosen                 bool
	automaticRoutePending      bool
	automaticRoutePreserveView bool
	nextID                     int
	history                    []journeyIntent
	revision                   uint64
	requestID                  uint64
	cancel                     context.CancelFunc
}

func (j *Journey) hasCompleteLocations() bool {
	if len(j.Stops) < 2 {
		return false
	}
	for _, stop := range j.Stops {
		if stop.Location == nil {
			return false
		}
	}
	return true
}

func (w *Workspace) newJourney() ItemRef {
	w.createJourney()
	return w.activateDocument(w.documents[len(w.documents)-1])
}

// createJourney adds an editable journey without changing selection or playback.
func (w *Workspace) createJourney() ItemRef {
	d := &Document{Name: "Planned journey", Filename: "Planned journey", Format: "planned"}
	item := d.addItem("planned")
	item.Track.Name = d.Name
	item.Journey = &Journey{Stops: []journeyStop{{ID: 1}, {ID: 2}}, nextID: 2}
	entry := w.appendDocument(d)
	return ItemRef{DocumentID: entry.id, ItemID: item.ID}
}

// Create only after validation, so a rejected plan cannot consume an identity.
func (w *Workspace) createJourneyWithLocations(locations []placeLocation, mode journeyMode) (ItemRef, error) {
	stops, err := journeyStopsFromLocations(locations, 0)
	if err != nil {
		return ItemRef{}, err
	}
	return w.createJourneyFromIntent(journeyIntent{Stops: stops, Mode: mode})
}

func (w *Workspace) createJourneyFromIntent(intent journeyIntent) (ItemRef, error) {
	intent, track, err := prepareInitialJourney(intent)
	if err != nil {
		return ItemRef{}, err
	}
	ref := w.createJourney()
	w.applyInitialJourney(ref, intent, track)
	return ref, nil
}

func (w *Workspace) initializeJourney(ref ItemRef, locations []placeLocation, mode journeyMode) error {
	if _, err := w.journey(ref); err != nil {
		return err
	}
	stops, err := journeyStopsFromLocations(locations, 0)
	if err != nil {
		return err
	}
	intent, track, err := prepareInitialJourney(journeyIntent{Stops: stops, Mode: mode})
	if err != nil {
		return err
	}
	w.applyInitialJourney(ref, intent, track)
	return nil
}

func prepareInitialJourney(intent journeyIntent) (journeyIntent, *Track, error) {
	if intent.Mode != journeyManual && intent.Mode.limitKM() == 0 {
		return journeyIntent{}, nil, fmt.Errorf("unknown journey routing mode")
	}
	if err := validateJourneyStopCount(len(intent.Stops)); err != nil {
		return journeyIntent{}, nil, err
	}
	stops := make([]journeyStop, len(intent.Stops))
	for i, stop := range intent.Stops {
		stops[i].ID = i + 1
		if stop.Location != nil {
			location := *stop.Location
			stops[i].Location = &location
		}
	}
	track, err := journeyTrack(stops)
	return journeyIntent{Stops: stops, Mode: intent.Mode}, track, err
}

func (w *Workspace) applyInitialJourney(ref ItemRef, intent journeyIntent, track *Track) {
	j, _ := w.journey(ref)
	j.Stops, j.Mode = intent.Stops, intent.Mode
	j.nextID, j.modeChosen = len(intent.Stops), true
	j.history = nil
	j.automaticRoutePending = false
	j.invalidateRoute()
	w.replaceJourneyTrack(ref, track)
}

func journeyStopsFromLocations(locations []placeLocation, nextID int) ([]journeyStop, error) {
	if err := validateJourneyStopCount(len(locations)); err != nil {
		return nil, err
	}
	stops := make([]journeyStop, len(locations))
	for i, location := range locations {
		stops[i] = journeyStop{ID: nextID + i + 1, Location: &location}
	}
	return stops, nil
}

func (w *Workspace) journey(ref ItemRef) (*Journey, error) {
	entry, i, err := w.lookup(ref)
	if err != nil {
		return nil, err
	}
	if j := entry.document.Items[i].Journey; j != nil {
		return j, nil
	}
	return nil, fmt.Errorf("item is not an editable journey")
}

func (w *Workspace) editJourney(ref ItemRef, stops []journeyStop) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	return w.editJourneyIntent(ref, journeyIntent{Stops: stops, Mode: j.Mode})
}

func (w *Workspace) replaceJourneyLocations(ref ItemRef, locations []placeLocation) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	stops, err := journeyStopsFromLocations(locations, j.nextID)
	if err != nil {
		return err
	}
	return w.editJourneyIntent(ref, journeyIntent{Stops: stops, Mode: j.Mode})
}

func (w *Workspace) editJourneyIntent(ref ItemRef, intent journeyIntent) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	stops := intent.Stops
	track, err := journeyTrack(stops)
	if err != nil {
		return err
	}
	j.automaticRoutePending = false
	j.history = append(j.history, journeyIntent{Stops: j.Stops, Mode: j.Mode})
	if len(j.history) > 64 {
		j.history = slices.Delete(j.history, 0, 1)
	}
	j.Stops = slices.Clone(stops)
	for _, stop := range stops {
		j.nextID = max(j.nextID, stop.ID)
	}
	j.Mode = intent.Mode
	j.invalidateRoute()
	w.replaceJourneyTrack(ref, track)
	return nil
}

func (w *Workspace) replaceJourneyTrack(ref ItemRef, track *Track) {
	entry, i, _ := w.lookup(ref)
	w.edited()
	entry.document.Items[i].Track = track
	entry.document.Bounds = track.Bounds
	if w.selection != nil && w.selection.ref == ref {
		w.activate(ref, track)
	}
}

// Editing displayed coordinates retains the existing origin; choosing another
// location (including a favourite) is a replacement and keeps its own origin.
func (w *Workspace) journeyCoordinateEdit(ref ItemRef, id int, location placeLocation) placeLocation {
	j, err := w.journey(ref)
	if err != nil {
		return location
	}
	for _, stop := range j.Stops {
		if stop.ID != id || stop.Location == nil {
			continue
		}
		previous := *stop.Location
		if location.Lon == previous.Lon && location.Lat == previous.Lat {
			return previous
		}
		location.Attribution = previous.Attribution
		if !previous.userEntered() {
			location.Source = previous.Source
		}
		break
	}
	return location
}

func (w *Workspace) setJourneyLocation(ref ItemRef, id int, location placeLocation) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	stops := slices.Clone(j.Stops)
	for i, stop := range stops {
		if stop.ID == id {
			location.Name = cleanPlaceText(location.Name)
			if location.Source != "" && !location.Source.valid() {
				return fmt.Errorf("unknown location source %q", location.Source)
			}
			location.Attribution = cleanPlaceText(location.Attribution)
			if stop.Location != nil && *stop.Location == location {
				return nil
			}
			stops[i].Location = &location
			return w.editJourney(ref, stops)
		}
	}
	return fmt.Errorf("unknown journey location")
}

func (w *Workspace) addJourneyWaypoint(ref ItemRef) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	return w.insertJourneyWaypoint(ref, j.Stops[len(j.Stops)-2].ID, nil)
}

func (w *Workspace) insertJourneyWaypoint(ref ItemRef, after int, location *placeLocation) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(j.Stops, func(stop journeyStop) bool { return stop.ID == after })
	if index < 0 || index == len(j.Stops)-1 {
		return fmt.Errorf("after must identify a stop before the destination")
	}
	if location != nil {
		owned := *location
		location = &owned
	}
	stop := journeyStop{ID: j.nextID + 1, Location: location}
	stops := slices.Insert(slices.Clone(j.Stops), index+1, stop)
	return w.editJourney(ref, stops)
}

func (w *Workspace) removeJourneyLocation(ref ItemRef, index int) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(j.Stops) {
		return fmt.Errorf("unknown journey location")
	}
	stops := slices.Clone(j.Stops)
	if index == 0 || index == len(stops)-1 {
		stops[index].Location = nil
	} else {
		stops = slices.Delete(stops, index, index+1)
	}
	return w.editJourney(ref, stops)
}

func (w *Workspace) moveJourneyWaypoint(ref ItemRef, index, direction int) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	target := index + direction
	validDirection := direction == -1 || direction == 1
	validIndices := index > 0 && index < len(j.Stops)-1 && target > 0 && target < len(j.Stops)-1
	if !validDirection || !validIndices {
		return fmt.Errorf("waypoints can only move between the start and destination")
	}
	stops := slices.Clone(j.Stops)
	stops[index], stops[target] = stops[target], stops[index]
	return w.editJourney(ref, stops)
}

func (w *Workspace) reverseJourney(ref ItemRef) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	stops := slices.Clone(j.Stops)
	slices.Reverse(stops)
	return w.editJourney(ref, stops)
}

func (w *Workspace) undoJourney(ref ItemRef) error {
	j, err := w.journey(ref)
	if err != nil {
		return err
	}
	if len(j.history) == 0 {
		return fmt.Errorf("no journey edits to undo")
	}
	intent := j.history[len(j.history)-1]
	track, err := journeyTrack(intent.Stops)
	if err != nil {
		return err
	}
	j.Stops, j.Mode = intent.Stops, intent.Mode
	j.invalidateRoute()
	j.history = slices.Delete(j.history, len(j.history)-1, len(j.history))
	w.replaceJourneyTrack(ref, track)
	return nil
}

func validateJourneyStopCount(count int) error {
	if count < 2 {
		return fmt.Errorf("at least two stops are required")
	}
	if count > maxJourneyStops {
		return fmt.Errorf("at most %d stops are supported", maxJourneyStops)
	}
	return nil
}

func journeyTrack(stops []journeyStop) (*Track, error) {
	if err := validateJourneyStopCount(len(stops)); err != nil {
		return nil, err
	}
	t := &Track{Name: "Planned journey"}
	segment := -1
	for i, stop := range stops {
		if stop.Location == nil {
			segment = -1
			continue
		}
		location := stop.Location
		point := Point{Lon: location.Lon, Lat: location.Lat, Name: location.Name}
		t.appendPoint(point, &segment, "route")
		if i > 0 && i < len(stops)-1 {
			t.Waypoints = append(t.Waypoints, point)
		}
	}
	if len(t.Points) == 0 {
		return t, nil
	}
	if err := prepareTrackWithNameCleaner(t, cleanPlaceText); err != nil {
		return nil, fmt.Errorf("journey: %w", err)
	}
	t.RouteCount = 1
	return t, nil
}

func coordinateLocation(input string) (placeLocation, bool, error) {
	parts := strings.Split(input, ",")
	if len(parts) != 2 {
		return placeLocation{}, false, nil
	}
	lat, latErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, lonErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if latErr != nil || lonErr != nil {
		return placeLocation{}, false, nil
	}
	if !validLocation(lon, lat) {
		return placeLocation{}, true, fmt.Errorf("coordinates must be latitude -90..90, longitude -180..180")
	}
	return placeLocation{
		Name: fmt.Sprintf("%.6f, %.6f", lat, lon), Lon: lon, Lat: lat, Source: placeSourceCoordinates,
	}, true, nil
}

func coordinatePrefix(input string) bool {
	if !strings.ContainsAny(input, ",.+-eE") {
		return false
	}
	lat, lon, pair := strings.Cut(input, ",")
	if !coordinateNumberPrefix.MatchString(strings.TrimSpace(lat)) {
		return false
	}
	return !pair || coordinateNumberPrefix.MatchString(strings.TrimSpace(lon))
}

func validLocation(lon, lat float64) bool {
	finite := !math.IsNaN(lon) && !math.IsNaN(lat) && !math.IsInf(lon, 0) && !math.IsInf(lat, 0)
	return finite && math.Abs(lon) <= 180 && math.Abs(lat) <= 90
}

func cleanPlaceText(text string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return ' '
		}
		return r
	}, text)
}
