# Usage notes

The viewer accepts GPX, FIT, TCX and KML files. Start it without a file to
explore the map, or open several files together:

```sh
./bin/mapbox-tui ride.gpx course.tcx
```

Use `--help` for command options and `?` in the viewer for shortcuts.
Use `--version` to print the build version and commit without starting the viewer.

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
| Manage loaded files and items | Ctrl-L | Wheel scrolls the Files panel |
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
| Centre selected `*` location; otherwise centre and follow the track | `c` | |
| Toggle elevation profiles | `e` | |
| Show / hide the sidebar | Ctrl-B, Esc closes | Wheel over panel scrolls |
| Cycle Context / Files / Journey | Tab / Shift-Tab | Click a panel header tab |
| Rotate / north-up | `,` / `.` / `n` | Right-drag |
| Toggle ASCII / camera animation | `a` / `m` | |
| Map credits | `v` | |
| Back / close panel / deselect `*` location | Esc | |
| Quit | `q` or Ctrl-C | |

The map starts with a compact context line: the active route or place, two facts,
and hints for Ctrl-B, `/` search and `?` help. Ctrl-B opens a scrollable Context panel
on the right without resizing the map or moving the camera. Context, Files and
Journey share that same panel, inset below the map’s outer border. The tabs form
the panel header; Journey shows its travel mode in the menu without a repeated
inner title. Click a header tab or use
Tab to cycle forwards or Shift-Tab backwards; Ctrl-L and Ctrl-J remain direct shortcuts.
Ctrl-L and Ctrl-J open or focus their view; pressing them again keeps it open.
Ctrl-K opens the command palette from any panel, editor or dialog. Esc returns to
the previous view, preserving its input, selection and sidebar visibility.
Esc closes the whole sidebar from any top-level tab, without stepping through
previous tabs. Within a nested editor or dialog, Esc goes back. Ctrl-B toggles sidebar visibility.

The panel stays open while you pan, zoom, or inspect positions on the exposed map.
In Context, arrows and H/J/K/L navigate the map, while Page Up/Down and Home/End
scroll the details. In Files and Journey, arrows navigate the list; Ctrl-B hides
the panel for keyboard map navigation. Narrow terminals use a full-width vertical
overlay with one column of inset on each side. Collapsing it exposes the map.

A `*` on the map marks the selected coordinates shown in the context line,
including search results. Selecting a loaded file opens its Context view in the
same panel. Switching views preserves the file selection, context scroll position
and journey plan.
Elevation profiles appear automatically when any visible imported activity has
recorded elevation, including files opened later. They stay hidden without
elevation data and disappear when the last elevation-bearing activity is hidden
or removed. Use `e` or the command palette to toggle them; your explicit choice
lasts for the viewer session, including later loads and selection changes.
Attribution stays in the footer.

Menus wrap. Rounded modal borders contain the current title and a breadcrumb
for nested dialogs. Esc goes back in nested dialogs and closes the top-level dialog.
On the map, Esc clears the selected `*` location without moving the camera or
unloading files or journeys. If a panel is open, the first Esc closes it; press
Esc again to clear the location.
Use `--ascii` if your terminal doesn't render Braille well. The minimum viewer
size is 30 columns by 12 rows.

## Loaded files and playback

Ctrl-L lists files and their items. Incomplete planner drafts stay out of this list
until Start, Destination and every added waypoint are set; opening the planner preserves the selected file. Enter selects, Space toggles visibility,
Delete removes the selected row and `D` removes its whole file.
Reopening a loaded path reuses the import; remove it first to reread a changed file.

Only the active item plays. Hiding or removing it stops playback and clears the
selection. Visible elevation profiles appear in file and item order on a shared
distance axis. Sections without elevation stay blank; recorded zero elevation
is valid, and gaps and disconnected parts stay separate.

Playback uses recorded timing when a part has complete, increasing timestamps.
Otherwise it uses a labelled 6 m/s estimate. Gaps stay separate; interpolated
values have a `~` prefix. Pan and zoom keep playback running. Selecting a point
pauses it, and manual pan disables camera follow. Press `c` to centre the selected
`*` location without changing zoom, including when no file or journey is loaded.
When no `*` location is selected, `c` restores track follow. Use `[` or `]` to
return selection to a recorded point before restoring follow.

`S` and `E` mark part endpoints, `W` marks waypoints and `@` marks the selected
or interpolated position.

## Favourite places

Save commonly used locations in a local favourites library. In quick search (`/`)
or a journey location editor, highlight an address or town with Up/Down and press
**Alt-S**. It saves under the returned place name and gains a **★** (`*` in ASCII
mode). The search stays open; saving does not move the map or change your journey.
Use the favourites picker to rename it later. Press **Alt-S** again to remove it;
the star disappears. Save/remove feedback appears at the bottom right.

You can also enter latitude, longitude and press **Alt-S** to name and save it.
An existing manually entered stop can be saved the same way.
On the map, select a point and use **Save selected location as favourite** in the
command palette (`Ctrl-K`).

