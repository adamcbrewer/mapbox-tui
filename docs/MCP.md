# MCP notes

These notes are for people connecting an MCP client and agents using the tools.
See the [README](../README.md#agent-workflow) for connection setup and
prompts. The server advertises tool names, arguments and constraints through MCP;
[shared/tools.json](../shared/tools.json) is the repository reference.
The server's initialisation response reports the same build version as
`mapbox-tui --version`: the release tag for published binaries, or `dev` for
ordinary source builds.

## Files and measurements

Use absolute file paths. `load_track` returns the file's `track_id` and its
`item_id` values; `list_tracks` gives you the current workspace. IDs last for
that server process.

A file can contain recorded tracks, planned routes and standalone waypoints.
A whole-file summary includes all its geometry. Use `item_id` when asking about
one recorded activity, rather than treating a file's total distance as distance
travelled.

`inspect_track` uses either a zero-based `point_index` or `distance_m`. Distance
inspection finds the nearest recorded point. Indices and distances refer to
the combined file, or to the selected item when you supply `item_id`.

The bundled `example.gpx` is a useful check: its recorded track is about 2.50 km,
while the file total is about 2.97 km because it also contains Optional spur.
The separate `waypoints.gpx` contains a place, with no recorded distance.

For a longer, real-world example, load `fixtures/country-ride.gpx` using its
absolute path. It contains one recorded track with 29,005 points over about
181.09 km, with elevation but no timestamps, planned routes or waypoints.
Geometry within 2 km of either original endpoint and identifying metadata have
been removed; the remaining route still reveals the area travelled. Try
`inspect_track` with `distance_m: 90000`, or render the whole ride offline.

## Rendering a workspace

After loading files and adjusting visibility, call `render_map` with:

```json
{
  "workspace": true,
  "provider": "none",
  "ascii": true,
  "width": 60,
  "height": 20
}
```

This fits the visible items and highlights the selection. Omit `track_id`,
`item_id` and `point_index` in workspace mode. An explicit `track_id` renders
that file directly instead of applying workspace visibility.

Results contain plain map text and scene metadata. `active_item` and
`visible_items` use `document_id` and `item_id`; match the document ID to the
file returned by `load_track` or `list_tracks`.

Mapbox is used when credentials are available. `provider: "none"` keeps a
render offline. Provider failures appear in `scene.status`; importing and
inspecting files don't need Mapbox.

## Beyond MCP: terminal control

MCP and the terminal viewer keep separate workspaces. MCP manages imports,
selection, visibility, inspection and text maps. Place search, journey planning,
playback and live traffic are viewer features, so an agent needs terminal
control to use them.

For example, with Mapbox connected:

> Use mapbox-tui in the terminal to plan a trip from King's Cross to Greenwich.
> Compare the cycling and walking estimates, then show me the cycling route.

These viewer actions and screenshots require separate terminal control. The MCP
tools return text maps and scene metadata; they don't control or capture the viewer.
