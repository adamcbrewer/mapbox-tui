package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type cliCommand struct {
	Remote        *remoteOptions
	Session       string
	Name, Path    string
	Paths         []string
	Args          Args
	JSON          bool
	Items         bool
	Frames        int
	AuthAction    string
	Usage         bool
	AcceptBilling bool
}

func parseCLI(argv []string, output io.Writer) (cliCommand, error) {
	if len(argv) > 0 && (argv[0] == "remote" || argv[0] == "session") {
		return parseControlCLI(argv, output)
	}
	if len(argv) > 0 && argv[0] == "--version" {
		if len(argv) != 1 {
			return cliCommand{}, fmt.Errorf("--version takes no other arguments")
		}
		return cliCommand{Name: "version"}, nil
	}
	if len(argv) > 0 && argv[0] == "auth" {
		return parseAuthCLI(argv[1:], output)
	}
	command := cliCommand{Name: "open", Frames: 100}
	if len(argv) > 0 {
		switch argv[0] {
		case "open", "render", "summary", "inspect", "bench", "mcp":
			command.Name, argv = argv[0], argv[1:]
		}
	}
	fs := flag.NewFlagSet(command.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	a := &command.Args
	if command.Name == "mcp" {
		fs.StringVar(&command.Session, "session", "", "attach to a live viewer UUID or name")
	}
	var lon, lat, zoom, bearing, distance float64
	var index, itemID int
	var noProvider bool
	var journeyStart, journeyEnd string
	journeyModeName := "driving"
	var journeyWaypoints []string
	var journeyRequested bool
	mapMode := command.Name == "open" || command.Name == "render" || command.Name == "bench"
	if mapMode {
		a.Aspect = fs.Float64("cell-aspect", 2, "cell height/width (1..3)")
		fs.BoolVar(&a.ASCII, "ascii", false, "use ASCII rendering")
		fs.Float64Var(&lon, "center-lon", 0, "starting longitude; supply with --center-lat")
		fs.Float64Var(&lat, "center-lat", 0, "starting latitude; supply with --center-lon")
		fs.Float64Var(
			&zoom,
			"zoom",
			0,
			"starting zoom (-8..20); negative values fit wide extents in small viewports",
		)
		fs.Float64Var(&bearing, "bearing", 0, "bearing in degrees (-360..360)")
	}
	if command.Name == "open" || command.Name == "render" {
		fs.BoolVar(&noProvider, "no-provider", false, "disable the Mapbox basemap (search and traffic are separate)")
		fs.StringVar(&a.Provider, "provider", "", "legacy basemap selector; use --no-provider to hide the basemap")
	}
	if command.Name == "open" {
		fs.StringVar(&a.SessionName, "name", "", "human-readable live session alias")
		fs.BoolVar(&a.NoControl, "no-control", false, "disable local session discovery and control")
		fs.StringVar(&a.WorkspacePath, "workspace", "", "open a saved workspace JSON file")
		fs.BoolVar(&a.ResetView, "reset-view", false, "delete saved centre and zoom before opening the viewer")
		fs.StringVar(&journeyStart, "start", "", "journey start as latitude,longitude; requires --end")
		fs.StringVar(&journeyEnd, "end", "", "journey destination as latitude,longitude; requires --start")
		fs.StringVar(&journeyModeName, "mode", "driving", "journey mode: offline, walking, cycling or driving")
		fs.Func("waypoint", "journey waypoint as latitude,longitude; repeat in travel order", func(value string) error {
			journeyWaypoints = append(journeyWaypoints, value)
			journeyRequested = true
			return nil
		})
		fs.Func("file", "load a track file; repeat for multiple imports (or use positional files)", func(value string) error {
			if value == "" {
				return fmt.Errorf("file path must not be empty")
			}
			return nil
		})
		fs.StringVar(&a.Elevation, "elevation", "auto", "elevation profiles: auto, show or hide (launch only)")
		fs.BoolVar(&a.NoAnimation, "no-animation", false, "disable camera transitions")
		fs.StringVar(
			&a.Theme,
			"theme",
			"catppuccin",
			"colour theme: catppuccin, noir, nord, paper, "+
				"synthwave, tokyo-night, or a TOML file path",
		)
	}
	if command.Name == "render" || command.Name == "bench" {
		a.Width = fs.Int("width", 120, "map width in cells (10..300)")
		a.Height = fs.Int("height", 40, "map height in cells (5..100)")
	}
	if command.Name == "open" || command.Name == "render" || command.Name == "inspect" {
		fs.IntVar(&index, "point-index", 0, "zero-based recorded point to select, highlight or inspect")
	}
	switch command.Name {
	case "open", "render", "inspect", "summary":
		fs.IntVar(
			&itemID,
			"item-id",
			0,
			"select a file's item; discover IDs with summary --items",
		)
	}
	if command.Name == "summary" {
		fs.BoolVar(
			&command.Items,
			"items",
			false,
			"list the file's items and IDs",
		)
	}
	if command.Name == "inspect" {
		fs.Float64Var(&distance, "distance-m", 0, "inspect the nearest recorded point at this distance in metres")
	}
	if command.Name == "render" {
		fs.BoolVar(&command.JSON, "json", false, "output map text and scene metadata as JSON")
	}
	if command.Name == "bench" {
		fs.IntVar(&command.Frames, "frames", 100, "number of synthetic rendering frames")
	}
	paths, err := parseOptions(fs, argv)
	if err != nil {
		if err == flag.ErrHelp {
			printCLIHelp(fs, output, command.Name)
		}
		return command, err
	}
	if command.Name == "mcp" {
		var attached bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "session" {
				attached = true
			}
		})
		if attached && command.Session == "" {
			return command, fmt.Errorf("--session requires a live UUID or name")
		}
		if len(paths) != 0 {
			return command, fmt.Errorf("mcp takes no file argument; see 'mapbox-tui mcp --help'")
		}
		return command, nil
	}
	multipleFiles := len(paths) > 1
	workspaceCommand := command.Name == "open" || command.Name == "render"
	if multipleFiles && !workspaceCommand {
		return command, fmt.Errorf("supply at most one track file (GPX, FIT, TCX or KML)")
	}
	command.Paths = paths
	if len(paths) > 0 {
		command.Path = paths[0]
	}
	if command.Path == "" && !workspaceCommand {
		return command, fmt.Errorf("%s requires a track file; see 'mapbox-tui %s --help'", command.Name, command.Name)
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "start", "end", "mode":
			journeyRequested = true
		case "point-index":
			a.PointIndex = &index
		case "item-id":
			a.ItemID = &itemID
		case "distance-m":
			a.Distance = &distance
		case "center-lon":
			a.Lon = &lon
		case "center-lat":
			a.Lat = &lat
		case "zoom":
			a.Zoom = &zoom
		case "bearing":
			a.Bearing = &bearing
		case "elevation":
			a.ElevationSet = true
		case "ascii":
			a.ASCIISet = true
		case "no-animation":
			a.NoAnimationSet = true
		case "theme":
			a.ThemeSet = true
		case "provider":
			a.ProviderSet = true
		}
	})
	if _, err := parseElevationMode(a.Elevation); err != nil || (a.ElevationSet && a.Elevation == "") {
		return command, fmt.Errorf("elevation must be auto, show or hide")
	}
	if a.NoControl && a.SessionName != "" {
		return command, fmt.Errorf("--name cannot be combined with --no-control")
	}
	if journeyRequested {
		journey, err := parseStartupJourney(journeyStart, journeyEnd, journeyWaypoints, journeyModeName)
		if err != nil {
			return command, err
		}
		a.Journey = journey
	}
	if a.ItemID != nil && *a.ItemID < 1 {
		return command, fmt.Errorf("item-id must be positive")
	}
	hasSelector := a.PointIndex != nil || a.ItemID != nil
	if a.WorkspacePath != "" && (len(paths) > 0 || a.Journey != nil || hasSelector) {
		return command, fmt.Errorf("--workspace cannot be combined with import files, journey options or item/point selectors")
	}
	if multipleFiles && hasSelector {
		return command, fmt.Errorf("point-index and item-id require a single file")
	}
	if command.Items && a.ItemID != nil {
		return command, fmt.Errorf("choose --items or --item-id")
	}
	if command.Name == "open" || command.Name == "render" {
		if noProvider {
			a.Provider, a.ProviderSet = "none", true
		}
		if a.Provider == "" {
			a.Provider = defaultProvider(accessToken())
		}
		if a.Provider != "mapbox" && a.Provider != "none" {
			return command, fmt.Errorf("unknown provider; Mapbox is the default basemap; use --no-provider to disable it")
		}
	}
	if command.Name == "inspect" && a.PointIndex == nil && a.Distance == nil {
		a.PointIndex = &index
	}
	return command, nil
}

