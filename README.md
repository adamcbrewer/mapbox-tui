# Mapbox TUI

[![CI](https://github.com/adamcbrewer/mapbox-tui/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/adamcbrewer/mapbox-tui/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/adamcbrewer/mapbox-tui?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/github/license/adamcbrewer/mapbox-tui)](LICENSE)
[![Builds: Linux, macOS and Windows](https://img.shields.io/badge/builds-Linux%20%7C%20macOS%20%7C%20Windows-blue)](#platforms)

A terminal map viewer and journey planner for GPX, FIT, TCX and KML files,
with optional Mapbox maps and a local MCP server. Built with Go and Bubble Tea.

Open an activity, compare routes, replay a recording or let your agent inspect
it. Imported files work offline.

## Getting started

You'll need Go 1.27.1 or newer (see [go.mod](go.mod)). The Linux/macOS example
below uses Make; see [Platforms](#platforms) for a Windows build.

```sh
git clone https://github.com/adamcbrewer/mapbox-tui.git
cd mapbox-tui
make build && ./bin/mapbox-tui fixtures/example.gpx --no-provider
```

That opens a synthetic activity without any account setup. Swap in your own
file, or run `./bin/mapbox-tui` to explore without one. Press `?` for controls
and `q` to quit.

For a longer, real-world example, open the bundled [Country ride](fixtures/country-ride.gpx):

```sh
./bin/mapbox-tui fixtures/country-ride.gpx
```

This recorded ride retains 181.09 km, 29,005 points and elevation data. Geometry
within 2 km of either original endpoint has been removed, along with timestamps
and identifying metadata. The remaining route still shows the area travelled.
Playback uses estimated timing. It works offline, or uses your connected Mapbox
account for the basemap; add `--no-provider` to stay offline.

On Linux x86-64, `make install` puts the binary in `~/.local/bin`.
For basemaps, place search and road routing, [connect Mapbox](#mapbox).

## Platforms

The Go binary cross-compiles for Linux, macOS and Windows on x86-64 and ARM64.
CI runs the tests and terminal interaction checks on Linux. Native macOS and
Windows runtime behaviour has not yet been verified.

The viewer needs an interactive terminal with ANSI/VT support. The optional
[Mapbox CLI](https://github.com/mapbox/mapbox-cli#download-the-archive-yourself)
publishes binaries for all three platforms. Imported files work without it.

On Windows, build and run directly with Go from the repository root in PowerShell:

```powershell
go build -trimpath -o bin/mapbox-tui.exe ./go
.\bin\mapbox-tui.exe fixtures/example.gpx --no-provider
```

`make install` and `make package` currently require Linux x86-64. On macOS and
Windows, build from source and put the binary on your `PATH` yourself.

The development watcher, terminal interaction checks and shell-based auth test
helpers need a Unix-like environment. The viewer's **Copy journey to clipboard**
command uses POSIX shell quoting; adapt copied commands for PowerShell or Command
Prompt.

## Use it with your agent

MCP lets your agent inspect files, compare activities and render text maps.
Loaded files, selection and visibility carry over between requests.

Add the binary as a local stdio server in your agent's MCP configuration:

```json
{
  "mcpServers": {
    "mapbox-tui": {
      "command": "/absolute/path/to/mapbox-tui/bin/mapbox-tui",
      "args": ["mcp"]
    }
  }
}
```

Use your actual repository path. If your client uses a different config format,
the command and argument are the same.

### Try it

Once connected, give your agent the absolute path to the repo and try:

> Open fixtures/example.gpx with mapbox-tui. What's in it? Summarise the
> recorded activity separately from the planned route and waypoints.

> Find the recorded point nearest 1 km, then show me an offline ASCII map
> of the activity at 60 by 20 cells.

> Also load fixtures/waypoints.gpx. Hide Optional spur, keep the waypoints
> visible and render the workspace. Then show the spur again and redraw it.

Those examples use synthetic fixtures and work offline. For a recorded ride, try:

> Open fixtures/country-ride.gpx. Summarise its distance and elevation, inspect
> the point nearest 90 km, then render an offline map of the whole ride.

MCP has its own workspace, separate from an open viewer. An agent with terminal
control can also use the viewer's journey planner, playback and live traffic.

See [MCP notes](docs/MCP.md) for workspace rendering and measurement details.
The [tool schemas](shared/tools.json) describe the available calls.

## Terminal and CLI

```sh
./bin/mapbox-tui ride.gpx course.tcx
./bin/mapbox-tui summary fixtures/example.gpx
./bin/mapbox-tui inspect fixtures/example.gpx --point-index 3
./bin/mapbox-tui render fixtures/example.gpx --no-provider --json
```

Use `fixtures/country-ride.gpx` in the same CLI commands to try the longer ride.

In the viewer, Ctrl-O opens files, Ctrl-L manages loaded items, `/` searches
for places and Ctrl-J opens the journey planner. Ctrl-K opens the command
palette from any panel or dialog; Esc returns to the previous view.

`summary` and `inspect` return JSON. `render` gives you a text map; add `--json`
for scene metadata too. CLI and MCP maps are ANSI-free.

Use `./bin/mapbox-tui --help` or a subcommand's `--help` for options.
[Usage notes](docs/USAGE.md) cover controls, imports and playback.

## Mapbox

You'll need a Mapbox account for online maps, place search and road routing.
You can start without a credit card, with generous initial usage limits for
trying the app. See Mapbox's [signup guide](https://docs.mapbox.com/accounts/guides/signup/)
for current trial limits and what changes when you add a payment method.
You can sign up and sign in through the [Mapbox Console](https://console.mapbox.com/)
or during a [Mapbox MCP server connection](https://docs.mapbox.com/accounts/guides/signup/#sign-up-with-mapbox-mcp-server-connection).
Local files and manual journey planning work without an account.

Install the optional [official Mapbox CLI](https://github.com/mapbox/mapbox-cli#install),
then sign in once:

```sh
./bin/mapbox-tui auth login
./bin/mapbox-tui auth status
```

The viewer, CLI and MCP reuse that login. You can also supply a public token
through `MAPBOX_ACCESS_TOKEN` for scripts. `--no-provider` disables the basemap;
local file inspection and manual journey planning work without Mapbox.

Mapbox requests use your account and may incur charges. See the
[Mapbox notes](docs/MAPBOX.md) for configuration, usage, caching and
attribution.

## Contributing

Contributions are welcome, especially docs improvements, bug reports and feature
requests. Try the setup or MCP examples and send a small PR for anything unclear,
outdated or missing.

[Open an issue](https://github.com/adamcbrewer/mapbox-tui/issues) to report a bug
or suggest a feature. Include steps to reproduce the bug or explain what you'd
like to do. See [Contributing](CONTRIBUTING.md) for more details.

## Development

You'll also need Python 3.11 or newer. Use `make dev` to rebuild on changes and
`make check` to run the offline checks. See [development notes](docs/DEVELOPMENT.md) for the
local workflow and terminal prerequisites.

## Further reading and other docs

- [Usage](docs/USAGE.md)
- [MCP](docs/MCP.md)
- [Mapbox](docs/MAPBOX.md)
- [Development](docs/DEVELOPMENT.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Themes](docs/THEMES.md)
- [Go style](docs/GO_STYLE.md)
- [Contributing](CONTRIBUTING.md)

[MIT licence](LICENSE). [Third-party notices](THIRD_PARTY_NOTICES.txt).
