package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type cliCommand struct {
	Name, Path string
	Args       Args
	JSON       bool
	Frames     int
}

func parseCLI(argv []string, output io.Writer) (cliCommand, error) {
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
	var lon, lat, zoom, bearing, distance float64
	var index int
	mapMode := command.Name == "open" || command.Name == "render" || command.Name == "bench"
	if mapMode {
		a.Aspect = fs.Float64("cell-aspect", 2, "cell height/width (1..3)")
		fs.BoolVar(&a.ASCII, "ascii", false, "use ASCII rendering")
		fs.Float64Var(&lon, "center-lon", 0, "starting longitude; supply with --center-lat")
		fs.Float64Var(&lat, "center-lat", 0, "starting latitude; supply with --center-lon")
		fs.Float64Var(&zoom, "zoom", 0, "starting zoom (0..20); defaults to fitting the file or the world view")
		fs.Float64Var(&bearing, "bearing", 0, "bearing in degrees (-360..360)")
	}
	if command.Name == "open" || command.Name == "render" {
		fs.StringVar(&a.Provider, "provider", "", "basemap: mapbox, none; defaults to Mapbox when a token is configured")
	}
	if command.Name == "open" {
		fs.BoolVar(&a.NoAnimation, "no-animation", false, "disable camera transitions")
	}
	if command.Name == "render" || command.Name == "bench" {
		a.Width = fs.Int("width", 120, "map width in cells (10..300)")
		a.Height = fs.Int("height", 40, "map height in cells (5..100)")
	}
	if command.Name == "open" || command.Name == "render" || command.Name == "inspect" {
		fs.IntVar(&index, "point-index", 0, "zero-based recorded point to select, highlight or inspect")
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
		if len(paths) != 0 {
			return command, fmt.Errorf("mcp takes no file argument; see 'shoe mcp --help'")
		}
		return command, nil
	}
	if len(paths) > 1 {
		return command, fmt.Errorf("supply at most one track file (GPX, FIT, TCX or KML)")
	}
	if len(paths) == 1 {
		command.Path = paths[0]
	}
	if command.Path == "" && command.Name != "open" && command.Name != "render" {
		return command, fmt.Errorf("%s requires a track file; see 'shoe %s --help'", command.Name, command.Name)
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "point-index":
			a.PointIndex = &index
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
		}
	})
	if command.Name == "open" || command.Name == "render" {
		if a.Provider == "" {
			a.Provider = defaultProvider(accessToken())
		}
		if a.Provider != "mapbox" && a.Provider != "none" {
			return command, fmt.Errorf("unknown provider %q; choose mapbox or none", a.Provider)
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
		name, _, inline := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		option := fs.Lookup(name)
		options = append(options, arg)
		if option == nil {
			return nil, fs.Parse(options)
		}
		boolean, isBool := option.Value.(interface{ IsBoolFlag() bool })
		if !inline && (!isBool || !boolean.IsBoolFlag()) && i+1 < len(argv) {
			i++
			options = append(options, argv[i])
		}
	}
	return paths, fs.Parse(options)
}

func printCLIHelp(fs *flag.FlagSet, output io.Writer, name string) {
	switch name {
	case "open":
		fmt.Fprintln(output, "Usage: shoe [FILE] [OPTIONS]\n       shoe COMMAND [FILE] [OPTIONS]\n\nBrowse the map interactively. An optional GPX, FIT, TCX or KML file adds tracks,\nroutes and waypoints. A configured Mapbox token adds a basemap; otherwise files\nare shown on an empty canvas. 'open' is an optional alias for the viewer.\n\nCommands:\n  render   Print a static map; the track file is optional\n  summary  Print track statistics as JSON\n  inspect  Print a recorded point as JSON\n  mcp      Run the local stdio MCP server\n  bench    Benchmark synthetic map rendering")
	case "render":
		fmt.Fprintln(output, "Usage: shoe render [FILE] [OPTIONS]\n\nPrint a static map with an optional GPX, FIT, TCX or KML overlay. Use --json to\ninclude scene metadata. A configured Mapbox token adds a basemap; otherwise\nonly the file geometry is rendered.")
	case "summary":
		fmt.Fprintln(output, "Usage: shoe summary FILE\n\nPrint GPX, FIT, TCX or KML geometry and measurement statistics as JSON.")
	case "inspect":
		fmt.Fprintln(output, "Usage: shoe inspect FILE [OPTIONS]\n\nPrint a recorded point from a GPX, FIT, TCX or KML file as JSON.\nSelect by index or distance, not both. Defaults to point index 0.")
	case "bench":
		fmt.Fprintln(output, "Usage: shoe bench FILE [OPTIONS]\n\nBenchmark GPX, FIT, TCX or KML parsing and synthetic map rendering.\nOutput timings as JSON.")
	case "mcp":
		fmt.Fprintln(output, "Usage: shoe mcp\n\nRun the local MCP server over stdin/stdout. Takes no file argument.")
	}
	fmt.Fprintln(output, "\nOptions:\n  -h, --help\n        show this help")
	fs.SetOutput(output)
	fs.PrintDefaults()
	if name != "mcp" {
		fmt.Fprintln(output, "\nOptions may appear before or after FILE. Use -- before filenames that start\nwith '-' or match a command name.")
	}
	if name == "open" {
		fmt.Fprintln(output, "\nExamples:\n  shoe\n  shoe fixtures/example.gpx\n  shoe --zoom 13 fixtures/example.gpx\n\nMapbox tokens are read at runtime from MAPBOX_ACCESS_TOKEN or mapbox.local.json.\nUse 'shoe COMMAND --help' for command-specific options.")
	}
}
