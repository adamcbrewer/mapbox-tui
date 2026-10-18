# Control a running viewer

Each running viewer exposes a local control session by default. CLI commands and
attached MCP tools operate on that viewer's workspace, through the same handlers.
No keyboard automation is needed. Control currently supports Unix local sockets.
Use `--no-control` to disable discovery and control for a viewer.

## Session identity

```sh
mapbox-tui --name main
mapbox-tui session list --json
mapbox-tui session inspect main --json
mapbox-tui remote --session main status --json
```

Every session gets a UUID, including named sessions. A name is an optional alias,
unique among live sessions for the current user. Names start with an ASCII letter
and contain up to 48 letters, digits, hyphens or underscores. UUID-shaped names
are reserved. Use the full UUID when an agent needs an unambiguous target.

A bare viewer launch also creates a discoverable session. Its name or shortened
UUID appears in the status line. Closing the viewer ends the session. Clients
never start a replacement viewer when their target disappears. Attached MCP pins
the resolved UUID for its lifetime, even if another viewer later reuses the name.

A session is not a saved workspace. It owns one live working copy. Clearing that
copy changes its generation, while the session UUID stays the same. Session
names don't create saved files, and there is no saved-session history.

Workspace saving/reopening belongs to #79; startup recovery and draft ownership
belong to #80. Those features aren't present on this branch's base. This release
reports `persistence_available: false` and does not advertise save/open tools or
implement a second persistence format. Ordinary CLI rendering and standalone MCP
continue to use independent workspaces.

## CLI actions

Options can precede or follow the action. `--session` accepts a UUID or live name.
Use `--json` for structured responses; `capabilities` returns the action schemas.

```sh
mapbox-tui remote --session main capabilities --json
mapbox-tui remote --session main load fixtures/example.gpx --json
mapbox-tui remote --session main camera fit
mapbox-tui remote --session main camera set --lat 51.53 --lon -0.12 --zoom 13
mapbox-tui remote --session main camera set --bearing 45
mapbox-tui remote --session main camera pan --dx 5 --dy 0
```

Camera commands set a destination and follow the viewer animation setting
unless overridden with `--animate=true/false`. Pan values describe dragging the map in
terminal cells: positive dx drags right; positive dy drags down. Centre latitude
must fit the Mercator range, approximately -85.0511 to 85.0511. Zoom is absolute,
from -8 to 20. `camera fit` fits all visible geometry.

Imports return `ref` with `document_id` and `item_id`. The CLI spelling is
`DOCUMENT_ID:ITEM_ID`; discover current IDs with `status`. IDs belong to the live
working copy. Relative import paths are resolved in the sending CLI's directory.
CLI loads wait for completion by default, including `--path` and JSON arguments.
Remote imports accept regular files up to 128 MiB.

```sh
mapbox-tui remote --session main select --ref 1:1
mapbox-tui remote --session main visibility --ref 1:1 --visible=false
mapbox-tui remote --session main visibility --ref 1:0 --visible=true
mapbox-tui remote --session main playback play --ref 1:1
mapbox-tui remote --session main playback pause --ref 1:1
mapbox-tui remote --session main playback seek --ref 1:1 --point-index 10
mapbox-tui remote --session main remove --ref 1:1
```

For visibility and removal only, item ID 0 targets a whole document. Reopening a
loaded path reuses the existing document, restores visibility and selects it.
Failed imports preserve the current workspace. Playback targets a specific
recording, selecting it first. Planned journeys have no recorded playback.

## Journey control

Create with coordinates, or use the `stops` array through `--args` or MCP. Returned
journey references use the same document/item pair as imports. Stops have stable
IDs within a journey; these are different from indices in provider geometry.

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

Use returned IDs instead of assuming the example's IDs. `journey stop move` takes
`--stop-id` and `--direction -1` or `1`, moving an intermediate waypoint without
crossing endpoints. Removing an endpoint clears its location, retaining the
incomplete draft. `journey stops` replaces the ordered stops with fresh stop IDs:

```sh
mapbox-tui remote --session main journey stops --args \
  '{"ref":{"document_id":2,"item_id":1},"stops":[{"lat":51.5,"lon":-0.1},{"lat":51.6,"lon":-0.2}]}'
```

