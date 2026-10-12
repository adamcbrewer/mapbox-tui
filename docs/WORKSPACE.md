# Workspace rules

The viewer, multi-file CLI rendering and MCP share lifecycle and scene rules.
Each process owns its own workspace; CLI renders are temporary and MCP state
lasts for that server process. Single-file CLI/MCP operations retain their
combined-file defaults unless an item is explicitly selected.
See [architecture](ARCHITECTURE.md) for responsibility boundaries and source links.

## Imports and identity

- An imported document owns named items, each containing geometry parts and/or
  waypoints. Disconnected parts remain separate; gaps never become connecting paths.
- Source paths/filenames and item names are separate. Item IDs are stable within
  their document; `ItemRef` pairs a document ID with an item ID.
- Workspace document IDs aren't reused after removal or clearing. Stale references
  cannot select later imports.
- Duplicate imports compare normalised absolute paths. Reopening selects the
  existing import and restores visibility; it doesn't reread changed file contents.
- A failed import preserves existing data. A new import selects its first item
  with track points, falling back to a waypoint-only item, and the viewer fits it.
- Each file keeps the importer's antimeridian restrictions. Separate files may
  span wider bounds without becoming a wrapped or continuous route.

## Selection, visibility and camera

- Selecting an item reveals it. Showing an item alone doesn't select it.
- Switching active items starts a fresh paused position. Reselecting the active
  item preserves its position.
- Hiding or removing the active item clears selection, stops playback and removes
  its profile cursor. Other visible profiles remain; another item isn't selected
  automatically and the camera stays in place.
- Removing a document removes its items. Removing its last item removes the
  document entry. Clearing the workspace preserves the camera.
- Free roam clears selection and playback while keeping imported geometry visible.
- Fit-visible includes visible geometry and waypoints, including original planned
  stops and routed geometry. Camera zoom supports -8 through 20 for wide extents;
  a file-free world view starts at zoom 0.
- Camera follow is separate from selection/playback. Starting playback preserves
  the view; explicit centre/follow enables it, and manual pan disables it.

Current keyboard and mouse actions are in the [README controls](../README.md#controls).

## Scene, playback and profiles

- Item colours use stable palette slots. Selection highlights the active item
  without changing file/item order or ownership. Themes resolve those slots at
  painting time; recolouring preserves imports and playback.
- Shared drawing priorities decide overlaps, labels and markers. Picking follows
  the rendered cell's ownership in Braille and ASCII modes.
- Only the active recorded item plays. Recorded measurements stay item-local;
  missing timestamps/elevations remain unavailable. Disconnected parts aren't joined.
- Visible elevation sections appear consecutively in file-load/item order, with
  a summed distance axis, common elevation scale and gaps for missing measurements.
  The active cursor includes earlier sections' offsets.
- Plain CLI/MCP output stays ANSI-free. Rendering errors or missing credentials
  leave imported geometry usable. Source snapshots carry their own status and credits.

Explicit activity ordering and sequential playback remain in
[#3](https://github.com/adamcbrewer/mapbox-tui/issues/3).

## Planned journeys and external sources

- Planned items share workspace identity, visibility, colour and scene rules while
  remaining separate from imported recorded data. They are session-only and have
  no invented recorded timestamps, playback or elevation.
- Manual coordinates and map-centre input work offline. Unresolved stops leave
  gaps. Full reversal reverses the stop order as one undoable edit.
- Routing keeps user stops, provider-snapped coordinates and resolved geometry
  separate. Edit/reversal/undo invalidates derived route estimates and recalculates
  the current intent; reversing a returned polyline isn't sufficient.
- Cancel obsolete requests and reject stale results using item/request revisions,
  including after removal, clearing or credential replacement. Failed routing
  preserves stops and offers manual geometry with feedback, without stale estimates.
- Imported playback and profiles survive planned-route edits and source failures.
  Camera movement and stationary basemap refresh don't trigger search or Directions.
- Each source owns cancellation, generations, snapshots and freshness independently.
  Shared scene rendering combines its features and credits with local geometry.

See [provider policies](PROVIDERS.md) for search/routing behaviour, cache rules,
account requirements and attribution; [validation](VALIDATION.md) describes the
regression checks. Favourites/caching and estimated terrain profiles remain in
[#10](https://github.com/adamcbrewer/mapbox-tui/issues/10) and
[#13](https://github.com/adamcbrewer/mapbox-tui/issues/13).
