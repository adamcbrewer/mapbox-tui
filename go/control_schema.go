package main

import (
	"encoding/json"
	"fmt"
	"math"

	"mapbox-tui/shared"
)

type controlArgs struct {
	Theme       string        `json:"theme,omitempty"`
	Speed       float64       `json:"speed,omitempty"`
	Animate     *bool         `json:"animate,omitempty"`
	Enabled     *bool         `json:"enabled,omitempty"`
	Path        string        `json:"path,omitempty"`
	Ref         ItemRef       `json:"ref,omitempty"`
	Visible     *bool         `json:"visible,omitempty"`
	Lat         *float64      `json:"lat,omitempty"`
	Lon         *float64      `json:"lon,omitempty"`
	Zoom        *float64      `json:"zoom,omitempty"`
	Bearing     *float64      `json:"bearing,omitempty"`
	DX          float64       `json:"dx,omitempty"`
	DY          float64       `json:"dy,omitempty"`
	PointIndex  *int          `json:"point_index,omitempty"`
	Stops       []controlStop `json:"stops,omitempty"`
	Mode        string        `json:"mode,omitempty"`
	StopID      int           `json:"stop_id,omitempty"`
	After       int           `json:"after,omitempty"`
	Direction   int           `json:"direction,omitempty"`
	OperationID string        `json:"operation_id,omitempty"`
	Discard     bool          `json:"discard,omitempty"`
}

type controlStop struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type controlSpec struct {
	Description string `json:"description"`
	Schema      struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties bool                       `json:"additionalProperties"`
	} `json:"inputSchema"`
}

var controlSpecs = func() map[string]controlSpec {
	var specs map[string]controlSpec
	if err := json.Unmarshal(shared.ControlTools, &specs); err != nil {
		panic(err)
	}
	return specs
}()

func controlCapabilities() any {
	return map[string]any{"protocol_version": controlVersion, "actions": controlSpecs,
		"operation_retention_seconds": operationRetention.Seconds(), "operation_capacity": operationCapacity,
		"pending_capacity": pendingCapacity, "persistence_available": false}
}

func decodeControlArgs(action string, data json.RawMessage) (controlArgs, error) {
	var args controlArgs
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	var fields map[string]json.RawMessage
	if err := strictJSON(data, &fields); err != nil || fields == nil {
		return args, fmt.Errorf("args must be an object")
	}
	spec := controlSpecs[action]
	for key, value := range fields {
		if _, ok := spec.Schema.Properties[key]; !ok {
			return args, fmt.Errorf("unknown argument %q for %s", key, action)
		}
		if string(value) == "null" {
			return args, fmt.Errorf("argument %q cannot be null", key)
		}
	}
	for _, key := range spec.Schema.Required {
		if _, ok := fields[key]; !ok {
			return args, fmt.Errorf("%s is required", key)
		}
	}
	if err := strictJSON(data, &args); err != nil {
		return args, err
	}
	if value, ok := fields["ref"]; ok {
		var ref map[string]json.RawMessage
		if err := json.Unmarshal(value, &ref); err != nil {
			return args, err
		}
		if args.Ref.DocumentID < 1 {
			return args, fmt.Errorf("ref.document_id must be positive")
		}
		_, hasItem := ref["item_id"]
		if !hasItem || string(ref["item_id"]) == "null" || args.Ref.ItemID < 0 {
			return args, fmt.Errorf("ref.item_id is required; use 0 only for whole-document visibility/removal")
		}
		if args.Ref.ItemID == 0 && action != "visibility" && action != "remove" {
			return args, fmt.Errorf("ref.item_id must be positive")
		}
	}
	if action == "playback.speed" {
		valid := false
		for _, speed := range playbackSpeeds {
			valid = valid || args.Speed == speed
		}
		if !valid {
			return args, fmt.Errorf("speed must be one of 1, 5, 20, 60, 120")
		}
	}
	if args.Path == "" && action == "load" {
		return args, fmt.Errorf("path must not be empty")
	}
	if len(args.Stops) > 1000 {
		return args, fmt.Errorf("at most 1000 stops are supported")
	}
	if raw, ok := fields["stops"]; ok {
		var stops []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &stops); err != nil {
			return args, err
		}
		for i, stop := range stops {
			if _, ok := stop["lat"]; !ok {
				return args, fmt.Errorf("stop latitude is required")
			}
			if _, ok := stop["lon"]; !ok {
				return args, fmt.Errorf("stop longitude is required")
			}
			if string(stop["lat"]) == "null" || string(stop["lon"]) == "null" || !validLocation(args.Stops[i].Lon, args.Stops[i].Lat) {
				return args, fmt.Errorf("invalid stop coordinates")
			}
		}
	}
	if math.Abs(args.DX) > 10000 || math.Abs(args.DY) > 10000 {
		return args, fmt.Errorf("pan is limited to 10000 terminal cells per command")
	}
	return args, nil
}
