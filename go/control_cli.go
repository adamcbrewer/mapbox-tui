package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type remoteOptions struct {
	Target  string
	Request controlRequest
	Wait    bool
	Timeout time.Duration
}

type remoteExitError struct{ code int }

func (e *remoteExitError) Error() string { return "remote command failed" }

func parseControlCLI(argv []string, output io.Writer) (cliCommand, error) {
	command := cliCommand{Name: argv[0]}
	options := &remoteOptions{Timeout: 30 * time.Second}
	command.Remote = options
	fs := flag.NewFlagSet(command.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(
		&options.Target,
		"session",
		"",
		"live session UUID or name",
	)
	fs.StringVar(
		&options.Request.ID,
		"request-id",
		"",
		"idempotency key; reuse the original arguments and preconditions on retry",
	)
	fs.StringVar(
		&options.Request.Generation,
		"generation",
		"",
		"expected workspace generation (default: read current status)",
	)
	var revision uint64
	fs.Uint64Var(
		&revision,
		"expected-revision",
		0,
		"expected workspace revision (default: read current status)",
	)
	fs.BoolVar(
		&command.JSON,
		"json",
		false,
		"print structured JSON",
	)
	fs.BoolVar(
		&options.Wait,
		"wait",
		false,
		"wait for operation completion",
	)
	fs.DurationVar(
		&options.Timeout,
		"timeout",
		30*time.Second,
		"maximum wait, e.g. 30s or 2m",
	)
	var waitVisual bool
	fs.BoolVar(&waitVisual, "wait-visual", false, "wait for this camera transition to settle or be superseded")
	var ref, raw, start, end string
	var waypoints []string
	a := controlArgs{}
	fs.StringVar(&a.Name, "name", "", "saved workspace name")
	fs.BoolVar(&a.SaveAs, "save-as", false, "save a new checkpoint identity")
	var limit int
	fs.IntVar(&a.Offset, "offset", 0, "status document offset")
	fs.IntVar(&a.ItemOffset, "item-offset", 0, "status item offset within each document")
	fs.IntVar(&limit, "limit", 20, "status page size (1..20 documents)")
	fs.BoolVar(&a.Summary, "summary", false, "status essentials only")
	var lat, lon, zoom, bearing float64
	var visible, animate, enabled bool
	fs.StringVar(&a.Theme, "theme", "", "theme ID from theme list")
	fs.Float64Var(&a.Speed, "speed", 0, "playback multiplier: 1, 5, 20, 60, 120")
	fs.BoolVar(&animate, "animate", false, "override camera animation for this command")
	fs.BoolVar(&enabled, "enabled", false, "enable/disable session camera animation")
	var point int
	fs.StringVar(
		&ref,
		"ref",
		"",
		"document_id:item_id (0 item targets whole document for remove/visibility)",
	)
	fs.StringVar(
		&ref,
		"journey",
		"",
		"journey reference as document_id:item_id",
	)
	fs.StringVar(
		&raw,
		"args",
		"",
		"exact action arguments as JSON; cannot be mixed with action flags",
	)
	fs.StringVar(
		&a.Path,
		"path",
		"",
		"local import path",
	)
	fs.BoolVar(
		&visible,
		"visible",
		false,
		"explicit visibility",
	)
	fs.Float64Var(
		&lat,
		"lat",
		0,
		"latitude",
	)
	fs.Float64Var(
		&lon,
		"lon",
		0,
		"longitude",
	)
	fs.Float64Var(
		&zoom,
		"zoom",
		0,
		"absolute zoom (-8..20)",
	)
	fs.Float64Var(
		&bearing,
		"bearing",
		0,
		"bearing (-360..360)",
	)
	fs.Float64Var(
		&a.DX,
		"dx",
		0,
		"horizontal pan in terminal cells",
	)
	fs.Float64Var(
		&a.DY,
		"dy",
		0,
		"vertical pan in terminal cells",
	)
	fs.IntVar(
		&point,
		"point-index",
		0,
		"recorded point index",
	)
	fs.StringVar(
		&a.Mode,
		"mode",
		"",
		"offline, walking, cycling or driving",
	)
	fs.IntVar(
		&a.StopID,
		"stop-id",
		0,
		"stable journey stop ID",
	)
	fs.IntVar(
		&a.After,
		"after",
		0,
		"insert after this stop ID",
	)
	fs.IntVar(
		&a.Direction,
		"direction",
		0,
		"move waypoint by -1 or 1",
	)
	fs.BoolVar(
		&a.Discard,
		"discard",
		false,
		"explicitly discard current working workspace",
	)
	fs.StringVar(
		&start,
		"start",
		"",
		"journey start latitude,longitude",
	)
	fs.StringVar(
		&end,
		"end",
		"",
		"journey destination latitude,longitude",
	)
	fs.Func("waypoint", "intermediate latitude,longitude; repeat in order", func(v string) error {
		waypoints = append(waypoints, v)
		return nil
	})
	words, err := parseOptions(fs, argv[1:])
	if err == flag.ErrHelp {
		fmt.Fprint(output, `Usage: mapbox-tui session list [--json]
       mapbox-tui session inspect UUID_OR_NAME [--json]
       mapbox-tui remote --session UUID_OR_NAME ACTION [OPTIONS]

Actions: status, capabilities, operation status/cancel ID, load FILE,
select, visibility, remove, workspace open/save/clear, camera set/pan/fit,
theme list/set, camera animation, playback play/pause/seek/speed, journey create/inspect/reverse/mode/stops/calculate/show,
journey stop add/set/remove/move.

Mutations use status preconditions unless supplied explicitly. Use --ref or
--journey with document_id:item_id. Loads wait by default; --wait waits for
routing too. Use capabilities --json for exact action schemas.
`)
		fs.SetOutput(output)
		fs.PrintDefaults()
		return command, err
	}
	if err != nil {
		return command, err
	}
	if options.Timeout <= 0 || options.Timeout > 5*time.Minute {
		return command, fmt.Errorf("timeout must be >0 and <=5m")
	}
	if command.Name == "session" {
		var invalid string
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "json" && f.Name != "timeout" {
				invalid = f.Name
			}
		})
		if invalid != "" {
			return command, fmt.Errorf("--%s is not supported by session list/inspect", invalid)
		}
		if len(words) == 1 && words[0] == "list" {
			return command, nil
		}
		if len(words) == 2 && words[0] == "inspect" {
			command.Name = "remote"
			options.Target = words[1]
			words = []string{"status"}
		} else {
			return command, fmt.Errorf("use session list or session inspect UUID_OR_NAME")
		}
	}
	if options.Target == "" {
		return command, fmt.Errorf("--session is required")
	}
	if len(words) == 0 {
		return command, fmt.Errorf("an action is required; see remote --help")
	}
	action := strings.Join(words, ".")
	switch {
	case len(words) == 3 && words[0] == "workspace" && words[1] == "open":
		a.Path = words[2]
		action = "workspace.open"
		options.Wait = true
	case words[0] == "load" && len(words) == 2:
		a.Path = words[1]
		action = "load"
		options.Wait = true
	case len(words) == 3 && words[0] == "operation" && (words[1] == "status" || words[1] == "cancel"):
		a.OperationID = words[2]
		action = "operation." + words[1]
	}
	if action == "load" || action == "workspace.open" {
		options.Wait = true
	}
	if waitVisual {
		if !cameraControl(action) && action != "fit" {
			return command, fmt.Errorf("--wait-visual requires camera set/pan/fit or journey show")
		}
		options.Wait = true
	}
	if action == "fit" {
		action = "camera.fit"
	}
	if _, ok := controlSpecs[action]; !ok {
		return command, fmt.Errorf("unknown action %q", action)
	}
	if ref != "" {
		parts := strings.Split(ref, ":")
		if len(parts) != 2 {
			return command, fmt.Errorf("reference must be document_id:item_id")
		}
		a.Ref.DocumentID, err = strconv.Atoi(parts[0])
		if err != nil {
			return command, err
		}
		a.Ref.ItemID, err = strconv.Atoi(parts[1])
		if err != nil {
			return command, err
		}
	}
	if start != "" || end != "" || len(waypoints) > 0 {
		if start == "" || end == "" {
			return command, fmt.Errorf("start and end are required together")
		}
		coordinates := append([]string{start}, waypoints...)
		coordinates = append(coordinates, end)
		for _, value := range coordinates {
			location, matched, err := coordinateLocation(value)
			if err != nil || !matched {
				return command, fmt.Errorf("invalid coordinates %q", value)
			}
			a.Stops = append(a.Stops, controlStop{Lat: location.Lat, Lon: location.Lon})
		}
	}
	supplied := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "expected-revision":
			options.Request.Revision = &revision
		case "lat":
			a.Lat = &lat
			supplied["lat"] = true
		case "lon":
			a.Lon = &lon
			supplied["lon"] = true
		case "zoom":
			a.Zoom = &zoom
			supplied["zoom"] = true
		case "bearing":
			a.Bearing = &bearing
			supplied["bearing"] = true
		case "limit":
			a.Limit = &limit
			supplied["limit"] = true
		case "animate":
			a.Animate = &animate
			supplied["animate"] = true
		case "enabled":
			a.Enabled = &enabled
			supplied["enabled"] = true
		case "visible":
			a.Visible = &visible
			supplied["visible"] = true
		case "point-index":
			a.PointIndex = &point
			supplied["point_index"] = true
		case "ref", "journey":
			supplied["ref"] = true
		case "start", "end", "waypoint":
			supplied["stops"] = true
		case "session", "request-id", "generation", "json", "wait", "wait-visual", "timeout", "args":
		default:
			supplied[strings.ReplaceAll(f.Name, "-", "_")] = true
		}
	})
	if a.Path != "" {
		supplied["path"] = true
	}
	if a.OperationID != "" {
		supplied["operation_id"] = true
	}
	var fields map[string]json.RawMessage
	if raw != "" {
		if len(supplied) > 0 {
			return command, fmt.Errorf("--args cannot be mixed with action flags")
		}
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return command, err
		}
	} else {
		// Marshal the full struct, then retain only explicitly supplied fields. Zero
		// values are meaningful for pan, stop indices and explicit discard=false.
		full := map[string]any{"path": a.Path, "ref": a.Ref, "visible": a.Visible, "lat": a.Lat, "lon": a.Lon,
			"zoom": a.Zoom, "bearing": a.Bearing, "dx": a.DX, "dy": a.DY, "point_index": a.PointIndex,
			"stops": a.Stops, "mode": a.Mode, "stop_id": a.StopID, "after": a.After, "direction": a.Direction,
			"operation_id": a.OperationID, "discard": a.Discard,
			"name": a.Name, "save_as": a.SaveAs, "offset": a.Offset, "item_offset": a.ItemOffset, "limit": a.Limit, "summary": a.Summary,
			"theme": a.Theme, "speed": a.Speed, "animate": a.Animate, "enabled": a.Enabled}
		fields = make(map[string]json.RawMessage)
		for key := range supplied {
			fields[key] = controlJSON(full[key])
		}
	}
	if path, ok := fields["path"]; ok {
		var value string
		if err := json.Unmarshal(path, &value); err != nil {
			return command, err
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return command, err
		}
		fields["path"] = controlJSON(absolute)
	}
	options.Request.Action, options.Request.Args = action, controlJSON(fields)
	if _, err := decodeControlArgs(action, options.Request.Args); err != nil {
		return command, err
	}
	if options.Request.ID == "" {
		options.Request.ID = newSessionID()
	}
	return command, nil
}

