# Validation

## Local Git checks

The bundled hooks require Make, Python 3 and the Go version declared in `go.mod`,
with `go` and `gofmt` available on `PATH` when Git runs. With mise, use its shims
or run Git through `mise exec go@1.27.1 --`.

From the repository root, inspect any existing hook setup before enabling:

```sh
git config --get core.hooksPath
git config --local core.hooksPath .githooks
```

If you already use hooks in another directory or `.git/hooks`, add these Make
commands to that setup instead of replacing it. No hook framework is required.

| Hook | Reproduce manually | Scope |
|---|---|---|
| Pre-commit | `make pre-commit` | Staged whitespace and staged Go formatting; reads the index, including partially staged files, without rewriting or staging anything |
| Pre-push | `make check` | Working-tree Go formatting, build/tests/vet, CLI/MCP parity and Go race checks |

Terminal interactions stay out of pre-push to keep local checks lighter; run
`make interactions` for viewer changes. To disable this hook setup, use
`git config --local --unset core.hooksPath`, or restore your previous value.

## CI and required merge checks

GitHub Actions runs on every pull request and push to `main`. It reads the Go
version from `go.mod` and uses Make and Python 3 from the Ubuntu runner. The
terminal job installs tmux, `rsvg-convert` (`librsvg2-bin`) and DejaVu fonts.
All jobs explicitly set `MAPBOX_ACCESS_TOKEN=""`; tests use fake providers and
do not need credentials or make live Mapbox requests. Superseded runs for the
same pull request or branch are cancelled. Terminal captures are uploaded on
failure and retained for seven days.

