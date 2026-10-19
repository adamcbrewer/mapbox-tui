# Remote access

Start the map in one terminal:

```sh
mapbox-tui --name main
```

Control it from another:

```sh
mapbox-tui session list --json
mapbox-tui remote --session main status --json
mapbox-tui remote --session main camera set --lat 51.53 --lon -0.12 --zoom 13
```

CLI and MCP use the same viewer commands. Local control uses Unix sockets and
starts with every Unix viewer. Launch with `--no-control` to disable it.
Windows viewers start without remote access; named control sessions require Unix.

## Sessions

Every session has a UUID and an optional name. Names are unique among the current
user's live sessions. They start with an ASCII letter and contain up to 48 letters,
digits, hyphens or underscores. UUID-shaped names are reserved.

Use `session inspect main --json` to inspect a session. CLI `--session` accepts
its name or full UUID. MCP uses UUIDs. The viewer shows its name or shortened UUID
in the status line.

Closing the viewer ends its session. Clients report a missing target rather than
start another viewer. A pinned MCP connection keeps the original UUID even if a
new viewer reuses the name.

Each session owns a working copy. Opening or clearing a workspace changes that
copy's generation but keeps the session UUID. Session names don't save files;
there is no session history to resume. Saved workspaces use #79's shared store.
Recovery drafts belong to #80. Ordinary CLI rendering and standalone MCP keep
separate workspaces.

## Imports and camera

Options can go before or after the action. Use `--json` for structured output
and `capabilities --json` for the action schemas.

```sh
mapbox-tui remote --session main load fixtures/example.gpx --json
mapbox-tui remote --session main camera fit
mapbox-tui remote --session main camera set --bearing 45
mapbox-tui remote --session main camera pan --dx 5 --dy 0
```

`camera fit` fits visible geometry. Pan values drag the map in terminal cells:
positive dx moves right; positive dy moves down. Latitude must stay within the
Mercator range, about -85.0511 to 85.0511. Absolute zoom accepts -8 to 20.

Imports return a `ref` with `document_id` and `item_id`. CLI commands use
`DOCUMENT_ID:ITEM_ID`. Read IDs from `status`; the IDs below are examples.
Relative paths resolve from the sending CLI's directory. Imports accept regular
files up to 128 MiB and wait for completion by default, including `--path` and
JSON arguments.

```sh
mapbox-tui remote --session main select --ref 1:1
mapbox-tui remote --session main visibility --ref 1:1 --visible=false
mapbox-tui remote --session main visibility --ref 1:0 --visible=true
mapbox-tui remote --session main playback play --ref 1:1
mapbox-tui remote --session main playback pause --ref 1:1
mapbox-tui remote --session main playback seek --ref 1:1 --point-index 10
mapbox-tui remote --session main remove --ref 1:1
```

Item ID 0 targets a whole document for visibility and removal only. Reopening a
loaded path reuses, reveals and selects its document without rereading it.
Failed imports preserve the workspace. Playback selects the requested recording;
planned journeys have no recorded playback.

## Status and saved workspaces

Status returns up to 20 documents and 20 items per document. Pass `next_offset`
as `--offset`, and `next_item_offset` as `--item-offset`. `--limit` accepts 1 to 20.
Names and descriptions are truncated to bounded lengths. Use `journey inspect`
for stops. Use `status --summary` for session, camera, playback and checkpoint
metadata without document lists.

```sh
mapbox-tui remote --session main workspace save --name "London tour" --json
mapbox-tui remote --session main workspace save --json
mapbox-tui remote --session main workspace save --name "London variant" --save-as --json
mapbox-tui remote --session main workspace open /path/from/save.json --discard --json
mapbox-tui remote --session main workspace clear --discard
```

Save returns the checkpoint path. CLI resolves relative open paths before sending;
MCP and protocol clients must send absolute paths. Open waits for completion and requires
`--discard` to replace the working copy. Missing imports remain available for
repair in the viewer. Clear invalidates pending work and leaves saved checkpoints
and source files intact.

Sessions can open the same checkpoint. Saving checks its revision and replaces
it under one storage lock. A stale save returns `workspace_revision_conflict`,
with CLI exit 3, and preserves both working copies. Reload or use Save As.
Checkpoints stay unlocked while open. Edits made during an open operation
supersede it.

MCP exposes `viewer_workspace_save` and `viewer_workspace_open` through the same
shared persistence APIs.

## Journeys

Create a journey with coordinates or a `stops` array through `--args` or MCP.
Use the returned document/item reference in later commands. Stop IDs are stable
within a journey and separate from provider geometry indices.

