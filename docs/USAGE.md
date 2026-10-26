# Usage

Keyboard, mouse and CLI reference. Commands assume `mapbox-tui` is on your
PATH; from this checkout use `./bin/mapbox-tui`. For MCP and automation, see
[Agent usage](AGENT_USAGE.md).

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

For login using a browser on another device, a known public token or a transferred
CLI login, see [remote authentication](MAPBOX.md#logging-in-on-a-machine-without-a-browser).

## Map and panels

Keys apply to the focused view. Hide Files or Journey with Ctrl-B for keyboard
map navigation. Ctrl-K opens Commands from any panel or dialog.

| Action | Keyboard | Mouse / notes |
|---|---|---|
| Open imports | Ctrl-O or `o` | Adds files; reopening a loaded path reuses it |
| Files / Journey | Ctrl-L / Ctrl-J | Opens or focuses the panel |
| Quick place search | `/` | Coordinates work offline |
| Commands / shortcuts | Ctrl-K / `?` | Type to filter Commands; Up/Down or Ctrl-P/Ctrl-N, then Enter |
| Toggle command option | Space in Commands after navigating | Keeps Commands open |
| Toggle sidebar | Ctrl-B | Context, Files and Journey share one panel |
| Cycle sidebar tabs | Tab / Shift-Tab | Click a header tab |
| Pan | Arrows or `h j k l` | Left-drag; manual pan disables track follow |
| Zoom | `+` or `=` / `-` | Wheel zooms at pointer; Ctrl/Cmd-wheel doubles speed |
| Fit visible content | `f` or `r` | World view when nothing visible |
| Rotate / north-up | `,` / `.` / `n` | 10° steps; right-drag rotates |
| Inspect position | Enter | Click selects coordinates or a nearby recorded point |
| Centre / follow | `c` | Centres selected `*` location; otherwise centres and follows recording |
| Elevation / ASCII / animation | `e` / `a` / `m` | Elevation needs visible recorded data; choices persist |
| Traffic overlay | Ctrl-K → Toggle traffic | Connected Mapbox feature; [traffic behaviour](MAPBOX.md#traffic-v1-overlay) |
| Clear item selection | Ctrl-K → Free roam (clear selection) | Keeps imported geometry visible |
| Map credits | `v` | Attribution also stays in footer |
| Back / close / clear location | Esc | Nested dialog: back; top-level panel: close; map: clear `*` |
| Quit | Ctrl-C or `q` outside text entry | Unsaved workspace edits are lost |

## Loaded files and playback

Ctrl-L manages imported files and items. Only the active recording plays;
hiding or removing it stops playback. Context uses seek keys to scroll instead.

Ctrl-L lists activities in sequence, including hidden items, with their source
filenames. Files containing multiple tracks show `Track 1`, `Track 2`, etc. beside
each track's name; single-track files show the name alone. Waypoints don't count
as extra tracks, and hiding or reordering tracks doesn't change their numbering.
Alt-Up/Alt-Down moves the highlighted activity, including across files. Moving
pauses playback without changing the current activity or its recorded position.
The legend and elevation chart follow the same
order; hidden activities take no distance on the chart and return to their place
when shown. Activity order is included in saved workspaces.

| View | Action | Keys / behaviour |
|---|---|---|
| Import picker | Browse / open / cancel | Arrows; Enter; Esc |
| Files | Choose row | Up/Down or `k`/`j`; Home/End for first/last; wheel scrolls |
| Files | Select / show or hide item | Enter / Space |
| Files | Reorder activity | Alt-Up / Alt-Down; stops at the first/last row |
| Files | Show or hide whole source file | `v`; applies the highlighted item's new visibility to its source file |
| Files | Remove item / whole file | Delete or Backspace / Shift-D |
| Map | Previous / next recorded point | `[` / `]`; pauses playback |
| Map | Seek 100 points / first or last | PgUp/PgDn / Home/End |
| Map | Play / pause | Space; pan and zoom keep playback running |
| Map | Speed picker / cycle speed | `p` / `s` or Shift-S; 1×, 5×, 20×, 60×, 120× |
| Context | Scroll details | PgUp/PgDn, Home/End or wheel; arrows still pan |
| Playback | Timing | Recorded timestamps where complete and increasing; otherwise labelled 6 m/s estimate |
| Profiles | Comparison | Visible activities share a distance axis; missing elevation and gaps stay blank |
| Map | Markers | `*` location; `@` recording position; `S`/`E` part endpoints; `W` waypoints; `~` estimated values |

## Search and journeys

`/` inspects places; Ctrl-J edits a journey. Both accept latitude,longitude
offline. Named search needs Mapbox and at least three characters.

| Context | Action | Keys / menu |
|---|---|---|
| Search / stop entry | Choose / apply location | Up/Down, then Enter; or type coordinates and Enter |
| Search / stop entry | Use map centre | Alt-M |
| Search / stop entry | Pick on map | Ctrl-P, then click or Enter for map centre; Esc cancels |
| Search / stop entry | Refresh named search | Ctrl-S; also resumes an expired/interrupted search |
| Journey | Edit selected stop / action | Up/Down or `k`/`j`, then Enter; Home/End jumps through menu |
| Journey | Add waypoint | Ctrl-N, or Add another waypoint; committed once a location is chosen |
| Journey | Reverse / undo | Ctrl-R / Ctrl-Z |
| Journey | Clear endpoint / remove waypoint | Delete or Backspace |
| Journey | Reorder waypoint | Alt-Up / Alt-Down; cannot cross endpoints |
| Journey | Pick selected stop on map | Ctrl-P, then click or Enter; Esc cancels |
| Journey | Travel mode | Select Travel mode, then Enter; Offline, Walking, Cycling or Driving |
| Journey | Retry route / diagnostics | Ctrl-D / Ctrl-U |
| Journey | Copy launch command | Copy journey to clipboard, then Enter; all stops must be set |
| Journey | Remove current journey | Clear journey in panel or Ctrl-K; other content stays |
| Journey | Keep changes | Ctrl-K → Save workspace; closing the panel alone doesn't save |
| Clipboard | Requirements | OSC 52-capable terminal; allow clipboard writes through tmux; POSIX shell quoting |
| Copied command | Contents | Resolved stops, travel mode and import paths; road routes recalculate; labels become coordinates |
| Routing | Recalculation | Complete road journeys calculate when Mapbox becomes available; failed routes need explicit retry |

## Favourite places

Local library, usable offline. Search saves support addresses and towns;
named venues such as cafés aren't supported. Address saves make a separately
billed Geocoding request. See [Mapbox policies](MAPBOX.md#journey-location-lookups).

| Context | Action | Keys / behaviour |
|---|---|---|
| Search / stop entry | Save / remove selected favourite | Alt-S; coordinates prompt for a name |
| Map | Save selected location | Ctrl-K → Save selected location as favourite |
| Search / stop entry | Open library | Ctrl-F; also Ctrl-K → Favourite places |
| Favourites | Filter / choose / use | Type; Up/Down; Enter revisits location or fills edited stop |
| Favourites | Rename | Ctrl-R; non-blank name, unique ignoring case |
| Favourites | Delete | Delete or Backspace asks confirmation; Backspace edits filter until a result is focused |
| Confirmation | Choose / confirm / cancel | Tab or Left/Right; Enter or Space; Esc cancels; Cancel initially focused |
| Storage | Keep across sessions | `favourites.json` beside settings; clearing workspace leaves library intact |
| Journeys | Reuse a favourite | Copies location; later library changes don't alter the stop |

## Saved viewer preferences

Theme, ASCII, animation, elevation and last centre/zoom persist in `settings.json`.
On Linux: `$XDG_CONFIG_HOME/mapbox-tui/`, falling back to `~/.config/mapbox-tui/`.
Other systems use Go's user configuration directory. CLI renders and standalone
MCP keep their own defaults.

| Setting / action | Control | Behaviour |
|---|---|---|
| Theme | Ctrl-K → Colour theme | Up/Down previews, Enter saves, Esc restores previous theme |
| ASCII / animation | `a` / `m` | Saves UI choice; defaults Braille and animation on |
| Elevation | `e`; `--elevation auto`, `show` or `hide` | Auto follows visible recorded data; no empty charts |
| Temporary launch choices | `--theme nord --ascii=false --no-animation=false` | Explicit flags override saved choices for one launch |
| Last view | Navigate, then quit normally | Saves centre/zoom, including with files loaded; no bearing or workspace content |
| Reset last view | `mapbox-tui --reset-view [FILE]` | Deletes saved centre/zoom immediately; later navigation may save a new view |
| Bypass preferences | `mapbox-tui --safe-start` | Defaults plus explicit flags; files/workspaces still load; skips exit-time camera save; UI preference edits still try to save |
| Safe-start conflicts | `--safe-start --reset-view` | Rejected; safe start doesn't repair/delete settings or reset credentials |
| Invalid settings | Open Context for warning | File left untouched; defaults used; repair before saving preferences |

## Saved workspaces

Saving is manual. No autosave or automatic session/crash recovery. Saves reference
original import files; retain them. Basemaps, traffic and calculated road geometry
aren't saved. Normal startup opens empty unless given files, journey options or
`--workspace`.

| Context | Action | Keys / behaviour |
|---|---|---|
| Commands | Save / Save As / open | Ctrl-K → Save workspace / Save workspace as / Open saved workspace |
| Saved picker | Select / open | Up/Down or `k`/`j`; Enter opens; Esc keeps current work |
| Saved picker | Search / sort / actions | `/` filters names; Enter/Esc keeps filter; `o` changes ordering; `m` opens actions |
| Saved picker | Rename / duplicate / delete | `n` / `d` / Delete or Backspace; delete asks confirmation |
| Saved picker | Save working copy / Save As | `s` / `a`; lowercase, without Ctrl |
| Saved picker | Refresh / scroll details | `r` / PgUp/PgDn or Home/End |
| Saved picker | Browse JSON / type path | Tab / `p` |
| Replacement prompt | Save / discard / cancel | Tab or arrows, then Enter; Save initially selected; `s`/`d`/`c` also work |
| Save conflict | Save As / reopen checkpoint | `s` / `r` in conflict dialog; working copy preserved on failure |
| Commands | Reopen repair screen | Ctrl-K → Repair missing workspace files; available while references remain unresolved |
| Repair missing files | Locate / remove reference | Enter opens picker; Delete/Backspace asks to remove reference only |
| Repair path entry | Browse / type / clear | Tab switches; Ctrl-U clears typed path; arrows/Home/End edit; Esc skips |
| Changed import | Adopt new contents | Remove old reference, then import changed file; locate accepts identical copy |
| Start clean | Ctrl-K → Start clean | Empties working copy, offers Save/Discard/Cancel, detaches checkpoint; next Save creates new ID |
| Save contents | Files, drafts, stops, visibility, selection, camera | Multiple journeys, including incomplete drafts; recordings reopen paused |
| Road journeys | Open save | Local content first; straight-line preview until recalculated; failed requests need retry |
| Rename / duplicate / Save As | Saved metadata / saved copy / working copy | Rename keeps ID; Duplicate and Save As create IDs; Duplicate excludes unsaved edits |
| Delete active save | Keep working copy | Removes checkpoint JSON only; next Save creates new ID; source files stay |
| Storage | Find checkpoints | `workspaces/` under config directory; Save reports exact path; names aren't paths |

## CLI reference

Use `--help` per command. Options may go before/after files; use `--` before a
filename beginning with `-` or matching a command name. `FILE`, `ID` and paths
below are placeholders. `summary`/`inspect` return JSON; maps are ANSI-free.

| Use case | Command / options | Notes |
|---|---|---|
| Open files | `mapbox-tui ride.gpx course.tcx` | `open` is an optional command alias; repeated `--file PATH` also works |
| Select item / point | `mapbox-tui FILE --item-id ID --point-index 3` | Single file only; IDs from `summary --items` |
| Offline journey | `mapbox-tui --mode offline --start 51.5,-0.1 --waypoint 51.51,-0.11 --end 51.52,-0.12` | Repeat waypoints in travel order; both endpoints required; imports may be added |
| Road journey | `mapbox-tui --mode cycling --start 51.5,-0.1 --end 51.52,-0.12` | Walking, Cycling, Driving (default); `manual` remains alias for `offline` |
| Saved workspace | `mapbox-tui --workspace /path/save.json` | Cannot combine with files, journey options or item/point selectors |
| Named viewer / disable control | `mapbox-tui --name main` / `mapbox-tui --no-control` | Mutually exclusive; local live control requires Unix |
| Camera position | `--center-lat 51.5 --center-lon -0.1 --zoom 12 --bearing 45` | Centre coordinates paired; explicit camera flags override saved/workspace camera at launch |
| Cell proportions | `--cell-aspect 2` | Viewer/render/bench; range 1..3 |
| File summary | `mapbox-tui summary FILE` | Whole file includes planned routes as well as recordings |
| List items / summarise one | `mapbox-tui summary FILE --items` / `--item-id ID` | Choose one; item IDs are positive |
| Inspect recorded point | `mapbox-tui inspect FILE --item-id ID --point-index 3` | Zero-based index; defaults to point 0 if selector omitted |
| Inspect by distance | `mapbox-tui inspect FILE --item-id ID --distance-m 1000` | Nearest recorded point; choose index or distance, not both |
| Offline text map | `mapbox-tui render FILE --no-provider --ascii --width 60 --height 20` | Multiple files supported; no files gives basemap/empty canvas |
| Map and metadata | `mapbox-tui render FILE --no-provider --json` | `text` plus `scene`; single-file item/point selectors supported |
| Map dimensions | `--width 60 --height 20` | Render/bench only; terminal cells |
| Basemap off | `--no-provider` | Viewer/render; search and traffic separate; legacy `--provider none` also accepted |
| Mapbox login / logout | `mapbox-tui auth login` / `mapbox-tui auth logout` | Choose public token or browser; CLI needed for browser login; [setup and billing](MAPBOX.md) |
| Account status / usage | `mapbox-tui auth status --json --usage` | Omit `--usage` for status only; may make an online basemap probe |
| Persistent offline / online | `mapbox-tui auth offline` / `mapbox-tui auth online` | Online requires billing acknowledgement |
| Explicit billing acceptance | `mapbox-tui auth online --accept-billing` | Also accepted by `auth login`; review billing guidance first |
| Live sessions | `mapbox-tui session list --json` / `mapbox-tui session inspect main --json` | Ephemeral names/UUIDs; closing viewer ends session |
| Live control | `mapbox-tui remote --session main status --json` | [CLI/MCP action table](AGENT_USAGE.md#live-viewer-commands) |
| MCP server | `mapbox-tui mcp` / `mapbox-tui mcp --session main` | [Agent setup](AGENT_USAGE.md#setup) |
| Synthetic benchmark | `mapbox-tui bench FILE --frames 100` | Developer timings; [measurement limits](ARCHITECTURE.md#run-benchmarks-and-retain-results) |
| Version / help | `mapbox-tui --version` / `mapbox-tui COMMAND --help` | Version takes no other arguments |

## Limits

Import and measurement rules shared by viewer, CLI and MCP.

| Area | Limit / behaviour |
|---|---|
| Formats | GPX, FIT, TCX, plain KML; no KMZ, sensor metrics, KML styles/overlays/network links |
| Imported item | 100,000 coordinates across segments and attached waypoints; exceeding cap rejects whole file; no thinning on import |
| Journey stops | 5,000 including endpoints/unset waypoints; provider road routing limited to 25 |
| Geography | Individual imports reject antimeridian crossings and bounds wider than 180°; Mercator display |
| Measurements | Metres, zero-based indices, bounds `[west, south, east, north]`; distances exclude gaps; spherical Haversine |
| Camera / output | Zoom -8..20; bearing -360..360°; render width 10..300, height 5..100 cells; viewer minimum 30×12 |
| Map detail | ASCII labels; basemap tile detail stops at zoom 14; no offline basemap download or persistent disk tile cache |
| Navigation / platforms | No live GPS/voice guidance; Linux terminal checks; [platform limitations](../README.md#platforms) |
