# Map workspace redesign

Implement in small, independently verified steps. Start with route identity and
workspace ownership, then build the remaining TODO features on those foundations.

## New-thread handover

- Continue on `feat/map-workspace`, tracking `origin/feat/map-workspace`.
- Steps 1-5 are complete. Resume at **step 6**, confirming manual route plotting
  versus road-following directions and travel modes before implementing routing.
- Theme scope is terminal recolouring. Three Omarchy-derived TOML palettes are
  embedded; `--theme` selects a built-in ID or custom palette file. Ctrl-T cycles
  themes and Ctrl-P opens the picker. [Theme API](THEMES.md) records the format,
  registry and viewer-owned style application.
- The [confirmed interaction decisions](#confirmed-interaction-decisions) remain
  the baseline. The remaining feature decisions are listed under step 6.
- [Validation](VALIDATION.md) records check commands. Go runs through
  `mise exec go@1.27.1 -- ...` in this environment. Current source-lifecycle checks
  use fake providers; live Mapbox refresh has not been exercised for this slice.
- [Provider notes](PROVIDERS.md) contain refresh/cache policy and the existing
  unresolved graphical-attribution limitation. Terminal captures under `reports/`
  are local ignored artefacts; the implementation and handover are tracked here.

## Progress

Import identity is complete: importers now produce documents containing named,
independently prepared items. Source paths and filenames are retained separately;
item IDs are stable within their owning document. FIT sessions remain parts of an
activity, chained FIT sequences are separate items, and KML container ancestry is
retained even when folder names occur after their placemarks.

Workspace lifecycle is complete: add/reopen, selection,
visibility, item/file removal, clearing, active playback ownership, workspace
elevation profiles and visible bounds. Document IDs are not reused after removal
or clearing, so old item references cannot address later imports.

Viewer and tool integration is complete. Ctrl-L shows loaded files and items,
with selection, visibility and removal controls. The viewer uses workspace-owned
playback/profile state, draws all visible items and fits them with `f`. CLI
multi-file rendering and item selectors share the scene renderer with MCP, which
now provides listing, selection, visibility and removal tools. Existing single-file
CLI/MCP calls retain their combined-file defaults.

Build, Go tests, vet, race checks, CLI/MCP validation and offline terminal
interaction checks pass. The synthetic scene benchmark measured roughly 5.5 ms
for one 100k-point route and 21.8 ms for four at 120×40 cells on this machine.
No spatial index or rendering cache was introduced.

Independent source loading and refresh is complete. Each enabled source has its
own cancellable requests, generation checks, snapshot and refresh deadline.
Stationary refresh honours cache freshness and backoff; uncacheable responses
use a 60-second fallback. The viewer, CLI and MCP consume source snapshots and
combine their credits. Controlled-clock/provider tests, race checks, the full
repository checks and offline terminal interactions pass. Mapbox remains the only
external source; this does not add traffic data.

Centralised theme ownership is complete. The viewer selects a data-only TOML
palette and restyles open widgets in place. Workspace activities and renderer cells
retain colour slots/roles rather than RGB values, so map/profile colours resolve
from the selected palette without reimporting, refetching or resetting playback.
The three built-ins are Catppuccin (dark/default), Catppuccin Latte (light) and Tokyo
Night (dark), with custom-file loading through the same API. Palette validation,
state-preservation tests, repository checks, race checks and offline terminal
interactions pass at wide/compact sizes, including open dialogs and ASCII playback.

Next: **step 6, route plotting / directions**. Confirm manual plotting versus
road-following directions before implementing it. Routing/traffic sources still
need decisions in their respective slices.

Pre-PR review fixes preserve startup camera overrides across queued imports,
consume them on import failure, and keep the current camera when active items are
hidden/removed during animation. Picking follows the displayed cell's item;
Mapbox request relevance follows tile coverage so useful moving-view batches can
complete. Regression tests, repository checks, race and terminal interactions pass.

Multi-activity presentation uses stable per-item colours and consecutive elevation
sections in file-load/item order, with a summed distance axis and activity-local
playback. Selection highlights an activity without changing that order. Explicit
reordering and journey-wide playback are deferred to
[issue #3](https://github.com/adamcbrewer/shell-on-earth/issues/3); they do not change
the next implementation step.

## Direction

- A workspace contains imported documents and their routes, activities and other
  supported geometry. A file can contain several items; an item can contain several
  disconnected parts.
- Preserve source filenames and item names separately. Retain enough source
  identity to distinguish equally named files and remove an imported document.
- Visibility, active selection and camera follow are separate choices. Initially,
  only one item is the target for playback. Elevation profiles show visible
  activities consecutively in load order.
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
not select it. Switching items starts a fresh paused position, while
reselecting the active item preserves its position. Removing the last item also
removes its file entry.

Visible bounds include waypoint-only items and may span more than 180 degrees
across separate files. They are combined as bounds, never as one continuous path.
Individual imports retain their existing antimeridian restrictions. Camera zoom
now permits -8 through 20 so wide extents can fit in narrow terminals without a
jump to zero on the next wheel event. File-free browsing still starts at zoom 0.

## 3. Integrate multiple items with the viewer and tools

- [x] Confirm file-open and camera-fit behaviour before changing interactions.
- [x] Resolve the zoom-0 limit for fit-all when widely separated routes cannot fit
  inside a narrow terminal.
- [x] Separate meaningful map roles from drawing priority, colour and ASCII glyphs.
- [x] Compose visible items and basemap features through shared scene rules,
  retaining the existing terminal grid implementation.
- [x] Make picking identify both the item and its point; distinguish the active
  item in rendering and details.
- [x] Connect playback and the profile cursor to the active item; show visible
  profiles in load order and keep camera follow independent.
- [x] Define predictable overlap and label precedence within the terminal's
  one-foreground-colour-per-cell constraint.
- [x] Extend CLI/MCP selection and rendering capabilities deliberately; add an MCP
  release operation so loaded documents need not remain until process exit.
- [x] Separate transport inputs from live viewer settings as these workflows move
  behind shared operations; use explicit result structures where contracts change.
- [x] Run interaction checks and benchmark representative multiple-track scenes
  before introducing rendering caches or spatial indexes.

**Acceptance:** Multiple items can appear together, inspection identifies the right
item, visible elevation profiles occupy consecutive distance sections, and only
the active item plays back. Existing single-file
CLI/MCP calls still work. Plain output stays ANSI-free.

### Loaded-files UI

Implemented as a Ctrl-L dialog, also available in the command palette. File
headings and item rows share keyboard navigation; IDs and source paths distinguish
duplicate filenames. Delete removes the selected row and `D` removes its file.

- [x] Choose a compact surface: command-palette entries, a dialog or a sidebar.
- [x] Show loaded filenames separately from contained route/activity names.
- [x] Distinguish duplicate filenames and indicate active, visible and hidden items.
- [x] Allow selection and visibility changes without re-importing data.
- [x] Surface removal of loaded files; decide whether individual contained items
  also need their own removal action.
- [x] Provide an explicit clear-workspace action; map browsing itself need not
  discard loaded data.
- [x] Keep controls usable in narrow terminals and reachable by keyboard.

**Acceptance:** A user can tell what is loaded and remove a file without losing
unrelated files. Merely browsing the map does not obscure ownership of loaded data.

## 4. Give data sources independent loading and refresh

- [x] Move scheduling, relevance checks and cancellation alongside source loading;
  reject results for superseded requests/configuration.
- [x] Track status and freshness per source rather than one global busy/error state.
- [x] Add refresh deadlines for sources that need updates while the camera is still.
- [x] Preserve cache directives, bounded caches, backoff and request/body limits.
- [x] Compose attribution from contributing sources; keep imported items visible
  through source failures.
- [x] Test delayed responses, cancellation, expiry and rate limiting with fake
  transport/controlled time; update provider documentation for changed policies.

**Acceptance:** One failed or stale source does not block another. Old responses
cannot replace current source state. Eligible sources refresh without camera motion.

## 5. Centralise theme ownership

Confirmed scope: terminal recolouring, using data-only TOML palettes and a shared
registry/API. Basemap-style/tileset switching is separate.

- [x] Confirm whether theme switching covers terminal appearance, map data style,
  or both; implement the chosen scope explicitly.
- [x] Own the selected presentation theme in viewer state and derive styles from
  named roles rather than scattered globals/literals.
- [x] Apply changes to the map, profile, dialogs, progress bar, borders, background
  and already-created Bubbles widgets.
- [x] Check wide/narrow layouts and ASCII mode; a palette-only change must not
  re-import files or fetch tiles.

**Acceptance:** Switching themes with a dialog and playback UI already open updates
all visible presentation consistently.

## 6. Build the remaining features on these boundaries

### Activity reordering (deferred)

Tracked in [issue #3](https://github.com/adamcbrewer/shell-on-earth/issues/3).
Keep this separate from the current presentation fixes and theme slice.

- [ ] Decide whether users reorder whole files, contained activities or activities
  across files, and how source grouping remains visible.
- [ ] Give the workspace an explicit order using stable item references; use it
  consistently in the list, legend and elevation chart.
- [ ] Define visibility and playback behaviour during reorder/removal, preserving
  activity-local measurements and positions where possible.
- [ ] Confirm selected-activity versus journey-wide playback before adding handoffs,
  combined progress or cross-activity seeking.

### Route plotting / directions

Tracked in [issue #5](https://github.com/adamcbrewer/shell-on-earth/issues/5).

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

Tracked in [issue #4](https://github.com/adamcbrewer/shell-on-earth/issues/4).

- [ ] Choose congestion, incidents or both, and the source providing that data.
- [ ] Define refresh/staleness expectations and the terminal display vocabulary.
- [ ] Add traffic as an independently toggleable overlay, with its own status and
  attribution, without replacing the basemap or imported items.

### Other sources

Dataset/use-case discovery is tracked in
[issue #6](https://github.com/adamcbrewer/shell-on-earth/issues/6).

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
  and its profile cursor. Other visible profiles remain. It does not automatically
  select another item or move the camera.

Later decisions belong to their implementation slice: plotting versus routing,
traffic refresh policy. Terminal theme scope is confirmed above.