```sh
mapbox-tui remote --session main journey create \
  --start 51.5308,-0.1238 --end 51.4826,-0.0077 --mode cycling --wait --json
mapbox-tui remote --session main journey inspect --journey 2:1 --json
mapbox-tui remote --session main journey mode --journey 2:1 --mode walking --wait
mapbox-tui remote --session main journey stop add \
  --journey 2:1 --after 1 --lat 51.51 --lon -0.10 --wait
mapbox-tui remote --session main journey stop set \
  --journey 2:1 --stop-id 3 --lat 51.52 --lon -0.10 --wait
mapbox-tui remote --session main journey reverse --journey 2:1 --wait
mapbox-tui remote --session main journey stop remove --journey 2:1 --stop-id 3
mapbox-tui remote --session main journey show --journey 2:1
```

`journey stop move --stop-id ID --direction -1` or `1` moves a waypoint without
crossing endpoints. Removing an endpoint clears its location but keeps the draft.
`journey stops` replaces all stops and assigns fresh IDs:

```sh
mapbox-tui remote --session main journey stops --args \
  '{"ref":{"document_id":2,"item_id":1},"stops":[{"lat":51.5,"lon":-0.1},{"lat":51.6,"lon":-0.2}]}'
```

Modes are `offline`, `walking`, `cycling` and `driving`. Routed edits recalculate
asynchronously once the stops are complete. Use `journey calculate` to retry.
Route states are `offline`, `unresolved`, `pending`, `ready`, `failed` and `stale`.
Only ready road routes include distance/duration estimates, revision and
attribution. Offline mode draws straight lines. Routing failure can leave stop
edits applied; check `changed` and `error`.

Creating, editing and inspecting journeys leave selection and camera unchanged.
`journey show` selects, reveals and fits the journey. Requests use the viewer's
credentials, offline setting and billing acknowledgement. Control cannot change
billing consent or override offline mode. Coordinates and imports work offline;
place-name lookup isn't available through these commands.

## Theme, speed and animation

These changes affect the session without saving global preferences:

```sh
mapbox-tui remote --session main theme list --json
mapbox-tui remote --session main theme set --theme nord
mapbox-tui remote --session main playback speed --ref 1:1 --speed 20
mapbox-tui remote --session main camera animation --enabled=true
mapbox-tui remote --session main camera set --lat 51.5308 --lon -0.1238 --zoom 14 --animate --wait-visual
mapbox-tui remote --session main camera fit --animate=false
```

Use a theme ID from `theme list`, including custom palettes loaded at startup.
Playback speeds are 1, 5, 20, 60 and 120. Setting speed selects the recording and
preserves its playing/paused state.

Camera set, pan, fit and journey show follow the viewer's animation setting.
`--animate=true/false` overrides it for one command. Motion uses the viewer's
spring animation; duration isn't configurable.

Camera moves return a `transition_id`. Animated moves stay `pending` until they
settle, then become `applied`. A replacement move or human navigation makes them
`superseded`. Cancellation stops at the current position. Use `--wait-visual`,
`--wait` or `operation status` to await that transition. For tours, wait for each
operation rather than the global animation flag, which may describe a later move.

Status includes `camera` for the destination, `camera_current`,
`camera_transition_id`, `camera_animating`, `camera_animation`, `theme` and
playback `speed`/`available_speeds`.

## Operations and retries

The viewer stacks small, high-contrast notifications at the top-right of the map,
with the newest command first and one terminal row of space between messages.
Each command has its own spinner while running
and stays visible for two seconds after completion or failure. Commands run
immediately. Up to five notifications appear, with fewer on short maps and an
overflow count for larger bursts.
Notifications use readable names, such as "Moving camera" or "Track loaded".
Background status queries and retries of retained operations don't duplicate,
reorder or extend them. This is command feedback, not a connection count or a
network latency reading.

Responses include session/request IDs, working-copy `generation`, live `revision`,
`state`, `changed`, `outcome_known` and a message. They may include `result` or an
`error` with `code`, `message` and `recovery`. Mutations return an `operation_id`
equal to the request ID. JSON is ANSI-free.

Mutations require the generation and expected live revision. CLI reads these
before sending unless you supply `--generation` and `--expected-revision`.
MCP requires both explicitly. When acting on an earlier observation, send its
values. Stale requests fail without applying edits.

Live revision counts edits, including presentation and playback changes. It
increases monotonically and can advance more than once per command. It is
separate from checkpoint revision and playback frames. The viewer compares
savable state for its Open/Start clean confirmation. Remote status still reports
`persistence_dirty_available: false` and omits `unsaved_changes`; remote replacement
continues to require explicit discard consent, independently of live revision.

