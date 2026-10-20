# Mapbox TUI

[![CI](https://github.com/adamcbrewer/mapbox-tui/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/adamcbrewer/mapbox-tui/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/adamcbrewer/mapbox-tui?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/github/license/adamcbrewer/mapbox-tui)](LICENSE)
[![Builds: Linux, macOS and Windows](https://img.shields.io/badge/builds-Linux%20%7C%20macOS%20%7C%20Windows-blue)](#platforms)

A terminal map viewer and journey planner for GPX, FIT, TCX and KML files,
with a JSON CLI and local MCP server. Mapbox adds basemaps, place search and road
routing; local files and straight-line journeys work offline.

[Getting started](#getting-started) · [Usage](#usage) ·
[Agent usage](docs/AGENT_USAGE.md) ·
[Themes](#themes) · [Other terminal maps](#other-terminal-map-projects)

![Mapbox TUI showing the Country ride near London, with an elevation profile](docs/images/mapbox-tui.png)


<div align="center">
  <video src="https://github.com/user-attachments/assets/09033b01-8f17-49f3-bc45-1ec23bb64d66" width="100%" controls></video>
</div>

The demo shows place search, activity playback with an elevation profile, theme
switching, journey planning and copying a command to reopen the journey.

## Features

- **Activity inspection and playback.** Inspect recorded points and distances,
  view elevation profiles and replay tracks at different speeds. Playback uses
  recorded timing where available and labelled estimates otherwise.
- **Multiple files.** Load activities and planned routes together, select or hide
  individual items, and compare elevation profiles on a shared distance axis.
- **Journey planning.** Pick stops on the map and reorder waypoints. Straight-line
  plans work offline; Mapbox provides place search and walking, cycling and driving
  routes.
- **Favourite places.** Save commonly used locations locally, then find, rename
  or remove them. Reuse saved places offline in quick search or as journey stops.
  See [favourites controls](docs/USAGE.md#favourite-places).
- **Shareable journey commands.** Copy stops, travel mode and imported file paths
  into a command to reopen the plan. Road routes recalculate; preferences aren't
  included.
- **Saved workspaces.** Manually save and reopen files, journeys and unfinished
  drafts. Search, sort, rename, duplicate or delete saves. Original import files
  required; no autosave or automatic session/crash recovery.
- **Remembered preferences.** Keep theme, ASCII, animation, elevation and last
  centre/zoom across launches. [Safe start](docs/USAGE.md#saved-viewer-preferences)
  bypasses saved preferences; [Start clean](docs/USAGE.md#saved-workspaces) empties
  the working workspace without deleting saves.
- **CLI and MCP.** Get JSON summaries, inspect points by distance and render
  ANSI-free text maps. The local MCP server keeps loaded files, selection and
  visibility in its own workspace.
- **Remote control through CLI and MCP.** Let an agent control a running map:
  move the camera, edit journeys, manage imports, set playback speed and themes,
  or save and open workspaces. Discover viewers by session name or UUID.
  See [remote access](docs/REMOTE_ACCESS.md).
- **Offline file support.** Import GPX, FIT, TCX and KML files, view elevation and
  replay recordings without an account. Connected features include Mapbox
  basemaps, search, road routing and an optional traffic overlay in the viewer.
- **Rendering and controls.** Braille and ASCII rendering, full keyboard and mouse
  navigation, map rotation, six built-in colour palettes and custom themes.

## Getting started

For prebuilt Linux x86-64 binaries, see [GitHub Releases](https://github.com/adamcbrewer/mapbox-tui/releases)
 and the [installation instructions](docs/INSTALL.md). Until the first release is
published, build from source below.

You'll need Go 1.27.1 or newer (see [go.mod](go.mod)). The Linux/macOS example
below uses Make; see [Platforms](#platforms) for a Windows build.

```sh
git clone https://github.com/adamcbrewer/mapbox-tui.git
cd mapbox-tui
make build && ./bin/mapbox-tui fixtures/country-ride.gpx --no-provider
```

That opens an activity without any Mapbox account setup. Swap in your own
file, or run `./bin/mapbox-tui` to explore without one. Press `?` for controls
and `q` to quit.

On Linux x86-64, `make install` puts the binary in `~/.local/bin`.
For basemaps, place search and road routing, [connect Mapbox](#mapbox).
Before authenticating, [review billing risks](#free-usage-and-billing) and set up
usage alerts. Monitoring usage and charges is your responsibility.

## Usage

**Using it yourself?** See [Usage](docs/USAGE.md) for keyboard shortcuts, CLI
commands and saved workspaces.

**Using an agent?** See [Agent usage](docs/AGENT_USAGE.md) for MCP setup, live
viewer control and example prompts.

## Mapbox

You'll need a Mapbox account for online maps, place search and road routing.
Mapbox offers product-specific free allowances. See the
[signup guide](https://docs.mapbox.com/accounts/guides/signup/) and the billing guidance below.

You can either sign up directly through the [Mapbox Console](https://console.mapbox.com/)
 or during a [Mapbox MCP server connection](https://docs.mapbox.com/accounts/guides/signup/#sign-up-with-mapbox-mcp-server-connection).

If you install the optional [official Mapbox CLI](https://github.com/mapbox/mapbox-cli#install) you can then sign in via:

```sh
./bin/mapbox-tui auth login
./bin/mapbox-tui auth status
```

The viewer, CLI and MCP reuse that login and your saved billing-risk acknowledgement.
CLI login and in-app login present the same confirmation before enabling online features.

You can also supply a public token through `MAPBOX_ACCESS_TOKEN` for scripts. `--no-provider` disables the basemap, even having authed;
local file inspection and manual journey planning work without Mapbox.

Mapbox requests use your account and may incur charges. See the
[Mapbox notes](docs/MAPBOX.md) for configuration, usage, caching and
attribution.

## Free usage and billing

Mapbox TUI aims to support personal use within Mapbox's free allowances, but free
use is not guaranteed. Online features, background refreshes and bugs can make
billable requests. Use online features at your own discretion: you are responsible
for monitoring your account and any charges. The app does not enforce a spending cap.

Check the [current pricing](https://www.mapbox.com/pricing/) for each product and
[set up usage alerts](https://console.mapbox.com/account/settings/usage-thresholds/)
before continuing online. Alerts send notifications; they do not stop requests or
charges. Maps and traffic, search sessions, geocoding and routing can use separate
allowances, and some products may have no free allowance.

Mapbox settings shows current-month usage for products used by this app, with a
**Usage details** view and a link to your
[account usage](https://console.mapbox.com/account/statistics/). These are
account-wide figures for the named saved-login account, including other applications.
An environment token used for maps may belong to a different account.
Statistics can be delayed or
unavailable; they are not an invoice or a remaining free allowance.

Choose **Continue offline** in the confirmation, or **Go offline** in Mapbox
settings, to disable online maps, traffic, place lookup and routing. Coordinates,
local favourites and imported files remain usable. This choice persists until
you choose **Go online** or successfully sign in again. Failed or cancelled login
keeps your previous offline choice. An authenticated account still makes one account-usage
check at viewer startup, including in offline mode.

See [Mapbox billing and usage guidance](docs/MAPBOX.md#free-usage-and-billing)
for recommendations, statistics limitations and unattended CLI/MCP setup.

## Other terminal map projects

There are some other great TUI projects out there which I drew inspiration from, but feature support and active development made me decide to take a fresh approach.
The main idea is Mapbox TUI would bring activity files, journey planning and CLI/MCP inspection together, using the features of Mapbox's ecosystem to drive a Google Maps sort of experience.

| Feature | Mapbox TUI | [MapSCII](https://github.com/rastapasta/mapscii#readme) | [ttymap](https://github.com/Kohei-Wada/ttymap#readme) | [TerminalMap](https://github.com/psmux/TerminalMap#readme) |
|---|---|---|---|---|
| Interactive terminal map | ✓ | ✓ | ✓ | ✓ |
| Braille rendering | ✓ | ✓ | ✓ | ✓ |
| Keyboard and mouse navigation | ✓ | ✓ | ✓ | ✓ |
| GPX, FIT, TCX and KML imports | ✓ | - | - | - |
| Activity elevation profiles | ✓ | - | - | - |
| Estimated elevation along planned routes | (proposed) | - | - | - |
| Recorded activity playback | ✓ | - | - | - |
| Visual comparison of imported routes | ✓ | - | - | - |
| Choose colours for individual imported activities | (proposed) | - | - | - |
| Journey planning with waypoints | ✓ | - | - | - |
| Saved favourite places / bookmarks | ✓ (offline reuse) | - | [Saved views via bookmark plugin](https://github.com/Kohei-Wada/ttymap#bundled-plugins) | - |
| Walking, cycling and driving routes | Mapbox | - | - | - |
| Copy a command to recreate a journey | ✓ | - | - | - |
| Save and reopen workspaces | ✓ (manual; original imports required) | - | - | - |
| Automatically save UI preferences and last camera view | ✓ | - | - | - |
| Automatic session/crash recovery | Deferred | - | - | - |
| Headless map rendering | ANSI-free text via CLI/MCP | - | ANSI snapshots via CLI | ANSI frames via Rust SDK |
| JSON activity summaries and point inspection | ✓ | - | - | - |
| Local MCP server with a session workspace | ✓ | - | - | - |
| Remote access to a running viewer through CLI and MCP | [✓](docs/REMOTE_ACCESS.md) | - | - | - |
| Lua plugins and scripted scenes | - | - | ✓ | - |
| Embeddable map SDK | - | - | - | Rust SDK |
| Multiple independent map views | - | - | - | Via SDK |
| Custom vector tile servers | - | ✓ | - | ✓ |
| Offline activity file inspection | ✓ | - | - | - |
| Offline basemaps | - | Local vector tiles / MBTiles | MBTiles/PMTiles proposed | Bundled zoom 0 and 1 tiles |
| Persistent disk tile cache | (in-memory only) | ✓ | ✓ | ✓ |

This is just a comparison of documented workflows, checked **14 October 2026**, not a feature audit.
Favourites and persistence/recovery rows checked **20 October 2026**, including ttymap's
[configuration](https://github.com/Kohei-Wada/ttymap/blob/main/docs/configuration.md)
and [bookmark implementation](https://github.com/Kohei-Wada/ttymap/blob/main/runtime/lua/plugin/bookmark.lua).
`-` means unsupported or not documented. Named view bookmarks aren't full workspace
saves or automatic last-view restoration. Other rows retain their check dates above.

## Themes

Choose **Colour theme** from the Ctrl-K command palette to preview a palette,
or start with `--theme synthwave`. Click a screenshot to see it at full size.
See [Terminal themes](docs/THEMES.md) for all six built-in palettes and custom themes. New ones can easily be added.


<table>
  <tr>
    <td width="50%" align="center">
      <strong>Catppuccin</strong><br>
      <a href="docs/images/theme-catppuccin.png"><img src="docs/images/theme-catppuccin.png" alt="Country ride map in the dark Catppuccin theme" width="100%"></a>
    </td>
    <td width="50%" align="center">
      <strong>Synthwave</strong><br>
      <a href="docs/images/theme-synthwave.png"><img src="docs/images/theme-synthwave.png" alt="Country ride map in the purple and cyan Synthwave theme" width="100%"></a>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <strong>Paper</strong><br>
      <a href="docs/images/theme-paper.png"><img src="docs/images/theme-paper.png" alt="Country ride map in the light Paper theme" width="100%"></a>
    </td>
    <td width="50%" align="center">
      <strong>Noir</strong><br>
      <a href="docs/images/theme-noir.png"><img src="docs/images/theme-noir.png" alt="Country ride map in the black and green Noir theme" width="100%"></a>
    </td>
  </tr>
</table>

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
- [Agent usage](docs/AGENT_USAGE.md)
- [Remote access reference](docs/REMOTE_ACCESS.md)
- [Mapbox](docs/MAPBOX.md)
- [Development](docs/DEVELOPMENT.md)
- [Release reference for maintainers](docs/RELEASING.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Themes](docs/THEMES.md)
- [Go style](docs/GO_STYLE.md)
- [Contributing](CONTRIBUTING.md)

[MIT licence](LICENSE). [Third-party notices](THIRD_PARTY_NOTICES.txt).
