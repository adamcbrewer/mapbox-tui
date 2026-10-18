package main

import (
	"io"
	"path/filepath"
	"testing"
)

func TestControlCLICommands(t *testing.T) {
	for _, test := range []struct {
		name   string
		argv   []string
		action string
		bad    bool
	}{
		{name: "visual wait", argv: []string{"remote", "--session", "main", "camera", "set", "--zoom", "12", "--animate", "--wait-visual"}, action: "camera.set"},
		{name: "irrelevant visual wait", argv: []string{"remote", "--session", "main", "theme", "list", "--wait-visual"}, bad: true},
		{name: "theme", argv: []string{"remote", "--session", "main", "theme", "set", "--theme", "nord"}, action: "theme.set"},
		{name: "speed", argv: []string{"remote", "--session", "main", "playback", "speed", "--ref", "1:1", "--speed", "20"}, action: "playback.speed"},
		{name: "bad speed", argv: []string{"remote", "--session", "main", "playback", "speed", "--ref", "1:1", "--speed", "7"}, bad: true},
		{name: "animation off", argv: []string{"remote", "--session", "main", "camera", "animation", "--enabled=false"}, action: "camera.animation"},
		{name: "instant", argv: []string{"remote", "--session", "main", "camera", "set", "--zoom", "12", "--animate=false"}, action: "camera.set"},
		{name: "load", argv: []string{"remote", "--session", "main", "load", "../fixtures/example.gpx", "--json"}, action: "load"},
		{name: "journey", argv: []string{"remote", "--session", "main", "journey", "create", "--start", "51,0", "--end", "52,1", "--mode", "offline", "--wait"}, action: "journey.create"},
		{name: "stop", argv: []string{"remote", "--session", "main", "journey", "stop", "add", "--journey", "1:1", "--after", "1", "--lat", "0", "--lon", "0"}, action: "journey.stop.add"},
		{name: "zero pan", argv: []string{"remote", "--session", "main", "camera", "pan", "--dx", "0", "--dy", "0"}, action: "camera.pan"},
		{name: "inspect", argv: []string{"session", "inspect", "main", "--json"}, action: "status"},
		{name: "cancel", argv: []string{"remote", "--session", "main", "operation", "cancel", "op1"}, action: "operation.cancel"},
		{name: "empty MCP target", argv: []string{"mcp", "--session="}, bad: true},
		{name: "irrelevant session flag", argv: []string{"session", "list", "--mode", "offline"}, bad: true},
		{name: "no target", argv: []string{"remote", "status"}, bad: true},
		{name: "irrelevant flag", argv: []string{"remote", "--session", "main", "camera", "fit", "--mode", "offline"}, bad: true},
		{name: "malformed ref", argv: []string{"remote", "--session", "main", "select", "--ref", "1"}, bad: true},
		{name: "missing stops", argv: []string{"remote", "--session", "main", "journey", "create", "--mode", "offline"}, bad: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, err := parseCLI(test.argv, io.Discard)
			if test.bad {
				if err == nil {
					t.Fatal("accepted invalid command")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if command.Remote.Request.Action != test.action {
				t.Fatal(command)
			}
			if test.action == "load" {
				a, err := decodeControlArgs("load", command.Remote.Request.Args)
				if err != nil || !filepath.IsAbs(a.Path) {
					t.Fatal(a, err)
				}
			}
		})
	}
}