func listLiveSessions(ctx context.Context) ([]sessionInfo, error) {
	records, err := discoverSessions()
	if err != nil {
		return nil, err
	}
	for i, info := range records {
		response, err := sendControl(ctx, info, controlRequest{Action: "status", Args: controlJSON(map[string]any{"summary": true})})
		if err == nil && response.Error == nil {
			records[i].Status = response.Result
		}
	}
	return records, nil
}

func runRemote(ctx context.Context, c cliCommand, out io.Writer) error {
	options := c.Remote
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	if c.Name == "session" {
		records, err := listLiveSessions(ctx)
		if err != nil {
			return err
		}
		if c.JSON {
			return json.NewEncoder(out).Encode(map[string]any{"sessions": records})
		}
		for _, info := range records {
			fmt.Fprintf(out, "%s  %s  %s\n", info.ID, info.Name, info.StartedAt.Format(time.RFC3339))
		}
		return nil
	}
	info, err := resolveSession(options.Target)
	var response controlResponse
	if err == nil {
		response, err = callRemote(ctx, info, options.Request, options.Wait, true)
	}
	if err != nil {
		response = controlTransportFailure(response, err,
			"list sessions; inspect the operation before retrying a mutation")
		response.RequestID, response.SessionID = options.Request.ID, info.ID
	}
	if c.JSON {
		err = json.NewEncoder(out).Encode(response)
	} else {
		_, err = fmt.Fprintln(out, cleanPlaceText(response.Message))
		if err == nil && len(response.Result) > 0 {
			_, err = fmt.Fprintln(out, string(response.Result))
		}
		if response.OperationID != "" {
			fmt.Fprintln(out, "Operation:", cleanPlaceText(response.OperationID))
		}
		if response.Error != nil {
			fmt.Fprintln(out, "Recovery:", cleanPlaceText(response.Error.Recovery))
		}
	}
	if err != nil {
		return err
	}
	if response.Error != nil {
		code := 2
		switch response.Error.Code {
		case "revision_conflict", "workspace_revision_conflict", "workspace_changed", "request_id_conflict":
			code = 3
		case "session_not_found", "session_disconnected", "routing_unavailable":
			code = 4
		case "timeout":
			code = 5
		}
		return &remoteExitError{code: code}
	}
	return nil
}

