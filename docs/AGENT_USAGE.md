# Agent usage

Inspect files in a standalone MCP workspace, or control a running viewer through
CLI/MCP. Separate workspaces: `load_track` doesn't load the map on screen.
For keyboard controls and ordinary CLI use, see [Usage](USAGE.md).
For session rules, retries, exit codes and transport, see [Remote access](REMOTE_ACCESS.md).

## Setup

Build with Go meeting [go.mod](../go.mod), then connect your client to the local
binary over stdio. [Development](DEVELOPMENT.md#go-toolchain) covers the toolchain.

| Client / target | Setup | Check / caveat |
|---|---|---|
| This checkout | `make build`, then restart agent | Rebuild each worktree; reconnect after server changes |
| Codex | Uses [project config](../.codex/config.toml); trust checkout | `codex mcp get mapbox-tui` |
| OpenCode v1 | Uses [project config](../opencode.json) | `opencode mcp list` |
| Other MCP clients | Absolute binary path, argument `mcp` | Client launches server; starting it in another terminal doesn't attach it |
| Live viewer | Start `mapbox-tui --name main` separately | Local live control requires Unix; `--no-control` disables it |
| One fixed viewer | Server args `mcp --session main` | Viewer tools only; pins UUID; reconnect to target a replacement viewer |

Example client config. Replace the path; client-specific format may differ.

```json
{
  "mcpServers": {
    "mapbox-tui": {
      "command": "/absolute/path/to/mapbox-tui/bin/mapbox-tui",
      "args": ["mcp"]
    }
  }
}
```

## Standalone MCP

These nine tools share state for the server process. Use absolute paths and IDs
returned by `load_track`/`list_tracks`. Exact arguments: [tool schemas](../shared/tools.json).

| Use case | Tool | Arguments / result |
|---|---|---|
| Import file | `load_track` | `path`; returns `track_id`, item IDs; loaded path reuses import |
| List workspace | `list_tracks` | No arguments; files, items, visibility and active selection |
| Unload file | `unload_track` | `track_id`; removes all its items |
| Summarise | `summarize_track` | `track_id`, optional `item_id`; file total includes planned routes |
| Inspect point | `inspect_track` | `track_id`, optional `item_id`, exactly one of `point_index` or `distance_m` |
| Select item | `select_item` | `track_id`, `item_id`; reveals and highlights it |
| Show / hide | `set_item_visibility` | `track_id`, `visible`, optional `item_id`; omit item for whole file |
| Remove item | `remove_item` | `track_id`, `item_id`; last item removal unloads file |
| Render map | `render_map` | `workspace: true` fits visible items; or `track_id` with optional `item_id`/`point_index`; no track gives basemap alone |

Offline workspace render, after loading files:

```json
{"workspace":true,"provider":"none","ascii":true,"width":60,"height":20}
```

| Caveat | Rule |
|---|---|
| Render selection | Workspace mode omits `track_id`, `item_id`, `point_index`; direct-file rendering ignores workspace visibility |
| Output | ANSI-free `text` and `scene`; match scene `document_id` to loaded file IDs |
| Point selection | Indices zero-based, distances metres; supplying `item_id` makes both item-local; nearest recorded point returned |
| Online rendering | Needs credentials, billing acknowledgement and offline mode disabled; MCP never accepts billing for the caller |
| Unattended billing | After reviewing [billing guidance](MAPBOX.md#free-usage-and-billing), use `auth online --accept-billing` or `MAPBOX_TUI_ACCEPT_BILLING=1`; environment acceptance doesn't override offline mode |
| Offline rendering | `provider: "none"`; imports, summaries and inspection need no Mapbox connection |
| Shared limits | [Import, geometry and output limits](USAGE.md#limits); viewer preferences aren't applied |

## Live viewer commands

Discover with `viewer_sessions`, then call `viewer_status` with
`{"session_id":"UUID_FROM_DISCOVERY","args":{}}`. An empty session list means
start a viewer first. CLI accepts names/UUIDs; MCP uses UUIDs.

Prefix CLI actions below with `mapbox-tui remote --session main`.
`D:I` means returned document/item IDs; `STOP` means stable stop ID;
`OP` means operation/request ID. Exact MCP `args`: [action schemas](../shared/control_tools.json).

| Use case | CLI action | MCP tool |
|---|---|---|
| Discover viewers | `mapbox-tui session list --json` (no prefix) | `viewer_sessions` |
| Inspect live state | `status --json` | `viewer_status` |
| Action schemas | `capabilities --json` | `viewer_capabilities` |
| Import | `load /absolute/ride.gpx` | `viewer_load` |
| Select item | `select --ref D:I` | `viewer_select` |
| Show / hide | `visibility --ref D:I --visible=false` | `viewer_visibility` |
| Remove item / file | `remove --ref D:I` | `viewer_remove` |
| Save working copy | `workspace save --name "Tour"` | `viewer_workspace_save` |
| Save As | `workspace save --name "Variant" --save-as` | `viewer_workspace_save` |
| Open checkpoint | `workspace open /absolute/save.json --discard` | `viewer_workspace_open` |
| Clear working copy | `workspace clear --discard` | `viewer_workspace_clear` |
| Move camera | `camera set --lat 51.53 --lon -0.12 --zoom 13 --bearing 45` | `viewer_camera_set` |
| Pan in terminal cells | `camera pan --dx 5 --dy 0` | `viewer_camera_pan` |
| Fit visible content | `camera fit` | `viewer_camera_fit` |
| Session animation | `camera animation --enabled=false` | `viewer_camera_animation` |
| Play / pause recording | `playback play --ref D:I` / `playback pause --ref D:I` | `viewer_playback_play` / `viewer_playback_pause` |
| Seek and pause | `playback seek --ref D:I --point-index 10` | `viewer_playback_seek` |
| Playback multiplier | `playback speed --ref D:I --speed 20` | `viewer_playback_speed` |
| Available themes | `theme list --json` | `viewer_theme_list` |
| Session theme | `theme set --theme nord` | `viewer_theme_set` |
| Create journey | `journey create --start 51.5,-0.1 --end 51.6,-0.2 --mode offline` | `viewer_journey_create` |
| Inspect stops / route state | `journey inspect --journey D:I` | `viewer_journey_inspect` |
| Reverse journey | `journey reverse --journey D:I` | `viewer_journey_reverse` |
| Travel mode | `journey mode --journey D:I --mode walking` | `viewer_journey_mode` |
| Replace all stops | `journey stops --args '{"ref":{"document_id":2,"item_id":1},"stops":[{"lat":51.5,"lon":-0.1},{"lat":51.6,"lon":-0.2}]}'` | `viewer_journey_stops` |
| Add waypoint | `journey stop add --journey D:I --after STOP --lat 51.51 --lon -0.1` | `viewer_journey_stop_add` |
| Set stop location | `journey stop set --journey D:I --stop-id STOP --lat 51.52 --lon -0.1` | `viewer_journey_stop_set` |
| Remove waypoint / clear endpoint | `journey stop remove --journey D:I --stop-id STOP` | `viewer_journey_stop_remove` |
| Reorder waypoint | `journey stop move --journey D:I --stop-id STOP --direction -1` | `viewer_journey_stop_move` |
| Retry routing | `journey calculate --journey D:I --wait` | `viewer_journey_calculate` |
| Select, reveal and fit journey | `journey show --journey D:I --wait-visual` | `viewer_journey_show` |
| Inspect operation | `operation status OP --json` | `viewer_operation_status` |
| Cancel operation | `operation cancel OP --json` | `viewer_operation_cancel` |

## Live edits and caveats

Read fresh status before each edit. MCP mutations require the observed generation,
revision and a unique request ID outside `args`. Example `viewer_camera_fit` call:

```json
{
  "session_id": "UUID_FROM_DISCOVERY",
  "request_id": "fit-1",
  "generation": "GENERATION_FROM_STATUS",
  "expected_revision": 7,
  "args": {},
  "wait_visual": true
}
```

| Area | Rule |
|---|---|
| CLI preconditions | Reads current status unless supplied `--generation` and `--expected-revision`; use observed values when acting on earlier state |
| CLI raw arguments | `--args JSON` replaces action flags; cannot mix them |
| Waiting | CLI `--wait`; MCP `wait: true`; camera moves also support `--wait-visual` / `wait_visual: true`; `--timeout` / `timeout_seconds` defaults 30s, maximum 5 minutes |
| Load/open completion | CLI load and workspace open wait by default; MCP must wait or query operation before using restored content |
| Camera | Latitude/longitude paired; positive pan dx/dy drags right/down; `--animate=true/false` overrides one move |
| Status lists | Up to 20 documents/items per page; follow `next_offset` / `next_item_offset`; CLI `--offset`, `--item-offset`, `--limit`; `--summary` omits lists |
| Item references | Item ID 0 targets whole document for visibility/removal only; use returned refs, never assumed IDs |
| Journey edits | Creation/editing doesn't select or fit; use journey show. Routed edits may apply even if routing fails; inspect `changed` and `error` |
| Routing | Coordinates only; viewer credentials/offline/billing settings apply; use Offline, Walking, Cycling or Driving; 25-stop provider limit |
| Speed / appearance | Speeds 1/5/20/60/120; speed selects recording and preserves play/pause. Remote theme/animation changes don't save global preferences |
| Workspace replacement | Open/clear requires explicit discard consent; saves use original file references; stale saves preserve working copy |
| Results / retries | Pending means accepted, not complete. Timeout/disconnect doesn't undo edits; query operation before retry. Reuse request ID only with identical arguments and preconditions |
| Sessions | End with viewer; pinned MCP keeps original UUID even if another viewer reuses its name |
| Missing tools | Named-place search, screenshots, favourites management, journey undo and saved-library rename/duplicate/delete need terminal interaction |
| Detailed reference | [Remote access](REMOTE_ACCESS.md): operation states, retention, exit codes, concurrency and transport |

## Agent recipes

Supply absolute import paths. For every live recipe, discover `main`, read fresh
status before each edit, use its UUID/generation/revision and wait for completion.

| Goal | Example prompt |
|---|---|
| Inspect offline | Load `/absolute/ride.gpx` in standalone MCP. List items, summarise only its recording, inspect nearest point to 1 km, then render a 60×20 offline ASCII map. |
| Compare files | Load `/absolute/ride.gpx` and `/absolute/course.tcx` in viewer `main`. Show both recordings, fit visible content and wait for the camera to settle. |
| Plan offline | In `main`, create an Offline journey from 51.5308,-0.1238 via 51.51,-0.10 to 51.4826,-0.0077. Show it and wait for the camera. |
| Play recording | In `main`, find the recording in `ride.gpx`, seek to point 10, set 20× speed and play. Keep other routes visible. |
| Camera tour | Set `main` to Nord. Animate to 51.5308,-0.1238 at zoom 14, bearing 45. Wait, then fit visible content and wait again. |
| Save work | Save `main` as London tour and return the exact checkpoint path. Ask for discard consent before later replacing another working copy with it. |
| Resolve timeout | Check the original operation ID and current status. Report whether the edit applied before attempting another mutation. |