Agreed required checks for merging to `main` (issue #8):

| Required check | Local equivalent |
|---|---|
| **Go checks** | `make format-check test` |
| **Race checks** | `make race` |
| **Terminal interactions** | `make interactions` |

Configure these exact names in the `main` branch protection/ruleset once the first
CI run publishes them. The workflow defines the checks; merge enforcement is a
separate repository setting.

CI actions use commit-hash pins, read-only repository permissions and checkout
without persisted credentials. Setup follows the dependency-minimisation and
deterministic-install principles in the
[supply-chain reference](https://github.com/lirantal/npm-security-best-practices).

## Automated checks

From the repository root:

```sh
make format-check
MAPBOX_ACCESS_TOKEN='' make test
make race
make interactions
```

`make check` combines formatting, `make test` and race checks. `make test` builds
`bin/mapbox-tui`, runs Go tests and `go vet`, then checks the CLI and local
stdio MCP server. It covers GPX/FIT/TCX/KML topology and missing measurements,
camera/picking, playback/profile behaviour, tile decoding and cache policy, token-free provider
errors, free-roam navigation and route transitions, tool schemas, and CLI/MCP
track-free rendering consistency. CLI checks cover default file opening, options
before/after files, scoped flags, help exit codes and benchmark dispatch. Provider
checks cover token-free geometry, missing-token warnings, saved-login discovery and
environment precedence, and automatic Mapbox selection for MCP. No Mapbox account
is required.

Auth tests use a fake CLI to cover schema compatibility, environment/debug/account
isolation, public-token retrieval, expired stored identity followed by successful
account access, warning-prefixed structured errors, malformed/oversized output,
whole-child timeout, opt-out, missing CLI, logged-out status, credential location,
HTTP-200 token validity, usage denial/empty reports, mismatched accounts/periods
and token-free text/JSON. Viewer tests cover cancellation/stale-result rejection,
both-client replacement, final connection-probe results, stable setup-action
selection and preserved imports/camera/playback. Settings checks cover atomic
replacement and telemetry-off fallback without blocking offline viewing. MCP
checks cover shared negative credential discovery and cancellation recovery.
No installed Mapbox CLI or account is needed for `make test`.

Go and terminal interaction tests use isolated config directories so saved viewer traffic/telemetry
preferences cannot enable extra sources or change expected defaults during checks.

With the optional CLI installed, `make mapbox-check` inspects six released command
schemas without account/API requests. `python3 scripts/mapbox_auth_check.py` is a
live saved-login/token/Streets/usage check and makes a tile request. Auth/signup
verification and deferred trial-account checks are recorded in
[the auth notes](MAPBOX_AUTH_PLAN.md).

Import regressions cover Garmin KML coordinate spacing and FIT header checksum
variants, including Zwift-style omitted header checksums. Edge cases include
malformed coordinates, corrupt checksums, truncation, and mixed-header chained files.

Document tests cover separate source filenames and item names, GPX track/route
ownership, TCX activities/courses, FIT chained files and session boundaries, and
KML placemarks with nested or late-named folders. They check document-local item
identity, independent distances and repeatable geometry recalculation. CLI/MCP
checks also verify that one TCX course with a GPS gap counts as one route.

Workspace model tests cover adding and reopening files, duplicate filenames,
visibility and removal, failed-import recovery, stale item references, and active
playback/profile transitions. Bounds checks include distant independent files,
hidden items and waypoint-only imports. Profile checks cover consecutive distance
sections, summed distance, common elevation scale, ordering, missing measurements,
cursor offsets, visibility and cache invalidation.
Scene tests cover stable per-item colours, active-item overlap
priority, waypoint picking, gaps between files and negative-zoom fitting.
Occupied-cell picking checks match rendered ownership in Braille/ASCII mode,
including shared colours, markers, clipping and rotation. Viewer checks cover
multi-file startup camera overrides, first/queued import failure recovery and
hiding/removing an active item during camera animation without moving the view.
CLI/MCP checks exercise item selectors, duplicate loads, listing, visibility, item/file
removal and multi-file rendering parity. A fake-provider test checks that network
rendering does not hold the workspace lifecycle lock.

Source lifecycle tests use controlled clocks and fake sources/transports to check
viewport cancellation, delayed/out-of-order results, source replacement and
visibility, stationary refresh, independent source completion and shutdown.
Provider-specific tests prove moving-camera requests complete within unchanged
tile coverage, retain fresh snapshots and refresh on expiry; obsolete coverage
still cancels and superseded generations stay rejected.
They cover returning to a cancelled viewport, cache expiry and `Age`, no-store/
no-cache behaviour, HTTP 429 recovery, malformed/oversized bodies, cache replacement
and cancellation while waiting for another batch. Viewer tests verify that file
loading does not block refresh and source failures leave imported items intact.
Failed-refresh checks retain the last usable scene and attribution while exposing
updated errors/retry deadlines; repeated errors and successful empty responses are
covered separately from source replacement and disabling.

Theme checks cover the three embedded TOML palettes, startup selection, custom-file
loading and invalid/incomplete palettes. Switching checks preserve loaded document
identity, playback, camera, source freshness and cached elevation geometry while
restyling existing widgets. They cover open-dialog input/focus/scroll state and
theme-picker/map painting at 120×40, 70×24, 70×18 and 30×12, including ASCII mode.
Picker regressions verify live highlighting previews, confirmation, restoration
on cancellation/closure/replacement, reopening after confirmation, return to the
filtered menu and removal of the old cycling shortcut from input/help.

Menu checks cover circular Up/Down navigation in Commands (including filtered,
single-result and empty searches), themes, speed, loaded files, Mapbox settings,
Journey Planner, travel modes, location suggestions and the file picker.

Quick-search checks cover `/` entry, offline coordinates, Mapbox suggest/retrieve,
debounce, circular results, selected-place credits and late credential discovery.
Acceptance/cancellation preserve imported geometry, journey stops, workspace
selection and playback; obsolete retrievals and clipboard results cannot change
a newer dialog or journey editor. Layout checks include 120×40, 70×18 and 30×12,
with ASCII mode and the real text cursor.
Quick-search framing checks cover one-mile local context across latitudes,
bearings, initial zoom levels and narrow viewports, including the dateline and
Mercator latitude limit.
World-edge regressions verify actual visible tile bounds and on-screen result
visibility. A playback-pulse regression keeps the accepted search inspection while
the activity advances, with explicit follow returning inspection to the activity.
Exact longitude/pole boundary cases also draw the searched point through the real
raster, checking exclusive screen bounds rather than inclusive camera coordinates.
Compact-search regressions clear stale early-Enter notices when results arrive,
keeping the selected match visible and selectable at 30×12 in both display modes.

Journey checks cover session-only generated item identity, isolation from imports,
full waypoint reversal/undo, unknown measurements, empty/unresolved location gaps
and geometry recalculation. Tests verify opening/reopening makes no location
requests. Fake transports verify automatic Mapbox search, suggest/retrieve session
ownership and rotation, cancellation, clipboard/editor ownership and debounce
ownership across replaced plans, paste-driven query invalidation,
edited-place provenance/attribution,
printable Unicode names/credits with control-character sanitization,
transient suggestion attribution and single-line footers with distinct credits,
stale responses after edits/removal, missing/invalid coordinates, HTTP/body limits
and token/query redaction. Planned geometry does not acquire recorded playback or
elevation profiles. Search checks prove automatic availability on fresh/repeated
launches, delayed credential discovery resuming typed queries, cancellation on
disconnect, minimum query length/debounce and no requests for empty/coordinate input.
Incomplete coordinate prefixes also stay offline without blocking numbered
addresses or numeric/ZIP+4 postcodes.
Viewer startup discovers saved-login credentials for search/routing with the
basemap disabled, while preserving explicit-empty opt-out and avoiding tile requests.
Journey dialogs are checked at 120×40, 70×24, 70×18 and 30×12
in Braille/ASCII modes.
Partial journeys retain the correct start/end markers without bridging unresolved
stops. Checks also cover notice-aware selection visibility, coordinate inspection
with a selected journey, imported profile reuse and playback command availability.

Directions checks use fake transports for ordered multi-stop requests and all three
profiles, separate snapped/user locations, geometry/estimate validation, HTTP/body
limits, redacted errors, cancellation and timeouts, no-route/no-segment responses,
account denial, profile limits and HTTP 429 backoff (integer/date Retry-After).
Workspace/viewer tests cover reversal/undo recalculation, committed coordinates and
search retrieval, superseded successes/errors, mode switches, retries, removal,
clearing, credential replacement, unchanged imported playback/elevation, manual
fallback and route attribution. Routed map/dialog layouts are checked at 120×40,
70×24, 70×18 and 30×12 in Braille/ASCII, including scrollable full error feedback.
Regressions cover failed unchanged-plan recalculation falling back to manual
geometry without current estimates/credits, and unchanged locations/modes making
no extra Directions requests.
Routing credits are deduplicated against basemap credits in the map footer.
Planner regressions cover the visible travel-mode selection, connected Driving
default (including delayed credential discovery), explicit Manual preservation,
automatic routing after complete endpoints/waypoints, Show waiting for resolved
geometry without duplicate requests, failure feedback and cancelled pending camera fits.
Show also waits for explicit recalculation of an already current route, fitting
the replacement on success or keeping the planner open with feedback on failure.
Panel checks verify unobscured map cells, input-cursor placement, sidebar/compact-strip
resizing, independent map/list mouse navigation and Tab keyboard focus at 120×40,
70×24, 70×18, 50×18 and 30×12 in Braille/ASCII.
Search checks keep the selected suggestion visible beside its input at compact
sizes, including notices and imported elevation profiles in a 70×15 sidebar.
Mouse checks cover selecting location/action/mode rows, placing start/destination/
waypoint coordinates, automatic recalculation and drag-versus-click separation.
View checks verify a mile-radius context across latitudes/viewports without cropping
longer plans. Planned endpoint A/B markers and separated stop-detail sections have
regression coverage.
Routed endpoints retain user-selected marker positions after provider snapping,
and fit-visible keeps both the original stops and the routed geometry on screen.

Opt-in live account check (three Directions requests, using shared credential resolution):

```sh
MAPBOX_TUI_DIRECTIONS_CHECK=1 mise exec go@1.27.1 -- go test -tags=integration ./go -run '^TestDirectionsAccountAccess$' -v
```

On 2026-10-09 this passed with the existing saved-login account: walking 1.27 km /
16 min, cycling 1.50 km / 6 min, driving 1.47 km / 7 min for the short London plan.
This proves those profiles independently of authentication/Streets access for that
account. A separate card-free trial-account run remains pending; no trial signup
was performed. Ordinary repository checks remain offline and don't run this test.

Optional checks, after building:

```sh
make fixtures
make bench
mise exec go@1.27.1 -- go test ./go -run '^$' -bench BenchmarkWorkspaceScene -benchmem
```

Interaction checks require tmux and `rsvg-convert` and run in headless Linux
pseudo-terminals. They exercise keyboard/mouse input, dialogs, playback, resizing
and terminal restoration. Auth checks use a fake external CLI at wide/compact/tiny
sizes, verify terminal streams are inherited, preserve imported files through
palette login, and exercise file-free setup/offline continuation with a missing
CLI. Missing-CLI GPX launches display the concise connection notice without a
prompt, and print it once to stdout after the viewer exits; the same output is
checked at tiny/compact sizes.
Dependency-aware setup checks exercise installation help, all three selectable
actions and rechecking an executable that appears while the viewer is open.
Multi-file checks cover startup with two files, the
Ctrl-L list, visibility, selection, removal and clearing at wide/compact sizes.
Two-activity checks also exercise ordered profiles, selected-only playback markers,
pause, switching while playing and hiding the playing activity without losing the
remaining profile. Colour captures are saved alongside the interaction results.
Theme interactions select all three palettes through the nested list at wide/compact
sizes, check live preview/cancellation/filter restoration, recoloured dialogs, unchanged map text
and continued playback in ASCII mode.
Journey interactions use offline coordinates to exercise
start/destination entry, Alt-M map-centre selection, intermediate waypoints,
full reversal/undo, reorder/removal,
map fit, ASCII mode and reopening at 120×40, 70×24 and 70×18. They make no location
lookup requests; provider behaviour uses fake transports in Go tests.
They also exercise the routing-mode picker, disconnected-account feedback,
scrollable usage/limits and explicit Manual fallback without Directions requests.
Nested-dialog regressions cover menu/filter/cursor restoration, repeated Esc through
planner/mode/feedback contexts, cancelled location edits, stale clipboard rejection,
setup actions reordered by credential discovery and theme changes while nested.
They also preserve map focus when cancelling a location editor and prevent a
cancelled queued login from launching its CLI process after returning to the menu.
Route completions refresh a nested planner's saved notice, keeping success/failure
feedback current when returning through direct or travel-mode feedback dialogs.
Terminal checks verify returning from speed selection to its filtered menu,
login to the menu, and routing feedback to the travel-mode picker.
Popup stacks reveal only dimmed top/left back-frame edges at 120×40, 70×18 and
30×12, including different dialog sizes, theme changes and ASCII borders.
Planner terminal checks keep the map visible beside the panel and exercise Tab
map/planner focus, mouse zoom, clicking a waypoint row and placing its map point.
They don't verify physical mouse forwarding or other
operating systems. Benchmark and capture output is ignored under `reports/`.

With a locally configured token, these checks make billable Mapbox requests:

```sh
python3 scripts/mapbox_check.py
python3 scripts/interactions.py --mapbox
```

## Traffic overlay

Offline regression checks cover MVT congestion/closure decoding, unknown coverage,
20-mile clipping of crossing roads at multiple latitudes/bearings, current-camera
clipping with unchanged tile coverage, local bounded requests, deterministic
severity overlap, preserved route colour/ownership and closure/ASCII glyphs.
Controlled time/transports verify traffic cache caps, `Age`, stationary refresh,
stale retention/recovery, redacted access-denial feedback and no repeated denied
requests. Settings checks cover persistent on/off, delayed credentials, cancellation
and late-result rejection. Menu checks cover disabled unchecked credentials,
successful certification, saved-preference gating, revoked verification and
turning traffic off while disconnected. Terminal interactions exercise the main
menu's greyed-out entry and disabled activation with imported geometry at 120×40,
70×18 and 30×12 in Braille/ASCII with credentials disabled.
Review regressions cover complete multi-word/playback menu search without toggling, cached basemaps
not overriding failed auth checks, `Age` before the cache cap, and complete traffic
retention after partial-batch 403/503 failures.

Opt-in live account check (one Traffic v1 TileJSON request and normally one London
zoom-14 tile request, using shared credential resolution):

```sh
MAPBOX_TUI_TRAFFIC_CHECK=1 mise exec go@1.27.1 -- go test -tags=integration ./go -run '^TestTrafficAccountAccess$' -v
```

On 2026-10-10 this passed with the existing saved-login account: advertised zooms
6–16, one tile request and 1,043 decoded road features, with approximately 57 seconds
until refresh. No raw traffic data or Directions request was made. Account tokens
and raw response bodies are not logged. A separate card-free trial-account check
remains pending. Ordinary repository and terminal checks stay offline.

A one-off radius comparison on 2026-10-10 reused the same **7,494 London traffic
features** at zoom 8 for both caps. Three geometry-rendering runs on this machine
gave these median times:

| Terminal | 10-mile cap | 20-mile cap | Traffic tiles |
|---|---:|---:|---:|
| 120×40 | 13.50 ms | 13.55 ms | 4 for either cap |
| 160×50 | 14.78 ms | 15.11 ms | 4 for either cap |

Allocations were the same for each radius at a given terminal size. A temporary
Go build overlay varied the radius only for the benchmark; production keeps the
fixed 20-mile cap. The timed loops reused in-memory data and exclude network,
tile decoding and ANSI/UI painting. This measures the sampled London scene, not
all locations/zooms.

To try the viewer, launch at a local focus and enable **Toggle traffic**
directly through Ctrl-K. It is greyed out until the Mapbox connection is verified;
use account setup / **Check connection** if needed. For example:

```sh
./bin/mapbox-tui --center-lon -0.1276 --center-lat 51.5072 --zoom 8
```

Pan and rotate, toggle ASCII with `a`, and press `v` for the traffic fetch time,
refresh deadline, stale status and legend. Turning traffic off removes its snapshot
without moving the camera or changing loaded routes.

## Manual smoke check

1. Run `MAPBOX_ACCESS_TOKEN="" ./bin/mapbox-tui fixtures/example.gpx` and check separate
   parts and waypoints on an empty canvas, with no authentication warning.
2. Pan, zoom, select points and use `f` to fit the geometry.
3. Play with Space and check the map keeps its view. Use `c` to explicitly center
   and follow; change speeds, toggle elevation with `e`, and pause/seek.
4. Open help, the command palette and file picker; check Esc closes each dialog.
5. Resize the terminal and try ASCII mode with `a`.
6. Quit with `q` or Ctrl-C and check the terminal is restored.
7. Run `MAPBOX_ACCESS_TOKEN="" ./bin/mapbox-tui` without a GPX; check the empty canvas
    includes a concise connection notice. Pan, zoom, rotate and click to inspect
   coordinates. Open a GPX with Ctrl-O, then use Ctrl-K's **Free roam (clear selection)**
    and check its geometry and elevation stay visible without playback or a profile cursor.
8. With a token configured, run `./bin/mapbox-tui` for the world view or
   `./bin/mapbox-tui --center-lon -0.1065 --center-lat 51.505 --zoom 13` for a local
   basemap. Check tiles load while panning and no route/playback/profile is shown.
9. Try `./bin/mapbox-tui --zoom 13 fixtures/example.gpx`, then check
   `./bin/mapbox-tui --help` and `./bin/mapbox-tui inspect --help` show the relevant options.
10. Run `MAPBOX_ACCESS_TOKEN="" ./bin/mapbox-tui render fixtures/example.gpx`;
    check the file is rendered and stdout reports **No basemap · Mapbox not connected**.
    Repeat with `--no-provider`; check the intentional offline render is quiet.
11. Open FIT, TCX and KML files with Ctrl-O. Check their tracks/waypoints, elevation
     and playback match the measurements present; a failed import keeps the previous file.
12. Open two files and press Ctrl-L. Check filenames and individual item names,
    toggle visibility with Space, select with Enter, remove an item with Delete
    and remove a file with `D`. Use `f` to fit only the remaining visible geometry.
    Check that each route, its elevation and its list entry use the same stable colour.
    With two activities containing elevation, check the chart places them end to end
    in load order with a summed distance axis and a gap between them. Play one activity,
    verify only its `@` markers move, then select the other and check playback resets
    paused without changing the chart/legend order. The chart cursor includes earlier
    activities' distances. Hide the playing activity and check its marker disappears
    while the other elevation remains visible.
13. Use **Clear workspace (remove all files)** and check the camera stays in place.
    In a narrow terminal, load widely separated files and verify `f` can fit them
    at a negative zoom, with subsequent wheel zoom staying smooth.
14. With Mapbox configured, open provider credits with `v` to inspect the source's
    status and refresh deadline. Pan during a request and check the newer viewport
    wins; keep the camera still through expiry and check the source refreshes.
     These live checks make billable requests; automated lifecycle tests use fakes.
15. Use Ctrl-K's **Colour theme** to preview highlighted palettes and Enter to keep one.
    Check Esc restores the original theme and returns to the filtered menu, and open
    help, the file picker or speed dialog to inspect the updated colours.
    Repeat in a narrow terminal and ASCII mode; playback should continue.
    Start with `--theme catppuccin-latte`, then try a custom TOML file with a unique
    ID. Invalid palettes should fail before entering the viewer.