```sh
mapbox-tui remote --session main camera set --zoom 12 \
  --generation GENERATION --expected-revision 7 --request-id move-camera-1 --json
mapbox-tui remote --session main operation status move-camera-1 --json
mapbox-tui remote --session main operation cancel OPERATION_ID --json
```

States are `applied`, `pending`, `failed`, `cancelled` and `superseded`. Pending
means accepted, not finished. `--wait` waits for completion; `--timeout` defaults
to `30s` and accepts up to five minutes.

A timeout or disconnect doesn't roll back work. `outcome_known: false` means the
outcome is uncertain, even when `changed` is false. Query the operation before
retrying. Cancellation keeps earlier edits. Later edits, removal or workspace
replacement can supersede pending work.

Reuse a request ID only with identical action, arguments and preconditions.
Results stay available for 15 minutes from acceptance, within these limits:

| Allowance | Limit |
| --- | ---: |
| Ordinary retained operations | 256 |
| Pending imports, routing and workspace opens | 16 |
| Retained cancel/clear operations | 33 |

Camera transitions and synchronous edits don't use pending-worker capacity.
A full allowance rejects new work. Cancel/clear has separate capacity, but once
that fills it also needs results to expire. Completed results can be removed
after 15 minutes; all results disappear when the session ends. Inspect current
state before retrying an expired or unknown request. An expired ID can apply again.

| CLI exit | Meaning |
| --- | --- |
| 0 | Applied or accepted pending work |
| 1 | Parsing or local setup failure |
| 2 | Action failed |
| 3 | Conflict |
| 4 | Missing/disconnected session or unavailable routing |
| 5 | Timeout |

Read JSON error codes rather than parsing messages.

## MCP setup

Configure your MCP client once to launch `mapbox-tui mcp`. The client starts and
connects to that process; running it in another terminal doesn't attach it to
an existing agent. Start the viewer separately with `mapbox-tui --name main`.

Call `viewer_sessions` with `{}`, find `main`, then call `viewer_status`:

```json
{"session_id":"UUID_FROM_DISCOVERY","args":{}}
```

Pass that UUID to each viewer tool. The server can discover viewers started
later and control several sessions. An empty discovery list means no viewers
are running. Launch one and try again.

Try this prompt:

> Find main with viewer_sessions. Set its theme to nord, then animate the camera
> to 51.5308, -0.1238 at zoom 14 and wait for it to settle. Read status before each
> edit and use its UUID, generation and revision with a unique request ID.

Tool names replace action dots with underscores, such as
`viewer_journey_stop_add`. [Action schemas](../shared/control_tools.json) define
`args`. The MCP wrapper accepts `wait`, `timeout_seconds` and mutation fields
`request_id`, `generation`, `expected_revision`. Camera tools also accept
`wait_visual: true`. Theme, speed and animation tools are `viewer_theme_list`,
`viewer_theme_set`, `viewer_playback_speed` and `viewer_camera_animation`.

Structured content and text carry the same response. Failed, cancelled and
superseded outcomes set `isError`; pending work does not. Use
`viewer_operation_status` to check completion.

For one fixed target, configure the client to launch `mapbox-tui mcp --session main`.
This pins its UUID. You can omit `session_id`; a different UUID fails. Reconnect
to change targets. Standalone tools such as `load_track` and `render_map` keep
their own workspace. Use `viewer_*` tools to control the map on screen.

## Transport

The current user owns the runtime directory, with mode 0700, and sockets use
0600. The directory is `$XDG_RUNTIME_DIR/mapbox-tui-UID`, falling back to
`$TMPDIR/mapbox-tui-UID`, normally under `/tmp`. If that path is too long for
a Unix socket, endpoints use `/tmp/mapbox-tui-UID`. A short registry lock protects
publication, names and stale-record cleanup. Normal exit removes the endpoint;
discovery reclaims missing or refused endpoints after crashes. Timeouts alone
don't mark a viewer dead. Any process running as the same OS user can connect.

Protocol version 1 sends one newline-delimited JSON request/response per
connection. Requests are limited to 512 KiB, responses to 4 MiB and concurrent
connections to 32. The request limit accommodates 5,000-stop journey creation
and replacement; provider route calculations retain their separate 25-stop limit.
Connections have deadlines. Oversized results return
`response_too_large`; inspect `changed` and the operation before retrying.

Parsing and provider work run outside the update loop. Typed requests/results
enter the loop; responses contain serialised snapshots, never model pointers.