func callRemote(
	ctx context.Context,
	info sessionInfo,
	request controlRequest,
	wait, autoGuard bool,
) (controlResponse, error) {
	if request.ID == "" {
		request.ID = newSessionID()
	}
	if autoGuard && !controlRead(request.Action) && (request.Generation == "" || request.Revision == nil) {
		status, err := sendControl(ctx, info, controlRequest{Action: "status", Args: controlJSON(map[string]any{"summary": true})})
		if err != nil {
			return controlResponse{}, err
		}
		if status.Error != nil {
			status.RequestID = request.ID
			return status, nil
		}
		if request.Generation == "" {
			request.Generation = status.Generation
		}
		if request.Revision == nil {
			request.Revision = &status.Revision
		}
	}
	response, err := sendControl(ctx, info, request)
	if err != nil || !wait || response.State != "pending" {
		return response, err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	operation := response.OperationID
	for {
		select {
		case <-ctx.Done():
			return controlWaitTimeout(response), nil
		case <-ticker.C:
			next, err := sendControl(ctx, info, controlRequest{
				Action: "operation.status",
				Args:   controlJSON(map[string]any{"operation_id": operation}),
			})
			if err != nil {
				if ctx.Err() != nil {
					return controlWaitTimeout(response), nil
				}
				return response, err
			}
			next.RequestID = request.ID
			response = next
			if response.State != "pending" {
				return response, nil
			}
		}
	}
}

func controlWaitTimeout(response controlResponse) controlResponse {
	response.State = "pending"
	response.OutcomeKnown = false
	response.Message = "wait timed out; operation may still complete"
	response.Error = &controlError{
		Code:     "timeout",
		Message:  response.Message,
		Recovery: "query operation.status with operation_id",
	}
	return response
}