func parseOptions(fs *flag.FlagSet, argv []string) ([]string, error) {
	var options, paths []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			paths = append(paths, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			paths = append(paths, arg)
			continue
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		option := fs.Lookup(name)
		options = append(options, arg)
		if option == nil {
			return nil, fs.Parse(options)
		}
		boolean, isBool := option.Value.(interface{ IsBoolFlag() bool })
		if !inline && (!isBool || !boolean.IsBoolFlag()) && i+1 < len(argv) {
			i++
			options = append(options, argv[i])
			value = argv[i]
		}
		if name == "file" {
			paths = append(paths, value)
		}
	}
	return paths, fs.Parse(options)
}

func printCLIHelp(fs *flag.FlagSet, output io.Writer, name string) {
	switch name {
	case "open":
		fmt.Fprintln(output, "Usage: mapbox-tui [FILE...] [OPTIONS]\n"+
			"       mapbox-tui COMMAND [FILE] [OPTIONS]\n"+
			"       mapbox-tui --version\n\n"+
			"Browse maps and load GPX, FIT, TCX or KML files into a workspace.\n"+
			"Ctrl-L lists loaded files and their routes. Opening a file adds it; reopening\n"+
			"a loaded path selects the existing import. 'open' is an optional alias.\n"+
			"A configured Mapbox token adds a basemap; otherwise files use an empty canvas.\n\n"+
			"Commands:\n"+
			"  render   Print a static map; accepts zero or more files\n"+
			"  summary  Print track statistics or list items as JSON\n"+
			"  inspect  Print a recorded point as JSON\n"+
			"  mcp      Run the local stdio MCP server (or attach with --session)\n"+
			"  session  List or inspect running viewer sessions\n"+
			"  remote   Control a running viewer session\n"+
			"  auth     Mapbox login, logout and status (optional Mapbox CLI)\n"+
			"  bench    Benchmark synthetic map rendering")
	case "render":
		fmt.Fprintln(output, "Usage: mapbox-tui render [FILE...] [OPTIONS]\n\n"+
			"Print a static map with GPX, FIT, TCX or KML overlays. Multiple files fit all\n"+
			"visible items. Use --json for scene metadata. Item/point selectors require one\n"+
			"file. Mapbox needs your account's public token, supplied through a saved CLI\n"+
			"login or MAPBOX_ACCESS_TOKEN. Offline: --no-provider. Setup: mapbox-tui auth --help.")
	case "summary":
		fmt.Fprintln(output, "Usage: mapbox-tui summary FILE [OPTIONS]\n\n"+
			"Print GPX, FIT, TCX or KML statistics as JSON. Use --items to list item IDs,\n"+
			"or --item-id to summarise one item.")
	case "inspect":
		fmt.Fprintln(output, "Usage: mapbox-tui inspect FILE [OPTIONS]\n\n"+
			"Print a recorded point from a GPX, FIT, TCX or KML file as JSON.\n"+
			"Select by index or distance, not both. Defaults to point index 0.")
	case "bench":
		fmt.Fprintln(output, "Usage: mapbox-tui bench FILE [OPTIONS]\n\n"+
			"Benchmark GPX, FIT, TCX or KML parsing and synthetic map rendering.\nOutput timings as JSON.")
	case "mcp":
		fmt.Fprintln(output, "Usage: mapbox-tui mcp\n\n"+
			"Run the local MCP server over stdin/stdout. Takes no file argument.\n"+
			"Discover live viewers with viewer_sessions and target tools by session_id.\n"+
			"Local imports/tools need no account. Mapbox renders use an environment token\n"+
			"or saved optional-CLI login; browser login never runs here. Setup: mapbox-tui auth --help.")
	}
	fmt.Fprintln(output, "\nOptions:\n  -h, --help\n        show this help")
	helpFlags := flag.NewFlagSet(name, flag.ContinueOnError)
	helpFlags.SetOutput(output)
	fs.VisitAll(func(option *flag.Flag) {
		if option.Name == "provider" {
			return
		}
		helpFlags.Var(option.Value, option.Name, option.Usage)
		helpFlags.Lookup(option.Name).DefValue = option.DefValue
	})
	helpFlags.PrintDefaults()
	if name != "mcp" {
		fmt.Fprintln(output, "\nOptions may appear before or after FILE. Use -- before filenames that start\nwith '-' or match a command name.")
	}
	if name == "open" {
		fmt.Fprintln(output, "\nExamples:\n  mapbox-tui\n  mapbox-tui fixtures/example.gpx\n"+
			"  mapbox-tui --zoom 13 fixtures/example.gpx\n\n"+
			"Offline files/manual journeys need no account. Mapbox maps/search require your\n"+
			"account's public token. 'mapbox-tui auth login' uses the optional Mapbox CLI to sign\n"+
			"in or create an account; login credentials live in ~/.mapbox (MAPBOX_CONFIG_DIR\n"+
			"overrides it). MAPBOX_ACCESS_TOKEN overrides login; empty disables discovery.\n"+
			"Use 'mapbox-tui auth --help' for setup or 'mapbox-tui COMMAND --help' for specific options.")
	}
}

