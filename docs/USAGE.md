# Usage notes

The viewer accepts GPX, FIT, TCX and KML files. Start it without a file to
explore the map, or open several files together:

```sh
./bin/mapbox-tui ride.gpx course.tcx
```

Use `--help` for command options and `?` in the viewer for shortcuts.

From the repository root, try `./bin/mapbox-tui fixtures/example.gpx` for a small
synthetic activity with a planned route and waypoints, or
`./bin/mapbox-tui fixtures/country-ride.gpx` for a recorded 181.09 km ride.
The latter retains elevation, with the original endpoint areas and identifying
metadata removed. It has no timestamps, so playback uses estimated timing.
Both work offline; a configured Mapbox account adds the basemap. Add
`--no-provider` to disable it.

## Controls

| Action | Keyboard | Mouse |
|---|---|---|
| Open a file | Ctrl-O or `o` | |
| Manage loaded files and items | Ctrl-L | Wheel scrolls the dialog |
| Quick place search | `/` | |
| Journey planner | Ctrl-J | |
| Command palette / help | Ctrl-K / `?` | |
| Pan | Arrows or `h j k l` | Left-drag |
| Zoom | `+` / `-` | Wheel, anchored to pointer |
| Zoom at twice the speed | | Ctrl + wheel |
| Fit visible geometry / world view | `f` / `r` | |
| Select recorded points | `[` / `]` | Click near a track |
| Seek along the track | PgUp/PgDn, Home/End | |
| Inspect selected position | Enter | |
| Play / pause | Space | |
| Change playback speed | `p` picker, `s` / `S` cycle | |
| Centre and follow | `c` | |
| Toggle elevation profiles | `e` | |
| Focus route details | Tab | Wheel over sidebar |
| Rotate / north-up | `,` / `.` / `n` | Right-drag |
| Toggle ASCII / camera animation | `a` / `m` | |
| Map credits | `v` | |
| Back / quit | Esc / `q` or Ctrl-C | |

Menus wrap. Esc goes back in nested dialogs and closes the top-level dialog.
Use `--ascii` if your terminal doesn't render Braille well. The minimum viewer
size is 30 columns by 12 rows.

## Loaded files and playback

Ctrl-L lists files and their items. Enter selects, Space toggles visibility,
Delete removes the selected row and `D` removes its whole file.
Reopening a loaded path reuses the import; remove it first to reread a changed file.

Only the active item plays. Hiding or removing it stops playback and clears the
selection. Visible elevation profiles appear in file and item order on a shared
distance axis.

Playback uses recorded timing when a part has complete, increasing timestamps.
Otherwise it uses a labelled 6 m/s estimate. Gaps stay separate; interpolated
values have a `~` prefix. Pan and zoom keep playback running. Selecting a point
pauses it, and manual pan disables camera follow. Press `c` to restore follow.

`S` and `E` mark part endpoints, `W` marks waypoints and `@` marks the selected
or interpolated position.

## Search and journeys

`/` searches for a place without changing your activity or journey. Ctrl-J
opens the journey planner. Both accept **latitude, longitude** offline;
Mapbox enables named-place search and walking, cycling or driving routes.

| Planner action | Control |
|---|---|
| Add a waypoint | Ctrl-N |
| Reverse the journey | Ctrl-R |
| Undo an edit | Ctrl-Z |
| Clear an endpoint / remove a waypoint | Delete or Backspace |
| Reorder waypoints | Alt-Up / Alt-Down |
| Choose travel mode | Ctrl-B |
| Recalculate a route | Ctrl-D |
| Routing feedback and usage | Ctrl-U |
| Switch map / planner focus | Tab |
| Close the planner | Esc or Ctrl-J |

Manual mode plots straight lines between resolved locations. Connected plans
default to Driving and recalculate after location or mode edits. Routed distance
and travel time come from Mapbox; planned geometry has no recorded timestamps
or elevation. Plans last for the viewer session.

Traffic and colour themes are in Ctrl-K. See [Mapbox notes](MAPBOX.md)
for routing, traffic and search policies, and [themes](THEMES.md) for palettes.

## Imports and CLI output

GPX imports tracks, routes and waypoints; FIT and TCX import activities and
courses; KML imports placemarks and geometry, including Google track timestamps.
Sensor metrics, KML styles, overlays and network links aren't loaded. Use plain
`.kml`, rather than `.kmz`.

Distance excludes gaps and includes both tracks and planned routes. Use
`summary --items` to discover item IDs and `--item-id` to inspect one activity.
Point indices are zero-based; distances are metres; bounds are
`[west, south, east, north]`. Distance inspection returns the nearest recorded
point. See [MCP notes](MCP.md) for the same measurement rules through tools.

Options can go before or after filenames. Use `--` before a filename that starts
with `-` or matches a command name. Static `render` output is ANSI-free text;
`--json` adds scene metadata.

## Limits

Individual imports reject antimeridian crossings and bounds wider than 180
degrees. Separate files can span a wider workspace. Display uses Mercator
projection and distances use spherical Haversine calculations. Map labels are
ASCII-only, and basemap detail stops at tile zoom 14.

The viewer doesn't provide live GPS navigation or voice guidance. Terminal
integration checks cover headless Linux/tmux; other environments aren't verified.
