# Validation

## Automated checks

From the repository root:

```sh
make test
```

This builds `bin/shoe`, runs Go tests and `go vet`, then checks the CLI and local
stdio MCP server. It covers GPX topology and missing measurements, camera/picking,
playback/profile behaviour, tile decoding and cache policy, token-free provider
errors, free-roam navigation and route transitions, tool schemas, and CLI/MCP
track-free rendering consistency. CLI checks cover default file opening, options
before/after files, scoped flags, help exit codes and benchmark dispatch. Provider
checks cover token-free geometry, missing-token warnings, runtime config and
environment precedence, and automatic Mapbox selection for MCP. No Mapbox account
is required.

Optional checks, after building:

```sh
make fixtures
make bench
python3 scripts/interactions.py
```

Interaction checks require tmux and `rsvg-convert` and run in headless Linux
pseudo-terminals. They exercise keyboard/mouse input, dialogs, playback, resizing
and terminal restoration. They don't verify physical mouse forwarding or other
operating systems. Benchmark and capture output is ignored under `reports/`.

With a locally configured token, these checks make billable Mapbox requests:

```sh
python3 scripts/mapbox_check.py
python3 scripts/interactions.py --mapbox
```

## Manual smoke check

1. Run `MAPBOX_ACCESS_TOKEN="" ./bin/shoe fixtures/example.gpx` and check separate
   parts and waypoints on an empty canvas, with no authentication warning.
2. Pan, zoom, select points and use `f` to fit the geometry.
3. Play with Space and check the map keeps its view. Use `c` to explicitly center
   and follow; change speeds, toggle elevation with `e`, and pause/seek.
4. Open help, the command palette and GPX picker; check Esc closes each dialog.
5. Resize the terminal and try ASCII mode with `a`.
6. Quit with `q` or Ctrl-C and check the terminal is restored.
7. Run `MAPBOX_ACCESS_TOKEN="" ./bin/shoe` without a GPX; check the empty canvas
   includes token setup guidance. Pan, zoom, rotate and click to inspect
   coordinates. Open a GPX with Ctrl-O, then use Ctrl-P's **Free roam (clear route)**.
8. With a token configured, run `./bin/shoe` for the world view or
   `./bin/shoe --center-lon -0.1065 --center-lat 51.505 --zoom 13` for a local
   basemap. Check tiles load while panning and no route/playback/profile is shown.
9. Try `./bin/shoe --zoom 13 fixtures/example.gpx`, then check
   `./bin/shoe --help` and `./bin/shoe inspect --help` show the relevant options.
10. Run `MAPBOX_ACCESS_TOKEN="" ./bin/shoe render fixtures/example.gpx --provider mapbox`;
    check the file is rendered and stderr explains how to configure a token.
