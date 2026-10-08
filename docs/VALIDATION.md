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
`bin/shoe`, runs Go tests and `go vet`, then checks the CLI and local
stdio MCP server. It covers GPX/FIT/TCX/KML topology and missing measurements,
camera/picking, playback/profile behaviour, tile decoding and cache policy, token-free provider
errors, free-roam navigation and route transitions, tool schemas, and CLI/MCP
track-free rendering consistency. CLI checks cover default file opening, options
before/after files, scoped flags, help exit codes and benchmark dispatch. Provider
checks cover token-free geometry, missing-token warnings, runtime config and
environment precedence, and automatic Mapbox selection for MCP. No Mapbox account
is required.

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

Optional checks, after building:

```sh
make fixtures
make bench
mise exec go@1.27.1 -- go test ./go -run '^$' -bench BenchmarkWorkspaceScene -benchmem
```

Interaction checks require tmux and `rsvg-convert` and run in headless Linux
pseudo-terminals. They exercise keyboard/mouse input, dialogs, playback, resizing
and terminal restoration. Multi-file checks cover startup with two files, the
Ctrl-L list, visibility, selection, removal and clearing at wide/compact sizes.
Two-activity checks also exercise ordered profiles, selected-only playback markers,
pause, switching while playing and hiding the playing activity without losing the
remaining profile. Colour captures are saved alongside the interaction results.
Theme interactions cycle all three palettes at wide/compact sizes, recolour open
help, credits, speed, workspace, file-picker and command dialogs, preserve filtered
input and map text, and continue playback in ASCII mode.
They don't verify physical mouse forwarding or other
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
4. Open help, the command palette and file picker; check Esc closes each dialog.
5. Resize the terminal and try ASCII mode with `a`.
6. Quit with `q` or Ctrl-C and check the terminal is restored.
7. Run `MAPBOX_ACCESS_TOKEN="" ./bin/shoe` without a GPX; check the empty canvas
   includes token setup guidance. Pan, zoom, rotate and click to inspect
   coordinates. Open a GPX with Ctrl-O, then use Ctrl-P's **Free roam (clear selection)**
    and check its geometry and elevation stay visible without playback or a profile cursor.
8. With a token configured, run `./bin/shoe` for the world view or
   `./bin/shoe --center-lon -0.1065 --center-lat 51.505 --zoom 13` for a local
   basemap. Check tiles load while panning and no route/playback/profile is shown.
9. Try `./bin/shoe --zoom 13 fixtures/example.gpx`, then check
   `./bin/shoe --help` and `./bin/shoe inspect --help` show the relevant options.
10. Run `MAPBOX_ACCESS_TOKEN="" ./bin/shoe render fixtures/example.gpx --provider mapbox`;
    check the file is rendered and stderr explains how to configure a token.
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
15. Use Ctrl-P's **Choose colour theme** to select each palette. With help, the
    file picker or speed dialog open, press Ctrl-T and check all visible colours
    update. Repeat in a narrow terminal and ASCII mode; playback should continue.
    Start with `--theme catppuccin-latte`, then try a custom TOML file with a unique
    ID. Invalid palettes should fail before entering the viewer.
