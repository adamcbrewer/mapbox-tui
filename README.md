# Mapbox TUI

A community-built terminal map explorer and journey planner powered by Mapbox,
written in Go.

Built with Bubble Tea, Bubbles and Lip Gloss. Imports GPX, FIT, TCX and KML files.
The executable is `mapbox-tui`.

- Pan, zoom and inspect multiple tracks, routes and waypoint collections.
- Free roam around the map without loading a file.
- Find a place with `/` quick search without changing a journey or selected activity.
- Plan an A-to-B journey with editable waypoints, offline plotting and optional Mapbox search/road routing.
- Show an elevation profile and replay recorded positions at adjustable speeds.
- View imported geometry on an empty canvas, or add Mapbox Streets vector tiles.
- Toggle live Mapbox traffic within 20 miles of the map centre.
- Switch terminal colour themes or load a custom TOML palette.
- Get structured results through the CLI or a local MCP server.

The bundled GPX fixtures are synthetic. The viewer doesn't provide live GPS
navigation or voice guidance.

## Quick start

Building requires Go **1.27.1** and Make. Development checks use Python 3.
Run from the repository root:

```sh
make build
./bin/mapbox-tui fixtures/example.gpx
```

File viewing needs no account or token. A configured Mapbox token adds a real
basemap automatically. Run `./bin/mapbox-tui` without a file to free roam; without a
token, this starts an empty canvas with setup guidance. Use Ctrl-O to open the
file picker, Ctrl-L to manage loaded files and `q` to quit.
If you manage Go with mise, prefix Make commands with `mise exec go@1.27.1 --`.

Menus and pickers wrap: Up on the first item selects the last, and Down on the
last selects the first. This includes filtered Commands and location-search results.

Esc returns to the previous dialog when you open a nested menu or help page,
preserving its selection and command filter. At the top level, Esc closes the
dialog and returns to the map. Nested dialogs show **esc back**; completing an
action such as opening a file or applying playback speed returns straight to the map.
Nested popups show dimmed back frames, each one cell above and left of the active
dialog. Only their top/left edges peek out; different dialog sizes don't expose
old content at the bottom-right. The stack stays within the terminal bounds.

### Build, install and package

`mapbox-tui` is a standalone executable. Builds, installations and release archives
don't download or bundle Mapbox CLI. Install that optional tool separately for
browser login, automatic rendering-token retrieval and account usage reports.

```sh
make install                              # ~/.local/bin/mapbox-tui
make install PREFIX=/your/install/prefix
make package                              # dist/mapbox-tui-linux-amd64.tar.gz
make mapbox-check                         # optional installed CLI's offline schemas
```

