# Usage notes

The viewer accepts GPX, FIT, TCX and KML files. Start it without a file to
explore the map, or open several files together:

```sh
./bin/mapbox-tui ride.gpx course.tcx
```

Each imported track, route, activity or KML placemark supports up to 100,000
points, counting all its segments and attached waypoints. If an item exceeds
that cap, the whole file is rejected; split the recording before importing.
Accepted geometry keeps every point and works offline.

Use `--help` for command options and `?` in the viewer for shortcuts.
Use `--version` to print the build version and commit without starting the viewer.

From the repository root, try `./bin/mapbox-tui fixtures/example.gpx` for a small
synthetic activity with a planned route and waypoints, or
`./bin/mapbox-tui fixtures/country-ride.gpx` for a recorded 181.09 km ride.
The latter retains elevation, with the original endpoint areas and identifying
metadata removed. It has no timestamps, so playback uses estimated timing.
Both work offline; a configured Mapbox account adds the basemap. Add
`--no-provider` to disable it.

## Mapbox usage and offline mode

Before enabling online features, CLI and in-app login show the same
[billing-risk confirmation](MAPBOX.md#free-usage-and-billing). Review your account
allowances and configure usage alerts; alerts do not prevent charges.

Ctrl-K → Mapbox settings shows current-month usage for the products this app uses.
**Usage details** shows totals, units, days reported, latest reported date and
report/fetch timestamps. **View usage on Mapbox** opens your full account usage.
Figures include other apps on the saved-login account, may lag and do not show
remaining allowances. Usage loads once at startup and after login, without polling.

**Go offline** persists until you choose **Go online**. Maps, traffic, place lookup
and routing are disabled; coordinate input, local favourites and imported files
remain usable. Place search explains how to connect or go online in Mapbox settings.
Authenticated accounts still check usage at viewer startup, even while offline.
`--no-provider` only disables the basemap; it is separate from this offline mode.
The CLI equivalents are `mapbox-tui auth offline` and `mapbox-tui auth online`.

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
is saved for future launches, loads and selection changes.
Attribution stays in the footer.

Menus wrap. Rounded modal borders contain the current title and a breadcrumb
for nested dialogs. Esc goes back in nested dialogs and closes the top-level dialog.
On the map, Esc clears the selected `*` location without moving the camera or
unloading files or journeys. If a panel is open, the first Esc closes it; press
Esc again to clear the location.
Use `--ascii` if your terminal doesn't render Braille well. The minimum viewer
size is 30 columns by 12 rows.

## Saved viewer preferences

We save theme, ASCII, animation, elevation and the last map view in `settings.json`.
On Linux, that's `$XDG_CONFIG_HOME/mapbox-tui/settings.json`, or
`~/.config/mapbox-tui/settings.json` if XDG isn't set. Other systems use Go's user
configuration directory. Clearing a workspace keeps these choices. CLI renders
and MCP don't use them.

Explicit flags override saved choices for one launch, including `false`:

```sh
mapbox-tui --ascii=false --no-animation=false --theme=catppuccin --elevation=auto
```

Without saved choices, we use Catppuccin, Braille, animation and automatic elevation.

- `a` saves ASCII mode; `m` saves animation. `camera_animation: true` enables it.
- Enter confirms and saves a theme. Dismissing the picker restores the previous one.
- `e` saves elevation as `show` or `hide`. `--elevation=auto` follows the loaded
  data. Even `show` won't draw an empty chart.
- After navigation, a clean exit saves `last_view` as `zoom`, `lat` and `long`.
  Reopening restores it, even with a track or route. Explicit centre and zoom
  flags win. We don't restore bearing, loaded files or routes.

Saving one choice doesn't save unrelated launch overrides. Crashes don't save
camera changes. With several viewers, the last successful camera save wins.

To delete the saved centre and zoom, then start at the default view or fit a file:

```sh
mapbox-tui --reset-view
mapbox-tui --reset-view fixtures/example.gpx
```

Reset removes `last_view` immediately. Explicit camera flags still apply; later
navigation and a clean exit save a new view. If reset fails, startup stops with an error.

To reset other viewer choices, close viewers and remove `theme`, `ascii`,
`camera_animation` and `elevation` from the file. Removing `elevation` restores
`auto`. Remove legacy `no_animation` too, if present. Deleting the whole file also
resets traffic, telemetry, offline mode and billing acceptance, but keeps
favourites and credentials.

Older key names still work and migrate on the next save. The new names win if
both exist, including `false` and `0`. `mapbox_billing_accepted_version` records
which billing notice you've accepted. See [settings internals](ARCHITECTURE.md#shared-model-and-rendering)
for the field mappings and write rules.

Invalid or incompatible settings stay untouched; the viewer warns and uses
defaults. Details are in Context (Ctrl-B). Repair or move the file before saving.
An unavailable saved theme falls back to Catppuccin with a warning. Failed saves
keep the previous file; exit-save errors appear after the terminal is restored.

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

Saving an address or town makes one Mapbox Geocoding request using the selected
result’s ID, using the API’s default geocoding mode. The returned place details
are saved in `favourites.json`. Geocoding is billed separately from Search Box.
Mapbox credit is shown when reusing these locations offline. Named venues such
as cafés are not supported by that endpoint; Save explains this without asking
for coordinates.
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
Journeys support up to 5,000 stops, including start, destination and unset
waypoints. The same cap applies to editing, startup arguments, remote control
and saved workspaces. Walking, cycling and driving route calculations retain
their separate 25-stop limit; returned path points do not count as stops.

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

Offline mode plots straight lines between resolved locations. Connected plans
default to Driving and recalculate after location or mode edits. Routed distance
and travel time come from Mapbox; planned geometry has no recorded timestamps
or elevation. Plans remain in memory until you explicitly save a workspace.

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
defaults to `driving`, with `offline`, `walking` and `cycling` also supported.
The `offline` option plots straight lines. `manual` remains a compatibility alias.
These are interactive viewer options; offline geometry works locally, while routed
modes use your normal Mapbox connection. Named-place search remains available
inside the planner; startup arguments accept resolved coordinates.

CLI journeys in Driving, Cycling or Walking mode calculate automatically once
Mapbox is available, even if you switch the planner to another journey while
connecting. Offline mode keeps straight lines; failed requests wait for an explicit retry.


## Saved workspaces

Use **Ctrl-K → Save workspace** to name a checkpoint. The dialog explains that
imports remain references to their original files and that calculated road routes,
basemaps and traffic aren't saved. **Save workspace as** creates a separate
checkpoint with a new ID. Names may contain printable Unicode and aren't used as
storage paths. You can keep several saves; each viewer has one active workspace.

**Ctrl-K → Open saved workspace** opens the saved-workspace picker. Press Tab to choose a JSON
file elsewhere, or press P to enter a path. The picker lists saved workspaces
from the workspace directory, newest modified first. Up/Down selects a save and
shows its created/modified dates, import counts and shortened filenames on the same
screen. Browsing reads saved metadata only; import files are loaded and checked
when you press Enter. Missing or changed imports then open the repair screen.
PgUp/PgDown scrolls longer details; R refreshes the filesystem list.
Unreadable saves remain visible with an explanation. Enter opens the selected
save; Esc keeps your current work. If the current workspace has unsaved changes,
opening another save offers Save, Discard or Cancel. A failed or conflicting Save
keeps the current work. Discard drops only the in-memory changes.
Save is selected by default. Use Tab or Left/Right to choose Cancel, Discard changes
or Save, then Enter; the buttons also accept mouse clicks.

Press `/` to search names, then Enter or Esc to return to the filtered list.
Clear the search text to show all saves. Press O to switch between newest-first
and name ordering. The active checkpoint is highlighted and labelled `(active)`.
Duplicate or shortened names show a short ID to distinguish them.

Press M for the workspace actions menu. The direct shortcuts still work:
N renames the selected checkpoint, D duplicates it and Delete asks
for confirmation before deleting it. Rename preserves the ID and saved contents;
it doesn't save current unsaved edits. Duplicate copies the selected saved contents
with a new ID, including missing-file references. It doesn't copy unsaved edits or
switch the active workspace. S saves the current working copy; A opens Save As.
Save As creates a new checkpoint from the current working copy. Names can match
other saves and contain up to 256 printable Unicode characters; blank names are
rejected. Names never select a storage path or overwrite target.

Rename, Duplicate and Delete check for conflicting edits. On conflict, keep the
working copy, press Esc to return to the picker and R to refresh before retrying.
Deleting the active checkpoint leaves its working copy available as unsaved work.
The next Save asks for a name and creates a new ID. Deletion only removes the
selected checkpoint JSON file; original imports, preferences, favourites and other
saves stay untouched. The separate lock file is retained for concurrent writers.

You can also launch directly into a saved workspace:

```sh
mapbox-tui --workspace ~/.config/mapbox-tui/workspaces/WORKSPACE_ID.json
mapbox-tui --workspace /path/to/workspace.json --zoom 12 --no-provider
```

The workspace camera takes precedence over the global last view. Explicit launch
centre/zoom/bearing flags override the workspace camera for that launch. Opening a
save inside the viewer restores its camera without reapplying launch overrides.
`--workspace` cannot be combined with startup imports, journey options or item/point
selectors. Theme, ASCII, animation and elevation preferences stay global.

A checkpoint includes imported file references, multiple journeys and incomplete
drafts, ordered stop values with their provenance and attribution, travel modes,
visibility, selection, activity palette slots and camera. Saved drafts are available
in Files; select one and open Journey to continue editing it. Playback reopens
paused at the saved recorded point. Journey road geometry and travel estimates
are cleared, with a labelled straight-line preview until each complete road journey
is recalculated automatically when Mapbox is available. Failed requests wait for
**Recalculate route**; reconnecting does not repeatedly retry them. The planner
restores its saved journey, and background recalculation preserves the saved view.
Favourite locations are copied values; changing a favourite doesn't change a save.

On Linux saves live in `$XDG_CONFIG_HOME/mapbox-tui/workspaces/`, or
`~/.config/mapbox-tui/workspaces/`, one JSON file per stable ID. Other systems use
Go's user configuration directory. The save result shows its path. Imported paths
are written as absolute paths; relative paths in a manifest resolve beside that
manifest, independently of the process working directory. This isn't a portable
bundle: retain the original GPX/FIT/TCX/KML files.

Missing or changed imports appear in **Repair missing workspace files**.
Repair preserves playback of another selected activity. If you reopen a missing
file through **Open track file**, remove its unresolved reference before saving;
one path cannot be saved as both a loaded import and an unresolved reference.
Press Enter to open the file picker beside the original file's location. Tab switches
to a typed path; press Tab again to return to the picker. Ctrl-U clears the typed
path. Home/End and arrow keys move through long paths; `~/`
paths resolve from your home directory. **Skip
for now** keeps their references, item state and any unresolved selection when you
save again. **Locate original import** accepts the path of an identical copy. A
changed file never receives old item state; to adopt it, deliberately remove the
reference and add the replacement with Open track file. Removing a reference asks
for confirmation and never deletes its source. A failed open leaves current work
intact. Local restoration makes no provider requests.
Once the last reference is repaired or removed, the repair dialog closes.

Only explicit Save replaces a checkpoint's working contents. Rename changes its
name and revision/timestamp metadata; Duplicate and Save As create new IDs.
Clearing the workspace, quitting,
opening another save and repairing references don't update it automatically.
Save detects edits by another viewer or external editor; on conflict keep your
working copy with Save workspace as, or explicitly reopen the saved file. A deleted
checkpoint is not silently recreated. Unsupported formats, oversized files and
failed writes leave existing saves intact. Clearing the working workspace or
`--reset-view` does not delete checkpoints, preferences or favourites. Browsing,
searching, renaming and duplicating saved metadata don't call providers or parse
original imports. Autosave and automatic session/crash recovery remain deferred.

## Start clean, clear journeys and safe start

Use **Ctrl-K → Start clean** to empty the workspace. This clears imports,
journeys, missing-file references and selection, and cancels pending workspace
work. If there are unsaved changes, choose Save, Discard or Cancel before clearing.
Save must succeed before clearing; a cancellation or failure keeps the working
copy available. Your preferences, favourites and saved files stay as they are
unless you explicitly choose Save.
**Save workspace** then asks for a new name, so you won't overwrite the
checkpoint you had open.

Choose **Clear journey** in the Journey panel or command palette to remove the
journey you're editing. It still targets that journey when an imported activity
is selected on the map. Other journeys and imports stay in place. Save the
workspace if you want to keep the change. If you reopen the unchanged checkpoint,
its journey comes back.

Workspace saving is manual. Unsaved edits are lost when you close the viewer or
it crashes. Starting without files, journey options or `--workspace` opens an
empty workspace. Viewer preferences, including the last view saved on normal
exit, are separate from workspace saves.

If saved preferences are causing problems, bypass them for one launch:

```sh
mapbox-tui --safe-start
mapbox-tui --safe-start --ascii --no-provider fixtures/example.gpx
mapbox-tui --safe-start --workspace /path/to/workspace.json
```

Safe start uses defaults and your explicit launch flags. It doesn't read the
settings file or save the last view when you exit, so it also works when settings
are malformed. Files, journey options and saved workspaces work as usual. An
opened workspace supplies its camera unless you override it with camera flags.

Credentials and favourites stay untouched. If you change a preference in the UI,
the app still tries to save that change and reports any error. Safe start doesn't
repair or delete the settings file. You can't combine it with `--reset-view`,
which deliberately changes saved settings.

## Live viewer control

Start a named viewer, then run control commands from another terminal:

```sh
mapbox-tui --name main
mapbox-tui session list --json
mapbox-tui remote --session main status --json
```

Every live session has a UUID; names are optional aliases. Use `--no-control`
to disable discovery and control. MCP agents discover sessions with
`viewer_sessions` and pass a UUID as `session_id` to viewer tools.
See [remote access](REMOTE_ACCESS.md) for CLI commands, MCP setup and retry rules.
