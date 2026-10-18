package main

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"mapbox-tui/shared"
)

type controlArgs struct {
	Name        string        `json:"name,omitempty"`
	SaveAs      bool          `json:"save_as,omitempty"`
	Offset      int           `json:"offset,omitempty"`
	ItemOffset  int           `json:"item_offset,omitempty"`
	Limit       *int          `json:"limit,omitempty"`
	Summary     bool          `json:"summary,omitempty"`
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
	Description string             `json:"description"`
	Schema      *jsonschema.Schema `json:"inputSchema"`
	resolved    *jsonschema.Resolved
}

var controlSpecs = func() map[string]controlSpec {
	var specs map[string]controlSpec
	if err := json.Unmarshal(shared.ControlTools, &specs); err != nil {
		panic(err)
	}
	for action, spec := range specs {
		resolved, err := spec.Schema.Resolve(nil)
		if err != nil {
			panic(fmt.Sprintf("invalid schema for %s: %v", action, err))
		}
		spec.resolved = resolved
		specs[action] = spec
	}
	return specs
}()

func controlCapabilities() any {
	return map[string]any{"protocol_version": controlVersion, "actions": controlSpecs,
		"operation_retention_seconds": operationRetention.Seconds(), "operation_capacity": operationCapacity,
		"pending_capacity": pendingCapacity, "recovery_capacity": recoveryCapacity, "persistence_available": true}
}

func decodeControlArgs(action string, data json.RawMessage) (controlArgs, error) {
	var args controlArgs
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	var value any
	if err := strictJSON(data, &value); err != nil {
		return args, err
	}
	spec, ok := controlSpecs[action]
	if !ok {
		return args, fmt.Errorf("unknown action %q", action)
	}
	if err := spec.resolved.Validate(value); err != nil {
		return args, fmt.Errorf("invalid %s arguments: %w", action, err)
	}
	if err := strictJSON(data, &args); err != nil {
		return args, err
	}
	return args, nil
}
