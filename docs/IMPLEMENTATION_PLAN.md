# Map workspace redesign

Implement in small, independently verified steps. Start with route identity and
workspace ownership, then build the remaining TODO features on those foundations.

## Progress

Import identity is complete: importers now produce documents containing named,
independently prepared items. Source paths and filenames are retained separately;
item IDs are stable within their owning document. FIT sessions remain parts of an
activity, chained FIT sequences are separate items, and KML container ancestry is
retained even when folder names occur after their placemarks.

Workspace lifecycle is also complete at the model level: add/reopen, selection,
visibility, item/file removal, clearing, active playback/profile ownership and
visible bounds. Document IDs are not reused after removal or clearing, so old
item references cannot address later imports.

Existing single-file consumers use a combined snapshot during the migration.
Route counts now describe route-bearing items rather than disconnected parts.
Build, Go tests, vet and CLI/MCP validation pass for both slices; offline terminal
interaction checks passed for the import slice. Next: **step 3, viewer and tool
integration**, including the loaded-files controls.

## Direction

- A workspace contains imported documents and their routes, activities and other
  supported geometry. A file can contain several items; an item can contain several
  disconnected parts.
- Preserve source filenames and item names separately. Retain enough source
  identity to distinguish equally named files and remove an imported document.
- Visibility, active selection and camera follow are separate choices. Initially,
  only one item is the target for playback and the elevation profile.
- Keep imported geometry usable without a basemap or credentials.
- Keep CLI and MCP single-file behaviour compatible while extending capabilities.
  MCP and the viewer remain independent processes.
- Keep the existing gap-safe geometry, missing measurements, format validation,
  playback estimates and terminal rasteriser.
- No persistent workspace storage, simultaneous multi-activity replay or generic
  plugin framework in the initial work.

## 1. Preserve document and route identity

- [x] Establish document/item ownership, stable in-session identity and source
  metadata without coupling them to Bubble Tea or provider state.
- [x] Keep disconnected parts within their owning item; distinguish entity counts
  from part counts in summaries.
- [x] Preserve GPX track/route ownership and names, including waypoint-only files.
- [x] Preserve TCX activity/course ownership, with laps and GPS gaps remaining
  parts of their owning entity.
- [x] Define FIT activity/course and chained-sequence grouping from available
  source information; preserve timer pauses and missing-GPS breaks.
- [x] Preserve KML placemark identity and supported geometry grouping; retain
  available document/folder names without adding new KML format support.
- [x] Make derived geometry calculations safe to repeat: distance, bounds and
  projected coordinates must describe current geometry rather than accumulate.
- [x] Adapt existing single-file summary/inspection consumers, preserving public
  point ordering where possible and documenting deliberate count corrections.

**Acceptance:** One TCX course with a GPS gap counts as one route with separate
parts. Multiple named entities in one file remain distinguishable. Recomputing
geometry does not change its distance. Existing import validation and gap tests pass.

## 2. Introduce workspace lifecycle

- [x] Add documents without replacing already loaded data; failed imports leave
  the workspace unchanged.
- [x] Support active-item selection, visibility and removal at the model level.
- [x] Support removing an imported document together with its owned items.
- [x] Define what happens when the active item is hidden or removed; ensure
  selection, playback and profile cannot refer to removed data.
- [x] Keep item data and derived playback/profile state consistent when switching
  the active item.
- [x] Expose workspace operations for front ends to share rather than reproducing
  lifecycle rules in each front end.
- [x] Define duplicate-import behaviour and active/visible bounds handling without
  concatenating independent items or silently changing antimeridian limitations.

**Acceptance:** Load two files, select either, change visibility and remove one
without changing the other. Clearing the workspace leaves a usable map. Cover
failed imports, duplicate names and removal of the active item through model tests.

The workspace compares normalised absolute paths for duplicate imports, not
filenames. A new file selects its first item with track points, falling back to a
waypoint-only item. Selecting a hidden item reveals it; showing an item alone does
not select it. Switching items starts a fresh paused position and profile, while
reselecting the active item preserves its position. Removing the last item also
removes its file entry.

Visible bounds include waypoint-only items and may span more than 180 degrees
across separate files. They are combined as bounds, never as one continuous path.
Individual imports retain their existing antimeridian restrictions. Fit uses the
existing camera zoom limits; very wide extents can reach zoom 0 before fitting
fully in a narrow terminal.

## 3. Integrate multiple items with the viewer and tools

- [x] Confirm file-open and camera-fit behaviour before changing interactions.
- [ ] Resolve the zoom-0 limit for fit-all when widely separated routes cannot fit
  inside a narrow terminal.
- [ ] Separate meaningful map roles from drawing priority, colour and ASCII glyphs.
- [ ] Compose visible items and basemap features through shared scene rules,
  retaining the existing terminal grid implementation.
- [ ] Make picking identify both the item and its point; distinguish the active
  item in rendering and details.
- [ ] Connect playback/profile to the active item; keep camera follow independent.
- [ ] Define predictable overlap and label precedence within the terminal's
  one-foreground-colour-per-cell constraint.
