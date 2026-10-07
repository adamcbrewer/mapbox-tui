# Shell on Earth

A personal proof of concept for browsing maps and viewing GPX, FIT, TCX and KML
files in a terminal. Built with Go, Bubble Tea, Bubbles and Lip Gloss.
The executable is `shoe`.

- Pan, zoom and inspect tracks, routes and waypoints.
- Free roam around the map without loading a file.
- Show an elevation profile and replay recorded positions at adjustable speeds.
- View imported geometry on an empty canvas, or add Mapbox Streets vector tiles.
- Get structured results through the CLI or a local MCP server.

The bundled GPX fixtures are synthetic. The viewer doesn't provide directions,
traffic data or live GPS navigation.

## Quick start

Requires Go **1.27.1** and Make. Run from the repository root:

```sh
make build
./bin/shoe fixtures/example.gpx
```

File viewing needs no account or token. A configured Mapbox token adds a real
basemap automatically. Run `./bin/shoe` without a file to free roam; without a
token, this starts an empty canvas with setup guidance. Use Ctrl-O to open the
file picker and `q` to quit.
If you manage Go with mise, prefix Make commands with `mise exec go@1.27.1 --`.

Braille rendering works best in a Unicode terminal. Use `--ascii` for a text
fallback. The minimum terminal size is 30 columns by 12 rows.

## Supported file formats

The viewer, CLI commands and MCP `load_track` share the same import support:

| Format | Imported data |
|---|---|
| GPX | Tracks, routes and waypoints |
| FIT | Activity/course GPS records, elevation, timestamps and course-point waypoints |
| TCX | Activity tracks/laps, courses and course-point waypoints, with elevation and timestamps |
| KML | Placemarks, line strings, polygon outlines, nested multi-geometries and Google `gx:Track` / `gx:MultiTrack` timestamps |

Open these files in the usual way, for example `./bin/shoe ride.fit`,
`./bin/shoe summary ride.tcx` or `./bin/shoe render map.kml --json`.
XML formats are detected by their root element; FIT is detected by its binary
header. The picker accepts lowercase and uppercase extensions.

KML accepts whitespace around coordinate commas, including Garmin's KML 2.1
exports. FIT validates every file's full checksum, including chained files and
Zwift exports with an omitted optional header checksum. Truncated files and
checksum mismatches are rejected.

Separate tracks, TCX laps, timer pauses and missing GPS positions remain separate
parts. Distances exclude those gaps. Missing elevation/timestamps stay unavailable;
geometry without complete timestamps uses the existing estimated-speed playback.
FIT/TCX imports use GPS geometry, not device-reported distance or sensor metrics.
KML altitude values are retained as stored, without terrain/altitude-mode conversion.
KML styles, overlays and network links aren't loaded. Export plain `.kml` rather
than compressed `.kmz`.

