package main

import (
	"bytes"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestCLIAuthCommandsAndSetupHelp(t *testing.T) {
	for _, action := range []string{"login", "logout", "status"} {
		t.Run(action, func(t *testing.T) {
			command, err := parseCLI([]string{"auth", action}, io.Discard)
			if err != nil || command.Name != "auth" || command.AuthAction != action {
				t.Fatal("auth command was mistaken for a track file", err)
			}
		})
	}
	command, err := parseCLI([]string{"auth", "status", "--usage", "--json"}, io.Discard)
	if err != nil || !command.Usage || !command.JSON {
		t.Fatal("status flags were not parsed", err)
	}
	for _, args := range [][]string{{"auth", "signup"}, {"auth", "login", "--json"}, {"auth", "status", "file.gpx"}} {
		if _, err := parseCLI(args, io.Discard); err == nil {
			t.Fatal("invalid auth arguments were accepted")
		}
	}
	for _, test := range []struct{ flag, method string }{
		{flag: "--browser", method: "browser"},
		{flag: "--token-stdin", method: "token"},
	} {
		t.Run(test.method, func(t *testing.T) {
			command, err := parseCLI([]string{"auth", "login", test.flag}, io.Discard)
			if err != nil || command.AuthMethod != test.method {
				t.Fatal("explicit login method was not parsed", err)
			}
		})
	}
	if _, err := parseCLI([]string{"auth", "login", "--browser", "--token-stdin"}, io.Discard); err == nil {
		t.Fatal("conflicting authentication methods were accepted")
	}
	var help bytes.Buffer
	if _, err := parseCLI([]string{"auth", "--help"}, &help); err != flag.ErrHelp {
		t.Fatal("auth help did not return the normal help result", err)
	}
	for _, text := range []string{
		mapboxInstallURL, mapboxSignupURL, "MAPBOX_CONFIG_DIR", "Offline imports", "statistics", "failed localhost callback",
	} {
		if !strings.Contains(help.String(), text) {
			t.Fatal("auth help omitted setup/account requirements", text)
		}
	}
}

func TestCLIViewerAcceptsOptionalFileAndInterspersedFlags(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	for _, test := range []struct {
		name string
		argv []string
		path string
	}{
		{"no file", []string{"--ascii", "--zoom", "13"}, ""},
		{"file first", []string{"fixtures/example.gpx", "--ascii", "--zoom", "13"}, "fixtures/example.gpx"},
		{"flags first", []string{"--ascii", "--zoom", "13", "fixtures/example.gpx"}, "fixtures/example.gpx"},
		{"mixed", []string{"--zoom", "13", "fixtures/example.gpx", "--ascii"}, "fixtures/example.gpx"},
		{"inline value", []string{"--zoom=13", "fixtures/example.gpx", "--ascii"}, "fixtures/example.gpx"},
		{"open alias", []string{"open", "--ascii", "fixtures/example.gpx", "--zoom", "13"}, "fixtures/example.gpx"},
		{"command filename", []string{"--ascii", "--zoom", "13", "--", "render"}, "render"},
		{"leading dash filename", []string{"--ascii", "--zoom", "13", "--", "-route.gpx"}, "-route.gpx"},
		{"spaces", []string{"--ascii", "route with spaces.gpx", "--zoom", "13"}, "route with spaces.gpx"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, err := parseCLI(test.argv, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if command.Name != "open" || command.Path != test.path || command.Args.Provider != "none" || !command.Args.ASCII || command.Args.Zoom == nil || *command.Args.Zoom != 13 {
				t.Fatalf("incorrect viewer request: %+v", command)
			}
		})
	}
	command, err := parseCLI([]string{"--", "summary"}, io.Discard)
	if err != nil || command.Name != "open" || command.Path != "summary" {
		t.Fatal("a filename matching a command was not treated literally", command, err)
	}
}

func TestCLICameraValuesAndProviderDefaults(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	command, err := parseCLI(nil, io.Discard)
	if err != nil || command.Name != "open" || command.Path != "" || command.Args.Provider != "none" || command.Args.Zoom != nil {
		t.Fatal("token-free invocation is not an empty canvas", command, err)
	}
	command, err = parseCLI([]string{
		"--center-lon", "-0.1",
		"fixtures/example.gpx", "--center-lat=51.5",
		"--bearing", "-45",
		"--ascii", "--no-animation",
		"--theme", "tokyo-night",
		"--zoom", "12", "--zoom=13",
		"--point-index", "0",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	a := command.Args
	fileOrProviderChanged := command.Path != "fixtures/example.gpx" || a.Provider != "none"
	cameraFlagsLost := a.Lon == nil || *a.Lon != -0.1 ||
		a.Lat == nil || *a.Lat != 51.5 ||
		a.Bearing == nil || *a.Bearing != -45 ||
		a.Zoom == nil || *a.Zoom != 13
	viewerFlagsLost := !a.ASCII || !a.NoAnimation || a.Theme != "tokyo-night"
	pointSelectorLost := a.PointIndex == nil || *a.PointIndex != 0
	if fileOrProviderChanged || cameraFlagsLost || viewerFlagsLost || pointSelectorLost {
		t.Fatal("flags were mistaken for input files or explicit values were lost", command)
	}
	t.Setenv("MAPBOX_ACCESS_TOKEN", "synthetic-test-token")
	command, err = parseCLI([]string{"fixtures/example.gpx"}, io.Discard)
	if err != nil || command.Args.Provider != "mapbox" {
		t.Fatal("configured token did not choose Mapbox", command, err)
	}
	command, err = parseCLI([]string{"fixtures/example.gpx", "--no-provider"}, io.Discard)
	if err != nil || command.Args.Provider != "none" || !command.Args.ProviderSet {
		t.Fatal("no-provider did not disable the configured basemap", command, err)
	}
	command, err = parseCLI([]string{"render", "--no-provider=false"}, io.Discard)
	if err != nil || command.Args.ProviderSet || command.Args.Provider != "mapbox" {
		t.Fatal("no-provider=false disabled the configured basemap", command, err)
	}
}

func TestCLICommandsAcceptFlagsAroundFiles(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	render, err := parseCLI([]string{"render", "--json", "--width", "80", "fixtures/example.gpx", "--height=20", "--point-index", "3"}, io.Discard)
	if err != nil || render.Name != "render" || render.Path != "fixtures/example.gpx" || !render.JSON || *render.Args.Width != 80 || *render.Args.Height != 20 || *render.Args.PointIndex != 3 {
		t.Fatal("static rendering arguments were not preserved", render, err)
	}
	inspect, err := parseCLI([]string{"inspect", "--distance-m", "1000", "fixtures/example.gpx"}, io.Discard)
	if err != nil || inspect.Name != "inspect" || inspect.Args.PointIndex != nil || inspect.Args.Distance == nil || *inspect.Args.Distance != 1000 {
		t.Fatal("distance inspection acquired an unwanted index selector", inspect, err)
	}
	inspect, err = parseCLI([]string{"inspect", "fixtures/example.gpx"}, io.Discard)
	if err != nil || inspect.Args.PointIndex == nil || *inspect.Args.PointIndex != 0 {
		t.Fatal("inspection no longer defaults to the first recorded point", inspect, err)
	}
	bench, err := parseCLI([]string{"bench", "--frames", "2", "fixtures/example.gpx", "--width", "80"}, io.Discard)
	if err != nil || bench.Name != "bench" || bench.Frames != 2 || *bench.Args.Width != 80 {
		t.Fatal("benchmark arguments were not preserved", bench, err)
	}
	for _, name := range []string{"summary", "mcp"} {
		argv := []string{name}
		if name == "summary" {
			argv = append(argv, "fixtures/example.gpx")
		}
		command, err := parseCLI(argv, io.Discard)
		if err != nil || command.Name != name {
			t.Fatal("command was mistaken for a viewer input", command, err)
		}
	}
}

func TestCLIRejectsIrrelevantFlagsAndInvalidInputCounts(t *testing.T) {
	for _, argv := range [][]string{
		{"--demo"}, {"--provider", "demo"}, {"render", "--provider", "demo"},
		{"--json"}, {"--frames", "1"}, {"--width", "80"}, {"--distance-m", "100"},
		{"summary", "fixtures/example.gpx", "--demo"},
		{"inspect", "fixtures/example.gpx", "--zoom", "3"},
		{"render", "--no-animation"}, {"render", "--frames", "2"},
		{"render", "--theme", "tokyo-night"}, {"mcp", "--theme", "catppuccin"},
		{"bench", "fixtures/example.gpx", "--provider", "mapbox"},
		{"bench", "fixtures/example.gpx", "--no-provider"},
		{"summary", "fixtures/example.gpx", "--no-provider"},
		{"mcp", "--demo"}, {"mcp", "fixtures/example.gpx"},
		{"summary", "fixtures/example.gpx", "fixtures/waypoints.gpx"},
		{"fixtures/example.gpx", "fixtures/waypoints.gpx", "--point-index", "0"},
		{"render", "a.gpx", "b.gpx", "--item-id", "1"},
		{"summary", "a.gpx", "--items", "--item-id", "1"},
		{"inspect", "a.gpx", "--item-id", "0"},
		{"--zoom"}, {"--zoom", "oops"},
		{"--unknown"}, {"summary"}, {"inspect"}, {"bench"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			if _, err := parseCLI(argv, io.Discard); err == nil {
				t.Fatal("accepted invalid CLI arguments")
			}
		})
	}
}

func TestCLIHelpIsAvailableWithoutInputOrCredentials(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	for _, argv := range [][]string{{"--help"}, {"-h"}, {"open", "--help"}, {"render", "--help"}, {"summary", "--help"}, {"inspect", "--help"}, {"bench", "--help"}, {"mcp", "--help"}} {
		var output bytes.Buffer
		command, err := parseCLI(argv, &output)
		if err != flag.ErrHelp || !strings.Contains(output.String(), "Usage: mapbox-tui") {
			t.Fatal("help tried to execute the command", argv, err)
		}
		help := output.String()
		if command.Name == "open" || command.Name == "render" {
			if !strings.Contains(help, "-no-provider") || strings.Contains(help, "  -provider ") {
				t.Fatal("help did not prefer the explicit offline toggle")
			}
		}
		viewerHelpInvalid := command.Name == "open" &&
			(!strings.Contains(help, "mapbox-tui [FILE...]") ||
				!strings.Contains(help, "Commands:") ||
				strings.Contains(help, "-frames") ||
				strings.Contains(help, "-json") ||
				strings.Contains(help, "-demo"))
		if viewerHelpInvalid {
			t.Fatal("viewer help did not describe the default action and its options")
		}
		if command.Name == "inspect" && (!strings.Contains(help, "-distance-m") || strings.Contains(help, "-zoom")) {
			t.Fatal("inspection help included unrelated map options")
		}
		if command.Name == "render" && (!strings.Contains(help, "-json") || !strings.Contains(help, "-width") || strings.Contains(help, "-no-animation")) {
			t.Fatal("render help did not list static rendering options")
		}
	}
}

func TestCLIMultipleFilesAndItemSelectors(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	for _, mode := range []string{"open", "render"} {
		cmd, err := parseCLI([]string{mode, "a.gpx", "--ascii", "b.tcx"}, io.Discard)
		multipleFilesLost := err != nil || len(cmd.Paths) != 2 || cmd.Paths[1] != "b.tcx" || !cmd.Args.ASCII
		if multipleFilesLost {
			t.Fatal("multiple files were not retained", cmd, err)
		}
	}
	for _, mode := range []string{"open", "render", "summary", "inspect"} {
		cmd, err := parseCLI([]string{mode, "--item-id", "3", "a.gpx"}, io.Discard)
		itemSelectorLost := err != nil || cmd.Args.ItemID == nil || *cmd.Args.ItemID != 3
		if itemSelectorLost {
			t.Fatal("item selection was not retained", cmd, err)
		}
	}
}

func TestSafeStartPreservesStartupContentRules(t *testing.T) {
	for _, test := range []struct {
		name  string
		flags []string
		valid bool
	}{
		{"empty", nil, true},
		{"import", []string{"ride.gpx"}, true},
		{"journey", []string{"--start", "51,0", "--end", "52,1"}, true},
		{"workspace", []string{"--workspace", "saved.json"}, true},
		{"workspace and import", []string{"--workspace", "saved.json", "ride.gpx"}, false},
		{"workspace and journey", []string{"--workspace", "saved.json", "--start", "51,0", "--end", "52,1"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, err := parseCLI(append([]string{"--safe-start"}, test.flags...), io.Discard)
			if (err == nil) != test.valid {
				t.Fatalf("unexpected startup validation: %v", err)
			}
			if test.valid && !command.Args.SafeStart {
				t.Fatal("lost safe-start flag")
			}
		})
	}
}