- [ ] Extend CLI/MCP selection and rendering capabilities deliberately; add an MCP
  release operation so loaded documents need not remain until process exit.
- [ ] Separate transport inputs from live viewer settings as these workflows move
  behind shared operations; use explicit result structures where contracts change.
- [ ] Run interaction checks and benchmark representative multiple-track scenes
  before introducing rendering caches or spatial indexes.

**Acceptance:** Multiple items can appear together, inspection identifies the right
item, and only the active item's profile/playback is used. Existing single-file
CLI/MCP calls still work. Plain output stays ANSI-free.

### Loaded-files UI follow-up

This is deliberately tracked separately from the initial model work. Preserve the
metadata and lifecycle operations now so the UI can be added without another
data-model redesign. Before shipping interactive multi-file loading, provide at
least a minimal way to identify and remove loaded data.

- [ ] Choose a compact surface: command-palette entries, a dialog or a sidebar.
- [ ] Show loaded filenames separately from contained route/activity names.
- [ ] Distinguish duplicate filenames and indicate active, visible and hidden items.
- [ ] Allow selection and visibility changes without re-importing data.
- [ ] Surface removal of loaded files; decide whether individual contained items
  also need their own removal action.
- [ ] Provide an explicit clear-workspace action; map browsing itself need not
  discard loaded data.
- [ ] Keep controls usable in narrow terminals and reachable by keyboard.

**Acceptance:** A user can tell what is loaded and remove a file without losing
unrelated files. Merely browsing the map does not obscure ownership of loaded data.

## 4. Give data sources independent loading and refresh

- [ ] Move scheduling, relevance checks and cancellation alongside source loading;
  reject results for superseded requests/configuration.
- [ ] Track status and freshness per source rather than one global busy/error state.
- [ ] Add refresh deadlines for sources that need updates while the camera is still.
- [ ] Preserve cache directives, bounded caches, backoff and request/body limits.
- [ ] Compose attribution from contributing sources; keep imported items visible
  through source failures.
- [ ] Test delayed responses, cancellation, expiry and rate limiting with fake
  transport/controlled time; update provider documentation for changed policies.

**Acceptance:** One failed or stale source does not block another. Old responses
cannot replace current source state. Eligible sources refresh without camera motion.

## 5. Centralise theme ownership

This can be a smaller parallel feature after scope is confirmed. Terminal
recolouring is distinct from selecting a different basemap style or tileset.

- [ ] Confirm whether theme switching covers terminal appearance, map data style,
  or both; implement the chosen scope explicitly.
- [ ] Own the selected presentation theme in viewer state and derive styles from
  named roles rather than scattered globals/literals.
- [ ] Apply changes to the map, profile, dialogs, progress bar, borders, background
  and already-created Bubbles widgets.
- [ ] Check wide/narrow layouts and ASCII mode; a palette-only change must not
  re-import files or fetch tiles.

**Acceptance:** Switching themes with a dialog and playback UI already open updates
all visible presentation consistently.

## 6. Build the remaining features on these boundaries

### Route plotting / directions

- [ ] Decide manual plotting versus road-following directions, and travel modes
  if directions are required.
- [ ] Model editable planned routes separately from recorded activities, while
  sharing geometry and workspace display.
- [ ] Define edit/undo behaviour and recompute or invalidate derived geometry,
  profiles and playback data together.
- [ ] For directions, choose an actual routing source and handle ordered stops,
  manoeuvres, request cancellation and failures. Visual Streets tiles are not a
  routing graph.
- [ ] Add resulting routes to the workspace without inventing recorded timestamps
  from estimated journey times.

### Traffic

- [ ] Choose congestion, incidents or both, and the source providing that data.
- [ ] Define refresh/staleness expectations and the terminal display vocabulary.
- [ ] Add traffic as an independently toggleable overlay, with its own status and
  attribution, without replacing the basemap or imported items.

### Other sources

- [ ] Pick one concrete dataset/display use case before adding more abstractions.
- [ ] Add only the geometry, properties, freshness and inspection behaviour that
  dataset requires, using the scene/source boundaries above.

## Verification per implementation slice

- Run focused behavioural tests for the changed ownership or interaction rules.
- Run `MAPBOX_ACCESS_TOKEN='' mise exec go@1.27.1 -- make test` for build, Go tests,
  vet and CLI/MCP validation.
- Run the existing terminal interaction checks when viewer behaviour changes.
- Update README, tool schemas and validation/provider notes alongside observable
  behaviour changes. Mark tasks complete only once their acceptance checks pass.

## Confirmed interaction decisions

- Opening another file adds it to the workspace, makes the newly loaded item
  active and fits the camera to it. Replacing all loaded data is explicit.
- `f` fits all visible routes, rather than only the active item.
- Reopening an already loaded path selects the existing import and restores its
  visibility. It does not read changed file contents again; explicit reload can
  be added separately.
- Hiding or removing the active item stops playback and clears active selection
  and profile. It does not automatically select another item or move the camera.

Later decisions belong to their implementation slice: loaded-files UI layout,
plotting versus routing, traffic refresh policy and theme scope.