func parseAuthCLI(argv []string, output io.Writer) (cliCommand, error) {
	command := cliCommand{Name: "auth"}
	if len(argv) == 0 || argv[0] == "--help" || argv[0] == "-h" {
		printAuthHelp(output)
		return command, flag.ErrHelp
	}
	command.AuthAction, argv = argv[0], argv[1:]
	switch command.AuthAction {
	case "login", "logout", "status", "online", "offline":
	default:
		return command, fmt.Errorf("choose 'mapbox-tui auth login', 'logout', 'status', 'online' or 'offline'; see 'mapbox-tui auth --help'")
	}
	fs := flag.NewFlagSet("auth "+command.AuthAction, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if command.AuthAction == "login" || command.AuthAction == "online" {
		fs.BoolVar(&command.AcceptBilling, "accept-billing", false, "acknowledge billing risks and enable online features")
	}
	if command.AuthAction == "status" {
		fs.BoolVar(&command.JSON, "json", false, "output the status report as token-free JSON")
		fs.BoolVar(&command.Usage, "usage", false, "include current-month account/active-token product statistics")
	}
	paths, err := parseOptions(fs, argv)
	if err == flag.ErrHelp {
		printAuthHelp(output)
		return command, err
	}
	if err != nil || len(paths) != 0 {
		return command, fmt.Errorf("invalid auth arguments; see 'mapbox-tui auth --help'")
	}
	return command, nil
}

func printAuthHelp(output io.Writer) {
	fmt.Fprintln(output, "Usage: mapbox-tui auth login [--accept-billing]\n       mapbox-tui auth logout\n"+
		"       mapbox-tui auth status [--json] [--usage]\n       mapbox-tui auth online [--accept-billing]\n       mapbox-tui auth offline\n\n"+
		"Login opens Mapbox's browser flow to sign in or create an account. The optional\n"+
		"Mapbox CLI owns OAuth, saved credentials and refresh. Credentials live in\n"+
		"~/.mapbox/credentials.json for the shared default profile; MAPBOX_CONFIG_DIR\n"+
		"can relocate them. Login/logout/status report the resolved path on your machine.\n"+
		"Logout removes that shared login; it doesn't revoke public or environment tokens.\n\n"+
		"Offline imports, measurements, playback and offline journeys need no account.\n"+
		"Mapbox maps and place search need your account's public token. A configured\n"+
		"MAPBOX_ACCESS_TOKEN works without the CLI; otherwise mapbox-tui retrieves a public\n"+
		"token through the saved login. An empty variable disables automatic discovery.\n\n"+
		"Install the optional Mapbox CLI (tested with v0.3.0): "+mapboxInstallURL+"\n"+
		"mapbox-tui finds mapbox on PATH; MAPBOX_TUI_CLI can select its executable explicitly.\n"+
		"Create an account/email verification: "+mapboxSignupURL+"\n"+
		"Current pricing/trial limits: https://www.mapbox.com/pricing/\n\n"+
		"Online use requires billing-risk acknowledgement. Login and auth online always show\n"+
		"the guidance and prompt in a terminal if acceptance is missing. --accept-billing\n"+
		"records acceptance without a prompt. Unattended\n"+
		"render/MCP use can set MAPBOX_TUI_ACCEPT_BILLING=1 after reviewing: "+billingDocsURL+"\n"+
		"auth offline persists offline mode; authenticated accounts still check usage at viewer startup.\n\n"+
		"The official CLI requests general-purpose account permissions, including write\n"+
		"scopes mapbox-tui doesn't use; v0.3.0 cannot narrow them. Status probes Streets only when online and acknowledged, with a\n"+
		"tile request. Usage requires statistics access, may lag 24 hours, and may include\n"+
		"other apps using your token. --help shows this help.")
}