FIT decoding uses the BSD-licensed [FIT SDK for Go](https://github.com/muktihari/fit),
without a proprietary SDK dependency. Garmin's
[FIT protocol licence](https://www.thisisant.com/developer/ant/licensing/flexible-and-interoperable-data-transfer-fit-protocol-license)
applies to the format. TCX and KML use Go's built-in XML parser.

## Mapbox basemap

Only real basemap tiles need a Mapbox account and public access token. Imported
geometry, measurements and playback work without Mapbox authentication.

### Runtime configuration

Tokens are read when the viewer or MCP server starts, **not compiled into the
binary**. Changing credentials needs no rebuild. Supply your own public access
token through `MAPBOX_ACCESS_TOKEN`, or copy the placeholder in
`mapbox.example.json` into an ignored `mapbox.local.json` in the current working
directory and replace it locally. The environment variable takes precedence;
an explicitly empty value disables the local token. `.env` files aren't loaded
automatically.

```sh
MAPBOX_ACCESS_TOKEN="pk.your-public-token" ./bin/shoe
./bin/shoe
./bin/shoe --center-lon -0.1065 --center-lat 51.505 --zoom 13
./bin/shoe fixtures/example.gpx --provider mapbox
./bin/shoe fixtures/example.gpx --provider none
```

Free roam starts at longitude/latitude 0 and zoom 0. Supply both center flags and
`--zoom` to start somewhere specific. Pan, zoom, rotate and click to inspect
coordinates. Route playback and elevation controls become available after opening
a track file. Use **Free roam (clear route)** in the Ctrl-P command palette to
remove the route and keep browsing the current location.

The viewer, CLI `render` and MCP `render_map` use Mapbox automatically when a
token is configured. Otherwise they display only imported geometry on an empty
canvas. `--provider none` (MCP: `provider: "none"`) disables the basemap even when
a token is available.

Opening a track file without credentials is quiet. An explicit Mapbox request
without a token, or a file-free launch without a basemap, shows token setup guidance
and keeps the viewer/file usable. The viewer shows this in its status line; plain
CLI renders warn on stderr, while JSON/MCP renders include it in `scene.status`.

Mapbox requests use your account and may incur charges. Provider errors leave the
file inspectable. See [provider notes](docs/PROVIDERS.md) for caching and attribution
requirements, including the unresolved graphical-logo requirement.

Credentials, personal GPX/FIT/TCX/KML files, binaries and generated reports are
ignored by Git. Only the four explicitly allowed synthetic GPX fixtures are tracked.

### Which commands need Mapbox authentication?

| Command / feature | Mapbox authentication | Without credentials |
|---|---|---|
| `shoe FILE` or `shoe open FILE` | Optional, for the basemap | View imported tracks, routes and waypoints on an empty canvas |
| File inspection, elevation profile and playback | No | Fully available for the loaded geometry/measurements |
| `shoe` / file-free browsing | Required for real maps | Empty canvas with a setup hint; Ctrl-O loads a track file |
| `shoe render [FILE]` | Optional, for the basemap | Render only the file geometry; no file produces an empty map with a setup hint |
| `shoe summary FILE` | No | Local geometry and measurement statistics as JSON |
| `shoe inspect FILE` | No | Local recorded-point inspection as JSON |
| `shoe bench FILE` | No | Local synthetic rendering benchmark |
| `shoe mcp` | No to start the server | Local tools work; real basemap rendering needs a token in the server's environment/config |

`--provider mapbox` selects the credential-dependent service. Camera and display
options (`--center-lon`, `--center-lat`, `--zoom`, `--bearing`, `--ascii`,
`--cell-aspect`), file selection and `--point-index` do not require authentication.
The [MCP tool table](#mcp) lists the same distinction for each service operation.

## Controls

| Action | Keyboard | Mouse |
|---|---|---|
| Open another track file | Ctrl-O or `o` | |
| Command palette / help | Ctrl-P / `?` | |
| Pan | Arrows or `h j k l` | Left-drag |
| Zoom | `+` / `-` | Wheel, anchored to pointer |
| Zoom at 2× speed | | Ctrl / Cmd + wheel |
| Fit geometry / reset world view | `f` / `r` | |
| Select recorded points | `[` / `]` | Click near a track |
| Seek along the track | PgUp/PgDn, Home/End | |
| Inspect selected position | Enter | |
| Play / pause | Space | |
| Change playback speed | `p` picker, `s` / `S` cycle | |
| Center and follow | `c` | |
| Show / hide elevation | `e` | |
| Focus route details | Tab | Wheel over sidebar |
| Rotate / north-up | `,` / `.` / `n` | Right-drag |
| Toggle ASCII / camera animation | `a` / `m` | |
| Provider credits | `v` | |
| Close dialog / quit | Esc / `q` or Ctrl-C | |

`S` and `E` mark part endpoints, `W` marks waypoints, and `@` marks the selected
or interpolated position. Pan and zoom keep playback running; selecting a point
pauses it. Manual pan disables follow mode, and `c` restores it.

Starting, resuming or restarting playback keeps the current camera view. Follow
mode is off when a file is loaded; press `c` (or **Center and follow position** in
the command palette) to explicitly center on the current point and enable follow.

Ctrl + wheel doubles zoom speed. Cmd works when the terminal reports that
modifier; standard terminal mouse reporting usually only carries Ctrl, Alt and
Shift, so use Ctrl if Cmd isn't forwarded.

Playback speeds are 1x, 5x, 20x, 60x and 120x. Parts with complete, increasing
timestamps use recorded timing. Other parts use a labelled **6 m/s estimate**.
Disconnected parts jump without inventing a connecting path. Interpolated values
are prefixed with `~`; missing elevations and timestamps remain unavailable.

## CLI

The interactive viewer is the default action. Supply an optional GPX, FIT, TCX or
KML file to add tracks, routes and waypoints; no file starts free roam. `open` is
an optional alias.

```sh
./bin/shoe
./bin/shoe fixtures/example.gpx
./bin/shoe --zoom 13 fixtures/example.gpx
./bin/shoe --help
```

Options may appear before or after the file. Use `--` before filenames that start
with `-` or match a command name, such as `./bin/shoe -- render`.

Subcommands select a different operation:

| Command | File | Result |
|---|---|---|
| `render` | Optional | Static map text, or JSON with `--json` |
| `summary` | Required | Geometry and measurement statistics as JSON |
| `inspect` | Required | Recorded point as JSON |
| `mcp` | None | Local stdio MCP server |
| `bench` | Required | Synthetic rendering benchmark timings as JSON |

```sh
./bin/shoe render --center-lon -0.1065 --center-lat 51.505 --zoom 13 --json
./bin/shoe summary fixtures/example.gpx
./bin/shoe inspect fixtures/example.gpx --point-index 3
./bin/shoe inspect fixtures/example.gpx --distance-m 1000
./bin/shoe render fixtures/example.gpx --width 120 --height 40
./bin/shoe render fixtures/example.gpx --json
./bin/shoe render --help
```

`summary` and `inspect` return JSON. `render` returns map text with attribution;
`render --json` returns plain map text and scene metadata, without ANSI sequences.
Indices are zero-based, distances are metres, and bounds are
`[west, south, east, north]`. Distance inspection returns the nearest recorded
point, not an interpolated measurement.

Camera options for the viewer, rendering and benchmarks: `--center-lon` and
`--center-lat` together, `--zoom` (0-20), `--bearing` (-360 to 360),
`--cell-aspect` (1-3, default 2), and `--ascii`.

The viewer and `render` accept `--provider mapbox|none` for the basemap, plus
`--point-index` to select or highlight a recorded point. `--no-animation` is
viewer-only. `--width` (10-300) and `--height` (5-100) size static maps and
benchmarks; the viewer uses the terminal size. `--json` belongs to `render`,
`--distance-m` to `inspect`, and `--frames` to `bench`. Benchmarks use internal
synthetic geometry, independent of credentials. Run `shoe COMMAND --help` for
that command's options.

## MCP

```sh
./bin/shoe mcp
```

The local stdio server exposes four tools:

| Tool | Inputs | Result | Mapbox authentication |
|---|---|---|---|
| `load_track` | Local `path` | Ephemeral track ID, bounds and summary | No |
| `summarize_track` | `track_id` | Geometry and measurement summary | No |
| `inspect_track` | `track_id` and either `point_index` or `distance_m` | Recorded point | No |
| `render_map` | Optional `track_id` and viewport/provider options | Plain map text and scene metadata | Optional; required for real basemap tiles |

Use [mcp.example.json](mcp.example.json) with the absolute path to your binary.
Full schemas are in [shared/tools.json](shared/tools.json). Track IDs and tile
caches last only for that server process. MCP runs independently of the viewer.
Its render default is Mapbox when the server has a configured token, and `none`
otherwise. Set `provider: "none"` to disable tile requests. For a server launched
outside the repository, supply the token through its environment; the client's
credentials aren't automatically passed to the server.

Omit `track_id` to render just the basemap. Without a token this returns an empty
map with setup guidance in `scene.status`. `point_index` requires a track. Camera
defaults match the viewer's free-roam world view.

## Development and checks

Python 3 is required for the development and validation scripts:

```sh
make dev
make dev ARGS='fixtures/example.gpx --zoom 13'
make test
make fixtures
make bench
```

The development watcher rebuilds and restarts the viewer when Go/build inputs
change. It requires a Unix-like interactive terminal; view state resets on restart.

`make test` builds, runs Go tests and vet, then checks CLI/MCP consistency without
Mapbox credentials. Benchmarks and captures are generated locally under ignored
`reports/`. See [validation notes](docs/VALIDATION.md) for interaction and live
provider checks.

Dependencies are pinned in `go.mod` with checksums in `go.sum`. Setup follows the
dependency-minimisation and deterministic-install principles in this
[supply-chain reference](https://github.com/lirantal/npm-security-best-practices).

## Limitations

- Antimeridian-crossing geometry and bounds wider than 180 degrees are rejected.
- Display uses Mercator projection; distances use spherical Haversine calculations.
- Map labels are ASCII-only. Sensor metrics and GPX activity extensions aren't imported.
- Basemap detail is capped at tile zoom 14, even when the viewer zooms further.
- Distance includes each imported track and route, excluding gaps between parts.
- Terminal integration checks cover headless Linux/tmux; other environments aren't verified.