Press **Ctrl-F** in quick search or a location editor to open Favourites, or choose
**Favourite places** in the command palette. Type to filter by name, use Up/Down
to choose, then Enter to revisit the location or copy it into the edited journey
stop. **Ctrl-R** renames the selected favourite. **Delete** or **Backspace** opens
a confirmation with **Cancel** focused. Tab or Left/Right switches buttons; Enter
activates the selected button, and Esc cancels. You can also click either button.
Backspace edits the filter while typing; use Up/Down to focus a result before
removing it with Backspace. Names must be non-blank and unique, ignoring case.

Favourites work offline and survive restarts. They live in `favourites.json` next
to `settings.json`: on Linux, `$XDG_CONFIG_HOME/mapbox-tui/favourites.json`, or
`~/.config/mapbox-tui/favourites.json` when that variable is unset. The JSON stores
names, coordinates, stable IDs and location origins. `source` identifies how the
location was obtained. `provider_id` is the returned Mapbox place ID, used to
recognise saved search results even after renaming; `id` is our own local ID.
Provider display credits are derived from the source, not repeated in the JSON.

`version` describes the whole file format. Version 1 files remain readable; the
next save, rename or deletion writes version 2 without per-location attribution.
Future breaking format changes will migrate the file as a unit. Unknown versions
are left untouched.

The library is independent of the workspace: clearing a workspace leaves favourites intact, and renaming or deleting
a favourite leaves copied journey stops unchanged.

Saving an address or town makes one Mapbox Permanent Geocoding request using the
selected result’s ID. This is billed separately from search and requires a valid
credit card on your Mapbox account or an enterprise contract. Only the permanent
response supplies the stored place details. Mapbox credit is shown when reusing
these locations offline. Named venues such as cafés are not supported by that
endpoint; Save explains this without asking for coordinates.
If Mapbox cannot return the exact selected place, nothing is saved.

Opening and filtering Favourites makes no location lookup requests; basemaps and
journey routing still follow their normal settings.
Esc returns to the parent search with its results and selection intact while the
search session is valid. Pending lookups are cancelled; press **Ctrl-S** to resume
them or refresh after the session expires or the Mapbox connection changes.
Returning from Commands without visiting Favourites resumes an interrupted search
automatically.

If the file cannot be read or a save fails, the viewer reports the error and leaves
existing data intact. Concurrent viewers preserve unrelated edits; if the same
favourite has changed, reopen Favourites before editing it again.

## Search and journeys

`/` searches for a place without changing your activity or journey. Ctrl-J
opens the journey planner. Both accept **latitude, longitude** offline;
Mapbox enables named-place search and walking, cycling or driving routes.
For names or addresses, type at least three characters, choose a suggested match
with Up/Down, then press Enter.

Copy journey to clipboard copies a command for the globally installed `mapbox-tui`.
It recreates the resolved start, destination, waypoints, travel mode and loaded
files directly in the viewer; routes are recalculated and place labels are
replaced by coordinates. All stops must be set first. The terminal must support
OSC 52 clipboard writes (and allow them through any terminal multiplexer).

| Planner action | Control |
|---|---|
| Add a waypoint | Select No waypoints / Add another waypoint, then Enter; Ctrl-N also opens waypoint entry |
| Reverse the journey | Ctrl-R |
| Undo an edit | Ctrl-Z |
| Clear an endpoint / remove a waypoint | Delete or Backspace |
| Reorder waypoints | Alt-Up / Alt-Down |
| Choose travel mode | Select Travel mode, then Enter |
| Copy a journey command | Select Copy journey to clipboard, then Enter |
| Recalculate a route | Ctrl-D |
| Routing error details and snapped positions | Ctrl-U |
| Show / hide the planner | Ctrl-B |
| Pick the selected stop on the map | Ctrl-P, then click or Enter for map centre |
| Cancel map picking | Esc |
| Close the sidebar | Esc |

Files, inspected places and journey plans coexist. Opening or editing a journey
preserves the selected file, playback and inspected place. The context bar keeps
that context visible alongside a Journey entry; click that entry or use Ctrl-J to
open the planner and return to the previous context view, including its scroll position.

The Journey view uses the same right-hand panel as Context and Files. Ctrl-B hides it without losing the plan or moving the camera.
The map remains interactive where visible. Ordinary map clicks inspect positions;
they change a stop while its location input is open, or after Ctrl-P on a stop row.
Selecting a stop opens a focused input: type immediately or click the map. New
waypoints are added only once a location is chosen; Esc cancels without changing
the existing journey.
Picking temporarily hides the planner. Click to choose a position, press Enter to
use the map centre, or Esc to return without changing the stop. Route geometry
remains visible after the planner is hidden or closed.

Each stop shows coordinates once. Ctrl-U contains current routing diagnostics and
snapped road/path coordinates; usage and request behaviour are documented in
[Mapbox routing notes](MAPBOX.md#journey-roadpath-routing).

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

To open a journey directly (coordinates use latitude,longitude):

```sh
mapbox-tui --file fixtures/country-ride.gpx --mode cycling \
  --start '51.309174,-2.583389' --end '51.297454,-2.532641'
```

Repeat `--waypoint LAT,LON` in travel order and `--file PATH` for additional files.
Omit files for a journey-only session. Both endpoints are required; `--mode`
defaults to `driving`, with `manual`, `walking` and `cycling` also supported.
These are interactive viewer options; manual geometry works offline, while routed
modes use your normal Mapbox connection. Named-place search remains available
inside the planner; startup arguments accept resolved coordinates.