`DESTDIR` supports staged installs. `make package` produces an archive containing
only `mapbox-tui` for the build target. Linux x86-64 is verified in this environment.
Use the [official Mapbox release instructions](https://github.com/mapbox/mapbox-cli#install-a-released-binary)
for the optional CLI; v0.3.0 is tested. The official installer checks the archive's
SHA-256 and supports `MAPBOX_CLI_VERSION` for versioned installation. Follow
versioned acquisition and reviewed-update principles from the
[supply-chain reference](https://github.com/lirantal/npm-security-best-practices).

Install Mapbox CLI by following the
[official installation documentation](https://github.com/mapbox/mapbox-cli#install-a-released-binary).
The separate **Mapbox Tilesets CLI** offered by its installer is for publishing
datasets; `mapbox-tui` doesn't require it. After installation:

```sh
mapbox --version
./bin/mapbox-tui auth status
```

If status reports a saved usable login, it is ready to use; otherwise run
`./bin/mapbox-tui auth login` and sign into your existing account. Installation doesn't
remove the login already stored by Mapbox CLI.

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

Open these files in the usual way, for example `./bin/mapbox-tui ride.fit`,
`./bin/mapbox-tui summary ride.tcx` or `./bin/mapbox-tui render map.kml --json`.
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

Real basemap tiles and optional journey place search use a Mapbox account and
public access token. Imported geometry, measurements, playback and manual journey
coordinates work without Mapbox authentication.

See the [existing-account journey walkthrough](docs/MAPBOX_JOURNEYS.md) for
standalone-binary and local-development setup, login, viewing, usage and logout.

### Runtime configuration

The recommended setup is the optional official Mapbox CLI plus your own Mapbox
account. Install the CLI from the link above, make `mapbox` available on `PATH`,
then run:

```sh
./bin/mapbox-tui auth login
./bin/mapbox-tui auth status
./bin/mapbox-tui auth status --usage --json
```

Login opens Mapbox's browser flow to sign in **or create an account**. Signup,
email verification, passwords, MFA and billing stay on Mapbox's website. See
[account creation and card-free trial details](https://docs.mapbox.com/accounts/guides/signup/)
and [current pricing](https://www.mapbox.com/pricing/); trial limits and standard
free allowances differ. If email verification exceeds the browser callback's
five-minute window, finish verification and run login again.

Mapbox CLI owns the persistent OAuth login and refresh. The shared `default`
profile lives at `~/.mapbox/credentials.json`; `MAPBOX_CONFIG_DIR` relocates its
store. **Login, logout and status print the resolved path on your machine**;
JSON status includes `credential_file`. The CLI uses owner-only Unix credential
files and plain JSON rather than an OS keychain. `mapbox-tui` uses the CLI's documented
JSON interface, retrieves the default public rendering token and holds it in
memory. It neither reads the CLI's private files nor saves another token copy.
The saved login works from any directory, without files or auth keys in this repo.

`MAPBOX_TUI_CLI=/absolute/path/to/mapbox` selects an executable outside `PATH`.
The adapter checks its JSON/schema contract and gives install/update guidance if
the CLI is missing or incompatible. v0.3.0 requests general-purpose account
permissions, including write scopes `mapbox-tui` doesn't use; that release has no
scope-selection option. Account token retrieval needs `tokens:read`; usage
needs `statistics:read` and account access to the Statistics API.

`mapbox-tui auth status` starts with a system check of `PATH` or the explicit
`MAPBOX_TUI_CLI` selection. It reports the executable used, validates its version
and command schema, and links official installation instructions when missing/unusable.
JSON includes `cli.checked`, `cli.path` and `cli.install_url`. A missing CLI
means the saved login couldn't be inspected, rather than that you are logged out.

For scripts/CI, `MAPBOX_ACCESS_TOKEN` can directly supply a public token without
Mapbox CLI. It overrides the saved login; an explicitly empty value disables all
automatic credential discovery and first-run setup. Tokens are runtime-only,
**never compiled into the binary**. `.env` and repo-local Mapbox JSON files
aren't loaded. Restart an existing viewer/MCP process to rediscover credentials,
or use the viewer's **Mapbox account setup** action.

```sh
MAPBOX_ACCESS_TOKEN="pk.your-public-token" ./bin/mapbox-tui
MAPBOX_ACCESS_TOKEN='' ./bin/mapbox-tui     # deliberately offline
./bin/mapbox-tui
./bin/mapbox-tui --center-lon -0.1065 --center-lat 51.505 --zoom 13
./bin/mapbox-tui fixtures/example.gpx --no-provider
```

Free roam starts at longitude/latitude 0 and zoom 0. Supply both center flags and
`--zoom` to start somewhere specific. Pan, zoom, rotate and click to inspect
coordinates. Route playback applies to the active item; elevation shows all visible
activities consecutively on a shared chart.
Use **Free roam (clear selection)** in the Ctrl-K command palette to stop playback
and keep browsing with loaded geometry still visible. **Clear workspace (remove
all files)** removes the imports while preserving the current camera view.

### Multiple files and routes

Opening another file adds it to the workspace, selects its first item containing
track points (or a waypoint-only item) and fits the camera to that item. Reopening
the same absolute path selects the existing import and restores its visibility;
it does not reread changed file contents. Remove it first to import it again.
Camera flags supplied at startup apply after all initial files load; later
interactive imports use the usual fit-new-item behaviour.

Press **Ctrl-L** for loaded files and their individual routes, activities or
placemarks. File IDs distinguish equally named files; the selected row also shows
its source path. `*` marks the active item and `[x]` means visible.

- Enter selects an item or activates a file. A hidden selection is revealed.
- Space shows/hides the selected item or whole file.
- Delete/Backspace removes the selected row. `D` removes its entire file.
- Arrow keys, `j`/`k`, Home/End or the mouse wheel navigate; Esc closes the dialog.

Only the active item plays back. Its colour, name and item reference identify the
playback controls, progress bar and moving `@` markers on the map and elevation chart.
Hiding or removing it stops playback and clears selection without selecting another item.
Selecting a different item starts a fresh paused position. Showing an item alone
does not select it. Removing a file's last item also removes its file entry.

The elevation chart places visible activities end to end in file-load order, then
item order within each file. Each activity occupies its own distance section on an
axis showing the summed distance, with a blank column separating activities and a
shared elevation scale. Missing elevation leaves its distance section blank; gaps
aren't connected. Items with no elevation are labelled in the colour key. The chart's
`@` distance includes earlier activities; route details and playback remain item-local.
Hiding/removing an activity updates the chart's axes; changing selection keeps the
same scale. Free roam retains visible profiles, and `e` toggles the whole chart.

**`f` fits all visible geometry**, including standalone waypoints. Click near a
visible route or waypoint to select its owning item. Each item keeps a colour across
the map, elevation chart and Ctrl-L list, even when selection or visibility changes.
The six-colour palette cycles for larger workspaces. A colour key below the map
shows item references and names; `*` identifies the selected item. Active routes use
`#` and other routes use `*` in ASCII mode. The active item wins overlaps;
clicking an occupied cell selects its displayed item. Elsewhere, nearest geometry
wins, with later-loaded items winning equal-distance picking ties. Selection markers
take priority over route geometry and map labels.

Activity reordering and journey-wide playback are tracked separately in
[issue #3](https://github.com/adamcbrewer/mapbox-tui/issues/3).

### Quick place search

Press **`/`** from the map, or choose **Quick place search** in Commands. Type a
place name with Mapbox connected, or enter **latitude, longitude** offline.
Autocomplete starts after three characters and a short pause; Up/Down chooses a
result, Enter frames roughly a one-mile radius around the place, and Esc cancels.
**Ctrl-S** retries a connected lookup.
At projection edges, the frame shifts inward to avoid blank map areas. It resets
north-up only when the current bearing would otherwise hide the chosen place.

Quick search keeps the selected activity, playback and every journey intact.
Accepting a location disables camera follow so the view stays at the chosen place;
playback continues and the searched coordinates remain in the details. Press `c`
to return to the activity's position. Use **Ctrl-J** to create or edit a journey instead.

### Journey planner

Press **Ctrl-J** or choose **Journey Planner** in the command palette. The planner
docks beside the live map at 70 columns or wider, and uses a compact strip above
the map on smaller terminals. Locations, travel modes and feedback stay in that
panel. Added locations and resolved routes automatically fit the map, leaving
roughly a one-mile radius of context around each stop so short plans aren't overly
zoomed in. Longer journeys still fit the whole route. Mouse pan/zoom remains
available while editing; **Tab** in the main planner switches keyboard focus
between the map and planner.

A new journey starts with editable start and destination fields. Use the same
place search or manual coordinate entry for either endpoint and intermediate waypoints.

Use Up/Down and Enter to choose/edit the start, destination or an intermediate
waypoint. Enter **latitude, longitude** (for example `51.5, -0.1`) to set a location
offline, or use **Alt-M** in the editor to use the current map centre.
You can also click a location row and then click its point on the map, including
start, destination and intermediate waypoints. A simple click places the selected
location; dragging pans without changing it. Planned endpoints use **A** (start)
and **B** (destination), matching the planner; stop details have space between them.
All stop markers stay at your chosen coordinates, even when Mapbox snaps the
route's endpoints to nearby roads or paths.
With a configured Mapbox token or saved login, address/named-POI search is enabled
automatically, without a confirmation on each launch. Autocomplete starts after
three characters and a short pause; **Ctrl-S** retries the current search.
Up/Down selects a suggestion and Enter resolves it. Mapbox receives the search text
and map-centre bias. Search uses the existing token independently of the basemap
setting, and may incur account charges. No new provider account or SDK is required.

| Planner action | Control |
|---|---|
| Add a waypoint before the destination | Ctrl-N or the Add waypoint row |
| Reverse the entire journey | Ctrl-R or Reverse journey |
| Undo the last location/order/mode edit | Ctrl-Z or Undo edit |
| Clear an endpoint / remove a waypoint | Delete or Backspace |
| Reorder intermediate waypoints | Alt-Up / Alt-Down |
| Choose Manual, Walking, Cycling or Driving; see the current choice in the list | Ctrl-B or Travel mode |
| Recalculate or retry a routed plan | Ctrl-D or Recalculate route |
| Read full routing feedback, account usage and limits | Ctrl-U or Routing feedback / usage |
| Show and fit the journey on the map | Select Show journey on map, then Enter |
| Switch keyboard focus between the map and planner | Tab in the main planner |
| Return from a location editor / close the planner | Esc; Ctrl-J also closes the main planner |

Reversal changes `A → W1 → W2 → B` to `B → W2 → W1 → A`; one undo restores the
whole order. Locations remain editable, and stale lookup results cannot overwrite
a newer edit. Cancelled/failed lookups leave the plan and imported items usable.

**Driving** is the default when Mapbox is connected. Routes calculate automatically
once all locations are set, including intermediate waypoints. The planner shows
the current choice directly in **Travel mode: Driving** (or Walking, Cycling, Manual).
There is no separate initial calculate step; **Recalculate route** refreshes/retries
the result. **Show journey on map** waits for a current road/path route, then closes
the panel and fits that resolved geometry. Routing failures keep the planner open
with feedback.

Without credentials, the default is **Manual**: the map connects resolved consecutive
locations with straight lines and reports plotted distance. An unresolved waypoint
leaves a gap. You can explicitly choose Manual even with Mapbox connected; that
choice is preserved through credential discovery and reopening the planner.

**Walking, Cycling and Driving** use Mapbox Directions with the same selected token
as maps/search, independently of basemap visibility. Mapbox
receives the ordered stop coordinates; each calculation uses one account request
and may be billable. See current [account/trial limits](https://docs.mapbox.com/accounts/guides/signup/)
and [pricing](https://www.mapbox.com/pricing#directions-api).

Routed plans show the road/path, provider distance and **estimated travel time**.
Your chosen locations stay separate from snapped road/path coordinates, which are
listed in the journey details. Completing a location edit, adding/removing/reordering
waypoints, reversal or undo recalculates an enabled routed plan. Typing, panning,
redrawing and reopening the planner make no Directions calls. Incomplete plans wait
for every location to be set. Reversal requests new directions rather than reversing
the old line, respecting directional road/access rules.
Accepting an unchanged location or choosing the current travel mode keeps the
resolved route without another request; use **Ctrl-D** to explicitly refresh it.

Mapbox accepts 2–25 locations, with total between-stop distance limits of 1,000 km
walking, 1,500 km cycling and 10,000 km driving. Over-limit plans show the cause and
keep every stop; they are never split or truncated. **Ctrl-U** opens the full,
scrollable feedback when a notice is clipped in a narrow terminal. Rate limiting
honours the provider's retry delay, with no automatic retry loop; use **Ctrl-D**
after the wait. Failed or stale results leave a clearly labelled manual preview,
with outdated routed estimates hidden. Choose **Manual** at any time for offline
plotting. Imported items remain usable through routing failures.

Planned geometry has no invented timestamps/elevation and is separate from recorded playback and
elevation profiles. It appears in Ctrl-L alongside imports with stable identity
and colour, and shares the scene renderer. Ctrl-J reopens the selected or most
recent plan; removing it or clearing the workspace discards it.

Plans and search results are **session-only**. Favorites and persistent/offline
geolocation caching are tracked in [issue #10](https://github.com/adamcbrewer/mapbox-tui/issues/10).
Road/path routing is implemented for [issue #12](https://github.com/adamcbrewer/mapbox-tui/issues/12).
Estimated route elevation remains in [issue #13](https://github.com/adamcbrewer/mapbox-tui/issues/13).
See [provider notes](docs/PROVIDERS.md#journey-location-lookups) for request and
storage policies and [route-planning notes](docs/ROUTE_PLANNING.md) for API research.

The viewer, CLI `render` and MCP `render_map` use Mapbox automatically when a
token is configured. Otherwise they display only imported geometry on an empty
canvas. `--no-provider` (MCP: `provider: "none"`) disables the basemap even when
a token is available.

Opening a track file never blocks on an authentication prompt. If Mapbox isn't
connected, the viewer shows **No basemap · Mapbox not connected** and echoes that
notice once to stdout after the viewer closes. Human CLI renders also print the
short notice; structured JSON/MCP output carries setup details in its status fields. Explicit
`--no-provider` is quiet. Credential
discovery can run while imports display. A file-free launch without credentials
offers setup or continuing offline; explicit `--no-provider` and an empty token
environment skip the offer. Ctrl-K lists **Keyboard shortcuts** first and puts
**Mapbox account setup** last when disconnected. Connected viewers offer **Mapbox
settings** there instead. Login hands the terminal to Mapbox CLI and restores the
viewer afterward, preserving workspace/camera and replacing map/search clients.
The setup dialog separates actions from information. With a missing CLI, choose
**Installation help**, **Check again**, or **Continue offline**. Official
documentation links open in a scrollable help page; checking again reports
success/failure and can detect a newly
installed CLI and reuse your saved login. Sign-in is offered when the CLI passes
its system/compatibility check and the viewer is disconnected. Connected settings
show **Connected** and **Check connection**, instead of asking you to sign in again.

**Toggle traffic** in the main Ctrl-K menu toggles the viewer's Traffic v1
congestion/road-closure layer. It is off by default and follows the current map
centre as you pan, independently of the basemap. Routes and markers stay above
traffic rather than changing their colours. The legend uses green/amber/red/dark
red for low/moderate/heavy/severe congestion; ASCII maps use `1`–`4`, and `X` marks
closures in both map modes. Press `v` for traffic status, fetch time and refresh
deadline. Missing coverage is not presented as free-flowing traffic.

The Traffic entry is greyed out until the selected Mapbox connection is verified
by saved-login account access, a successful Streets request or **Check connection**.
A token being present alone does not enable it. A saved Traffic-on preference waits
for verification; turning Traffic off remains available if the connection drops.
Enabled toggle options show a tick after their label, such as **Toggle traffic ✓**.
Use arrows to select an option, then Space to toggle it without closing the menu.
Spaces entered while typing stay in the search query.

Traffic makes additional tile requests, including when the view is stationary.
Response cache headers determine refresh, capped at five minutes; the live account
check observed roughly a one-minute deadline. Access is checked by traffic requests
separately from Streets. On an account/token denial, toggle traffic off/on or use
**Check connection** to retry. A failed refresh retains the last usable traffic
with stale status in credits; imported geometry remains usable. Traffic-aware
route calculation and detailed incident feeds are separate from this overlay.

**CLI telemetry** in Mapbox settings is an opt-in toggle, off by default. The
preference is shared by viewer/CLI/MCP calls and saved at
`~/.config/mapbox-tui/settings.json` on Linux (`XDG_CONFIG_HOME` relocates it). It controls
only Mapbox CLI processes launched by `mapbox-tui`, not standalone `mapbox` commands.
An explicit `MAPBOX_CLI_NO_TELEMETRY` environment opt-out takes precedence. See
[Mapbox CLI privacy](https://github.com/mapbox/mapbox-cli#privacy) for the usage
information it collects. Background map/search API requests are still normal
Mapbox service requests regardless of this setting.

`settings.json` persists telemetry and the viewer's traffic toggle. Theme/camera/
travel-mode choices and workspace plans are session-only. Login credentials are
owned by Mapbox CLI under `~/.mapbox/` (or `MAPBOX_CONFIG_DIR`); `mapbox-tui` doesn't save
another token copy. Place-search availability is derived from the selected token,
so it needs no separate remembered permission.

To carry over viewer preferences from an earlier installation, move its
`settings.json` into the new `mapbox-tui` config directory before launching.

Manual journey planning works offline. The location editor greys out unavailable
Mapbox address/place search and prevents its shortcut from starting a search.
**Map credits** is inside Mapbox settings, with `v` also opening it directly.
Attribution depends on data usage, not CLI login state. Esc returns nested credits
to settings; credits opened directly return to the map.
Explicit Mapbox renders without credentials get actionable setup guidance; plain
CLI warns on stderr, while JSON/MCP includes it in `scene.status`. These
non-interactive operations never open a browser. MCP discovers login lazily when
a render needs maps, rather than when importing/inspecting local files.

`mapbox-tui auth status` separates stored login, active rendering-token source/account,
token validity and actual Streets access. Login and status probe Streets with a
tile request. `--usage` adds account and eligible active-public-token Vector Tiles
reports, with matching inclusive UTC dates and generation timestamps. A rendering
override from another account can't receive the logged-in account's token usage.
Unavailable statistics don't make a working map token invalid; no usage is an
empty report. Counts can lag 24 hours, include other applications and aren't an
invoice or exact remaining allowance. Completed status reports exit zero even
when sections are logged out/unavailable.

`mapbox-tui auth logout` removes the selected shared CLI profile. It doesn't revoke the
account's public token, unset environment overrides, or erase tokens already held
by other running apps. A subsequent launch must resolve credentials again.

Mapbox requests use your account and may incur charges. Provider errors leave the
file inspectable. See [provider notes](docs/PROVIDERS.md) for caching and attribution
requirements, including the unresolved graphical-logo requirement.

The viewer cancels requests for obsolete tile coverage and refreshes stationary
views as tiles expire. Uncacheable responses use a 60-second refresh interval; provider
backoff still applies. Press `v` to see source status, refresh deadlines and credit
links. CLI/MCP renders remain one-shot requests and expose this information in
`scene.sources`; they do not run background refresh loops.

Credentials, personal GPX/FIT/TCX/KML files, binaries and generated reports are
ignored by Git. Only the four explicitly allowed synthetic GPX fixtures are tracked.

### Which commands need Mapbox authentication?

| Command / feature | Mapbox account/token | Mapbox CLI | Offline alternative |
|---|---|---|---|
| Imported files, inspection, elevation and playback | No | No | Fully available |
| Manual journey coordinates, editing and reversal | No | No | Fully available |
| Real basemaps in viewer, `render` or MCP | Yes | Recommended for saved login; unnecessary with an environment token | Imported geometry/empty canvas |
| Quick and journey address/named-POI search | Yes; automatic when connected | Recommended for saved login; unnecessary with an environment token | Coordinates/map centre |
| Journey walking/cycling/driving routes | Yes, with Directions access; Driving defaults when connected | Recommended for saved login; unnecessary with an environment token | Manual straight-line plotting |
| `mapbox-tui auth login` / `logout` | Login signs in or creates one | Required | Offline features remain available |
| `mapbox-tui auth status` | No to report status | Needed to inspect the saved login and token metadata | Reports unavailable sections |
| `mapbox-tui auth status --usage` | Yes, with statistics access | Required for usage queries | Maps/local features still work without statistics |
| `mapbox-tui summary`, `inspect`, `bench`, local MCP tools | No | No | Fully available |

Mapbox is the default external basemap; `--no-provider` disables it. Camera and display
options (`--center-lon`, `--center-lat`, `--zoom`, `--bearing`, `--ascii`,
`--cell-aspect`), file selection and `--point-index` do not require authentication.
The [MCP tool table](#mcp) lists the same distinction for each service operation.

## Controls

| Action | Keyboard | Mouse |
|---|---|---|
| Add another track file | Ctrl-O or `o` | |
| Manage loaded files/items | Ctrl-L | Wheel scrolls the dialog |
| Quick place search (without editing a journey) | `/` | |
| Journey planner | Ctrl-J | |
| Command palette / help | Ctrl-K / `?` | |
| Choose colour theme | Ctrl-K → Colour theme | |
| Pan | Arrows or `h j k l` | Left-drag |
| Zoom | `+` / `-` | Wheel, anchored to pointer |
| Zoom at 2× speed | | Ctrl / Cmd + wheel |
| Fit all visible geometry / world view when none is visible | `f` / `r` | |
| Select recorded points | `[` / `]` | Click near a track |
| Seek along the track | PgUp/PgDn, Home/End | |
| Inspect selected position | Enter | |
| Play / pause | Space | |
| Change playback speed | `p` picker, `s` / `S` cycle | |
| Center and follow | `c` | |
| Show / hide visible elevation profiles | `e` | |
| Focus route details | Tab | Wheel over sidebar |
| Rotate / north-up | `,` / `.` / `n` | Right-drag |
| Toggle ASCII / camera animation | `a` / `m` | |
| Map credits | `v` | |
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

## Colour themes

Three terminal palettes are bundled, based on Omarchy's theme catalogue:

- **Catppuccin** (`catppuccin`): dark, the default.
- **Catppuccin Latte** (`catppuccin-latte`): light.
- **Tokyo Night** (`tokyo-night`): dark.

Use **Ctrl-K → Colour theme** to open the theme list. The current palette is
marked; arrows or `j`/`k` preview each highlighted palette across the viewer.
Enter keeps the preview and returns to the map. Esc restores the theme that was
active when the picker opened and returns to the command menu.
The map, elevation chart, activity colours, dialogs and playback controls update
together without re-importing files, fetching tiles or resetting playback.
Each activity keeps its palette slot; the slot's colour changes with the theme.

```sh
./bin/mapbox-tui fixtures/example.gpx --theme catppuccin-latte
./bin/mapbox-tui --theme tokyo-night
./bin/mapbox-tui fixtures/example.gpx --theme "/path/to/my-theme.toml"
```

Built-ins are embedded in the binary. Custom palettes are read at startup and
join the picker for that session. Copy a file from [`go/themes/`](go/themes/),
give it a unique `id` and `name`, edit its colours and pass its path with `--theme`.
Theme changes last for the current viewer session. These are terminal palettes;
they do not select a different Mapbox style or tileset.
See [the palette format and theme API](docs/THEMES.md) for adding a theme.

## CLI

The interactive viewer is the default action. Supply GPX, FIT, TCX or KML files
to add tracks, routes and waypoints; no file starts free roam. `open` is
an optional alias.

```sh
./bin/mapbox-tui
./bin/mapbox-tui fixtures/example.gpx
./bin/mapbox-tui ride.gpx course.tcx
./bin/mapbox-tui --zoom 13 fixtures/example.gpx
./bin/mapbox-tui --help
```

Options may appear before or after the file. Use `--` before filenames that start
with `-` or match a command name, such as `./bin/mapbox-tui -- render`.

Subcommands select a different operation:

| Command | File | Result |
|---|---|---|
| `render` | Zero or more | Static map text, or JSON with `--json` |
| `summary` | One required | Geometry statistics, or item listing with `--items`, as JSON |
| `inspect` | Required | Recorded point as JSON |
| `mcp` | None | Local stdio MCP server |
| `bench` | Required | Synthetic rendering benchmark timings as JSON |
| `auth login`, `logout`, `status` | None | Mapbox browser login, shared-profile logout, text/JSON status and optional usage |

```sh
./bin/mapbox-tui render --center-lon -0.1065 --center-lat 51.505 --zoom 13 --json
./bin/mapbox-tui summary fixtures/example.gpx
./bin/mapbox-tui inspect fixtures/example.gpx --point-index 3
./bin/mapbox-tui inspect fixtures/example.gpx --distance-m 1000
./bin/mapbox-tui render fixtures/example.gpx --width 120 --height 40
./bin/mapbox-tui render fixtures/example.gpx --json
./bin/mapbox-tui render ride.gpx course.tcx --json
./bin/mapbox-tui summary fixtures/example.gpx --items
./bin/mapbox-tui inspect fixtures/example.gpx --item-id 4 --point-index 0
./bin/mapbox-tui render --help
```

`summary` and `inspect` return JSON. `render` returns map text with attribution;
`render --json` returns plain map text and scene metadata, without ANSI sequences.
Indices are zero-based, distances are metres, and bounds are
`[west, south, east, north]`. Distance inspection returns the nearest recorded
point, not an interpolated measurement.

`route_count` counts distinct route-bearing items: GPX routes, TCX/FIT courses,
and KML placemarks containing route geometry. A GPS gap or multiple disconnected
parts within one item do not add routes. `segment_count` counts non-route parts;
point indices and distances refer to the combined file unless `--item-id` is
supplied, in which case they are local to that item. GPX metadata names
take precedence over individual track names for the file title.

Use `summary --items` to discover item IDs. `open`, `summary`, `inspect` and
`render` accept `--item-id` with one file. Point/item selectors require a single
file; multi-file `render` fits the workspace and marks the last file's active item.
In the viewer, keyboard seeking and displayed point indices are always local to
the active item. A startup `--point-index` without `--item-id` still uses the
combined file index and selects the item containing that point.

Camera options for the viewer, rendering and benchmarks: `--center-lon` and
`--center-lat` together, `--zoom` (-8 to 20), `--bearing` (-360 to 360),
`--cell-aspect` (1-3, default 2), and `--ascii`.

The viewer and `render` accept `--no-provider` to disable the basemap, plus
`--point-index` to select or highlight a recorded point. `--no-animation` and
`--theme` are viewer-only. Negative zoom allows widely separated items to fit in a
small terminal; file-free browsing still starts at zoom 0. `--width` (10-300) and
`--height` (5-100) size static maps and
benchmarks; the viewer uses the terminal size. `--json` belongs to `render`,
`--distance-m` to `inspect`, and `--frames` to `bench`. Benchmarks use internal
synthetic geometry, independent of credentials. Run `mapbox-tui COMMAND --help` for
that command's options.

## MCP

```sh
./bin/mapbox-tui mcp
```

The local stdio server exposes nine tools:

| Tool | Inputs | Result | Mapbox authentication |
|---|---|---|---|
| `load_track` | Local `path` | File ID, document ID, bounds, summary and item IDs; reuses loaded paths | No |
| `list_tracks` | None | Loaded filenames/paths, items, visibility and active item | No |
| `unload_track` | `track_id` | Removes a file and its items | No |
| `summarize_track` | `track_id`, optional `item_id` | File or item summary | No |
| `inspect_track` | `track_id`, optional `item_id`, and either `point_index` or `distance_m` | Recorded point; indices/distances are item-local when `item_id` is supplied | No |
| `select_item` | `track_id`, `item_id` | Selects and reveals an item | No |
| `set_item_visibility` | `track_id`, `visible`, optional `item_id` | Shows/hides an item or whole file | No |
| `remove_item` | `track_id`, `item_id` | Removes one item; unloads the file if it was its last item | No |
| `render_map` | Optional `track_id`/`item_id`, or `workspace: true`, plus viewport/provider options | Plain map text and scene metadata | Optional; required for real basemap tiles |

Use [mcp.example.json](mcp.example.json) with the absolute path to your binary.
Full schemas are in [shared/tools.json](shared/tools.json). Track IDs and tile
caches last only for that server process. MCP runs independently of the viewer.
Its render default is Mapbox when the server has a configured token, and `none`
otherwise. Set `provider: "none"` to disable tile requests. For a server launched
outside the repository, supply the token through its environment; the client's
credentials aren't automatically passed to the server.

Omit `track_id` to render just the basemap, as before. Set `workspace: true` to
render all visible items and highlight the active one; omit `track_id`, `item_id`
and `point_index` in that mode. Workspace scenes include `active_item` and
`visible_items` references, each containing `document_id` and `item_id`. Match
`document_id` to the file entry returned by `load_track` or `list_tracks`.
Visibility controls apply to workspace renders; an explicit `track_id` renders
that file directly, preserving the existing single-file behaviour.

Without a file/workspace and token, rendering returns an empty map with setup
guidance in `scene.status`. `point_index` requires a track. Camera defaults match
the viewer's free-roam world view.

## Architecture

The viewer, CLI and MCP reuse workspace lifecycle and scene-rendering rules, with
separate state per process. Bubble Tea, Bubbles and Lip Gloss provide the UI;
the application owns projection, clipping, Braille/ASCII rasterisation and picking.
Imported geometry stays usable without Mapbox data or credentials.

See [the architecture notes](docs/ARCHITECTURE.md) for the Go/Charm rationale,
responsibility boundaries, historical benchmarks and measurable criteria for
reconsidering Rust + Ratatui.

## Development and checks

Python 3 is required for the development and validation scripts:

```sh
make dev
make dev ARGS='fixtures/example.gpx --zoom 13'
make check
make interactions
make fixtures
make bench
```

The development watcher rebuilds and restarts the viewer when Go/build inputs or
bundled TOML palettes change. It requires a Unix-like interactive terminal; view
state resets on restart.

`make check` checks Go formatting, builds, runs tests and vet, checks CLI/MCP
consistency, and runs the race detector. `make test` runs just the build/test/vet
and CLI/MCP suite. These checks disable Mapbox credentials explicitly.
`make interactions` also checks the terminal viewer; it needs tmux and
`rsvg-convert`. CI runs all three required checks on pull requests and pushes to
main. Benchmarks and captures are generated under ignored `reports/`.

Enable the bundled pre-commit and pre-push hooks with
`git config --local core.hooksPath .githooks`. See [validation notes](docs/VALIDATION.md)
for the hook split, existing-hook setup, required CI checks and live provider checks.

Dependencies are pinned in `go.mod` with checksums in `go.sum`. Setup follows the
dependency-minimisation and deterministic-install principles in this
[supply-chain reference](https://github.com/lirantal/npm-security-best-practices).
CI actions are pinned to commit hashes and use a read-only repository token;
checkout does not persist credentials.

## Limitations

- Individual imports reject antimeridian-crossing geometry and file bounds wider
  than 180 degrees. Separate files may span a wider workspace; no connecting path
  or wrapped antimeridian route is invented.
- Display uses Mercator projection; distances use spherical Haversine calculations.
- Map labels are ASCII-only. Sensor metrics and GPX activity extensions aren't imported.
- Basemap detail is capped at tile zoom 14, even when the viewer zooms further.
- Distance includes each imported track and route, excluding gaps between parts.
- Terminal integration checks cover headless Linux/tmux; other environments aren't verified.
