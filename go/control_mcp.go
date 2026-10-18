package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type attachedToolArgs struct {
	SessionID      string          `json:"session_id"`
	RequestID      string          `json:"request_id"`
	Generation     string          `json:"generation"`
	Revision       *uint64         `json:"expected_revision"`
	Args           json.RawMessage `json:"args"`
	WaitVisual     bool            `json:"wait_visual"`
	Wait           bool            `json:"wait"`
	TimeoutSeconds *float64        `json:"timeout_seconds"`
}

func attachedMCPServer(info sessionInfo) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "mapbox-tui-viewer", Version: version}, nil)
	addViewerTools(server, info)
	return server
}

func addViewerTools(server *mcp.Server, pinned sessionInfo) {
	for action, spec := range controlSpecs {
		required := []string{"args"}
		description := spec.Description
		if pinned.ID == "" {
			required = append(required, "session_id")
			description += " Supply a session UUID from viewer_sessions; use viewer_status to inspect it."
		}
		if !controlRead(action) {
			required = append(required, "generation", "expected_revision", "request_id")
			description += " Read viewer_status first and supply its generation/revision. Reuse request_id only with identical arguments and preconditions."
		}
		if cameraControl(action) {
			description += " Animated moves return a transition_id and pending operation; use wait_visual=true to await this transition, or inspect viewer_operation_status."
		}
		properties := map[string]any{
			"session_id":        map[string]any{"type": "string", "minLength": 1, "description": "Live session UUID from viewer_sessions. In pinned mode, must match the attached UUID if supplied."},
			"request_id":        map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
			"generation":        map[string]any{"type": "string", "minLength": 1},
			"expected_revision": map[string]any{"type": "integer", "minimum": 0},
			"args":              spec.Schema, "wait": map[string]any{"type": "boolean"},
			"timeout_seconds": map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 300},
		}
		if cameraControl(action) {
			properties["wait_visual"] = map[string]any{"type": "boolean"}
		}
		schema := map[string]any{"type": "object", "additionalProperties": false,
			"required": required, "properties": properties}
		server.AddTool(&mcp.Tool{Name: "viewer_" + strings.ReplaceAll(action, ".", "_"), Description: description, InputSchema: schema},
			func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args attachedToolArgs
				if err := strictJSON(request.Params.Arguments, &args); err != nil {
					return controlMCPResult(controlFailure("invalid_arguments", err.Error(), "use the advertised tool schema")), nil
				}
				if args.WaitVisual && !cameraControl(action) {
					return controlMCPResult(controlFailure("invalid_arguments", "wait_visual requires a camera command", "use a camera tool")), nil
				}
				info := pinned
				if pinned.ID == "" {
					resolved, err := resolveSession(args.SessionID)
					if err != nil {
						return controlMCPResult(controlFailure("session_not_found", err.Error(), "call viewer_sessions and choose a live UUID")), nil
					}
					info = resolved
				}
				if args.SessionID != info.ID && (pinned.ID == "" || args.SessionID != "") {
					return controlMCPResult(controlFailure("session_mismatch", "session_id must match the target UUID", "use the UUID from viewer_sessions; reconnect to change a pinned target")), nil
				}
				timeout := 30 * time.Second
				if args.TimeoutSeconds != nil {
					if *args.TimeoutSeconds <= 0 || *args.TimeoutSeconds > 300 {
						return controlMCPResult(controlFailure(
							"invalid_arguments",
							"timeout_seconds must be >0 and <=300",
							"correct the timeout"),
						), nil
					}
					timeout = time.Duration(*args.TimeoutSeconds * float64(time.Second))
				}
				ctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				response, err := callRemote(ctx, info, controlRequest{ID: args.RequestID, Action: action,
					Generation: args.Generation, Revision: args.Revision, Args: args.Args}, args.Wait || args.WaitVisual, false)
				if err != nil {
					response = controlTransportFailure(response, err,
						"inspect operation status before retrying; explicitly reconnect if the session ended")
					response.SessionID, response.RequestID = info.ID, args.RequestID
				}
				return controlMCPResult(response), nil
			})
	}
	server.AddTool(&mcp.Tool{Name: "viewer_sessions", Description: "List running viewer sessions with UUIDs and optional human names. Use a UUID as session_id in viewer tools. Does not create or switch sessions.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
		func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			records, err := listLiveSessions(ctx)
			if err != nil {
				return controlMCPResult(controlFailure("discovery_failed", err.Error(), "check the local runtime directory")), nil
			}
			return controlMCPResult(controlResponse{OutcomeKnown: true, Version: controlVersion, SessionID: pinned.ID, State: "applied",
				Message: "live sessions listed", Result: controlJSON(map[string]any{"sessions": records})}), nil
		})
}

func controlMCPResult(response controlResponse) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: response.Error != nil, StructuredContent: response,
		Content: []mcp.Content{&mcp.TextContent{Text: string(controlJSON(response))}}}
}

func serveAttachedMCP(target string) error {
	info, err := resolveSession(target)
	if err != nil {
		return fmt.Errorf("attach MCP: %w", err)
	}
	return attachedMCPServer(info).Run(context.Background(), &mcp.StdioTransport{})
}