Supported modes are `offline`, `walking`, `cycling` and `driving`. Complete edits
in routed modes recalculate asynchronously. `journey calculate` explicitly retries
routing. Route status distinguishes `offline`, `unresolved`, `pending`, `ready`,
`failed` and `stale`. Distance/duration estimates appear only for a ready road
route, with its revision and attribution. Offline geometry is a straight-line
preview, not a road route. A failure can retain edited stops while reporting that
routing failed; check `changed` and `error` rather than assuming rollback.

Creating, editing or inspecting a journey doesn't move the camera or select it.
`journey show` explicitly selects, reveals and fits its current geometry. Provider
requests use the viewer's credentials and online/billing settings. Control never
acknowledges billing or overrides offline mode. Coordinates and local imports
remain usable offline. Place-name lookup is not included in this interface yet.

## Preconditions, operations and retries

Each response includes `session_id`, `request_id`, `generation`, live `revision`,
`state`, `changed`, `outcome_known`, a message and, where applicable, `result` or a structured
`error`. Mutation responses also include `operation_id`, equal to their request
ID. Errors contain `code`, `message` and `recovery`. JSON never contains terminal
styling. States are `applied`, `pending`, `failed`, `cancelled` or `superseded`.

Mutations require the generation and expected live revision. CLI commands read
status immediately before sending unless you provide `--generation` and
`--expected-revision`. This catches changes between that read and application;
for an agent acting on an earlier observation, supply that observation's values.
MCP requires explicit preconditions. Stale requests fail without applying changes.
The live revision is monotonic and may advance more than once per command. It is
not a saved checkpoint revision or a playback-frame counter.

```sh
mapbox-tui remote --session main camera set --zoom 12 \
  --generation GENERATION --expected-revision 7 --request-id move-camera-1 --json
mapbox-tui remote --session main operation status move-camera-1 --json
mapbox-tui remote --session main operation cancel OPERATION_ID --json
```

`--wait` waits for imports/routing; `--timeout 30s` is the default, with a maximum
of five minutes. An `applied` import is in the workspace. A `pending` response
means work was accepted, not completed. Timeout/disconnect doesn't imply rollback.
`outcome_known: false` marks an uncertain outcome; `changed` then reports only
confirmed effects, and false mustn't be read as proof that nothing happened.
Query `operation status` before retrying. Cancellation leaves previously applied
edits intact. Edits/removal or workspace clearing can supersede pending work.

Reuse a request ID only with the same action, arguments and preconditions. The
viewer retains mutation results for 15 minutes from acceptance, with a maximum
of 256 retained operations and 16 pending operations. It rejects new work when
full rather than evicting unexpired outcomes. Completed results become eligible
for removal after the retention period. Deduplication is session-local and ends
when the viewer exits. After expiry or an unknown outcome, inspect the workspace
before issuing a new mutation; replaying an expired key can apply it again.

Ordinary success and accepted pending work exit 0. Reported action failures exit
2, conflicts exit 3, missing/disconnected sessions or unavailable routing exit 4,
and a reported timeout exits 5. CLI parsing/local setup failures exit 1. A transport
failure can leave the outcome unknown; the message identifies the request to
inspect. Use `--json` to read the error code rather than parsing prose.

Clearing the workspace is explicit:

```sh
mapbox-tui remote --session main workspace clear --discard
```

This cancels/invalidates old work without deleting saved checkpoints or source
files. It is not saved-workspace deletion.

## Theme, playback speed and camera animation

These settings affect the live session and do not overwrite saved preferences:

```sh
mapbox-tui remote --session main theme list --json
mapbox-tui remote --session main theme set --theme nord
mapbox-tui remote --session main playback speed --ref 1:1 --speed 20
mapbox-tui remote --session main camera animation --enabled=true
mapbox-tui remote --session main camera set --lat 51.5308 --lon -0.1238 --zoom 14 --animate
mapbox-tui remote --session main camera set --zoom 10 --animate
mapbox-tui remote --session main camera fit --animate=false
```

Playback speed accepts the viewer's existing multipliers: 1, 5, 20, 60 and 120.
It selects the specified recording and preserves its playing/paused state.
Use the recording reference from status, not the example reference above.
Theme selection accepts IDs returned by `theme list`, including a custom palette
loaded when that viewer started.

Camera set, pan, fit and journey show follow the viewer's animation setting by
default. An explicit `animate` boolean overrides it for that command only.
Transitions use the same spring motion as keyboard navigation; duration is not
configurable. The applied response means the destination was accepted.
`--wait` does not wait for visual settling. Status reports `camera` (destination),
`camera_current`, `camera_animating`, `camera_animation`, `theme` and playback
`speed`/`available_speeds`. Poll status until `camera_animating` is false if visual
completion matters. Later camera commands or human navigation can redirect motion.

MCP exposes `viewer_theme_list`, `viewer_theme_set`, `viewer_playback_speed`
and `viewer_camera_animation`, alongside the camera tools. For example, ask:

> Find the session named main. Set its theme to nord, set the active recording's
> playback speed to 20x, then smoothly move to King's Cross at 51.5308, -0.1238,
> zoom 14. Read current generation/revision before each mutation.

## MCP session discovery and control

Configure your MCP client once to launch `mapbox-tui mcp`. No session-specific
configuration is needed. Start a viewer in another terminal:

```sh
mapbox-tui --name main
```

Ask the agent to call `viewer_sessions` with `{}`, choose the session named
`main`, then call `viewer_status` with:

```json
{"session_id":"UUID_FROM_DISCOVERY","args":{}}
```

Every viewer action accepts that same `session_id` UUID. Names are displayed for
humans; UUID targeting prevents a later viewer reusing a name from receiving an
old command. The MCP server can start before any viewers, discover newly opened
ones, and control several sessions independently. Discovery returns an empty
list when none are running; launch a viewer and list again.

For example, ask: “Use viewer_sessions to find main, inspect its status, then
move its camera to latitude 51.5308, longitude -0.1238, zoom 14. Use its UUID,
generation and revision, and a unique request ID.”

Typed `viewer_*` tools cover camera, import, selection, visibility, playback,
journey and operation controls. The canonical action argument schemas are in
`shared/control_tools.json`; tool names replace dots with underscores, for
example `viewer_journey_stop_add`. The wrapper accepts `args`, optional `wait`
and `timeout_seconds`, and mutation preconditions `request_id`, `generation`,
`expected_revision`. Read status before mutating.

Both MCP structured content and text carry the response; `isError` is set for
failed, cancelled or superseded outcomes. A pending response is not an MCP
error. `viewer_operation_status` inspects its final result. An ended session
returns an error with recovery instructions; it never selects a replacement.

Optionally launch `mapbox-tui mcp --session main` to pin a connection to one
UUID. In pinned mode `session_id` can be omitted; supplying a different UUID
fails. Reconnect to change that pinned target.

Default MCP also retains its existing standalone tools and private workspace.
Standalone `render_map` and `load_track` do not modify live viewers; use the
explicit `viewer_*` tools for live control.

## Transport and workspace integration

The runtime directory is `$XDG_RUNTIME_DIR/mapbox-tui-UID`, falling back to
`$TMPDIR/mapbox-tui-UID` (normally `/tmp`). It must be owned by the current user,
with permissions 0700. Sockets are 0600. A short registry lock protects publication,
name uniqueness and stale-record cleanup. Timeouts aren't evidence of a dead
viewer. Normal exit removes the socket/record; discovery reclaims refused or
missing endpoints after a crash. This is local same-user control, not a remote
network server or a boundary between agents running as the same OS user.

The wire protocol is one newline-delimited JSON request/response per connection,
version 1. Requests are bounded to 64 KiB; clients bound responses to 4 MiB.
Socket handlers are bounded to 32 concurrent connections and use deadlines.
Parsing and provider work run outside the update loop; only typed results and
requests enter it. Responses are serialised snapshots, never shared model pointers.

When integrating #79, keep the saved checkpoint ID/base revision alongside the
live working copy. Checkpoint saving must check and replace under one storage
lock. Another session's successful save causes a stale-save conflict, preserving
the losing session's work for reload or Save As. A saved checkpoint needn't stay
locked while open. Recovery-draft ownership remains exclusive under #80.
Replacing a working copy must create a fresh live generation and invalidate old
imports/routes/saves. Advertise workspace persistence tools only once those
shared operations are available; don't create a second store in the transport.
