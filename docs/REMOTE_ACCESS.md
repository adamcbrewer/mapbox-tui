# Remote access

Mapbox TUI sessions can be controlled remotely, either by you or an agent.
Remote control has its own [CLI/MCP command set](AGENT_USAGE.md#live-viewer-commands).
Many actions match the viewer, but keyboard shortcuts and startup commands from
[Usage](USAGE.md) aren't passed through. Prefix remote CLI actions with
`mapbox-tui remote --session NAME_OR_UUID`.

This reference covers session rules, command results and transport.
For setup and recipes, see [Agent usage](AGENT_USAGE.md).

Start the map at one terminal using a named session.

```sh
mapbox-tui --name main
```

Control it from another:

```sh
mapbox-tui session list --json
mapbox-tui remote --session main status --json
```

## Sessions

Unix viewers start with local control enabled; Windows viewers don't.
Use `--no-control` to disable discovery and control.

| Area | Behaviour |
|---|---|
| Identity | Fixed UUID; optional name unique among your running viewers |
| Names | ASCII letter first; 48 characters max; letters, digits, hyphens, underscores; cannot look like UUID |
| Target | CLI takes name or UUID; MCP takes UUID; pinned MCP keeps original UUID if name reused |
| Lifetime | Ends when viewer closes; missing target returns error, not a new viewer |
| Working copy | Open/clear changes generation, keeps session UUID; standalone CLI/MCP has separate workspace |
| Persistence | Names don't save work; use [manual saves](USAGE.md#saved-workspaces); automatic session/crash recovery deferred |
| Imports | Regular files, 128 MiB max; CLI resolves relative paths; MCP/protocol needs absolute paths; reopening reuses import |
| Item IDs | Read refs from status; document/item IDs separate from journey stop IDs |

## Response fields

CLI JSON and MCP share an ANSI-free response. Check error codes, not message text.
Arguments: [action schemas](../shared/control_tools.json).

| Field | Meaning |
|---|---|
| `session_id`, `request_id` | Viewer and request IDs; edits use request ID as `operation_id` |
| `generation` | Working-copy ID; changes on workspace replacement |
| `revision` | Rises with edits, including display/playback changes; may rise several times per command; playback frames don't count |
| `state`, `message` | Operation state and readable feedback |
| `changed` | Edits applied? Route failure can still leave stop edits in place |
| `outcome_known` | False means outcome uncertain; `changed: false` then proves nothing |
| `result` | Action result; status lists paginated, names/descriptions shortened |
| `error` | `code`, `message`, `recovery`; failed/cancelled/superseded set MCP `isError`; pending doesn't |
| Checkpoint revision | Separate from live revision; stale save returns `workspace_revision_conflict`; working copies kept; reload or Save As |
| Dirty state | `persistence_dirty_available: false`; no `unsaved_changes` field; open/clear requires explicit discard consent |

## Operations and retries

Edits require generation and expected live revision. CLI reads these unless you
supply them; MCP needs them explicitly. Acting on earlier status? Use its values.
Stale requests fail without edits.

| State | Meaning |
|---|---|
| `applied` | Completed |
| `pending` | Accepted, still running; wait or check operation |
| `failed` | Failed; check `changed` for edits kept |
| `cancelled` | Work stopped; earlier edits kept |
| `superseded` | Replaced by later edits, human navigation, removal or workspace switch |

| Situation | Handling |
|---|---|
| Timeout / disconnect | Work may have applied; check original operation before retrying |
| Retry | Reuse ID only for same action, arguments, generation and expected revision |
| Expired / unknown operation | Check current state first; expired ID can apply again |
| Concurrent save | Save locks storage and checks checkpoint revision; merely opening a save doesn't lock it |
| Concurrent open | Edits during open supersede it; failed import/open keeps current work |
| Viewer feedback | Up to five notices, fewer on short maps; spinner while running, two seconds after result; status checks/retained retries stay quiet |

Results kept 15 minutes from acceptance, until session ends. Limits below;
full allowance rejects new work.

| Allowance | Limit |
|---|---:|
| Ordinary retained operations | 256 |
| Pending imports, routing and workspace opens | 16 |
| Retained cancel/clear operations | 33 |

Camera moves and synchronous edits don't use pending-worker slots. Cancel/clear
has separate capacity; when full, wait for results to expire.

## Camera, journeys and saves

Extra details for the [command table](AGENT_USAGE.md#live-viewer-commands).

| Area | Behaviour |
|---|---|
| Camera status | `camera`: destination; `camera_current`: current position; motion fields: `camera_transition_id`, `camera_animating`, `camera_animation` |
| Camera completion | Moves return `transition_id`; tours wait for that operation; cancel stops at current position; global animation flag may refer to later move |
| Animation | Spring motion; duration isn't configurable; later move/human navigation supersedes previous transition |
| Journey route states | `offline`, `unresolved`, `pending`, `ready`, `failed`, `stale`; only ready road routes include estimates/revision/attribution |
| Stop IDs | Replace all stops: new IDs; remove endpoint: clear location, keep draft |
| Mapbox access | Viewer credentials/offline/billing settings apply; control can't accept billing or override offline mode |
| Missing saved imports | Repair in viewer; clear keeps checkpoints and source files |

## CLI exit codes

Exit 0 can mean pending. Check JSON state for completion.

| Exit | Meaning |
|---|---|
| 0 | Applied or accepted pending work |
| 1 | Parsing or local setup failure |
| 2 | Action failed |
| 3 | Conflict |
| 4 | Missing/disconnected session or unavailable routing |
| 5 | Timeout |

## Transport

Local Unix sockets. Any process running as your OS user can connect.

| Area | Detail |
|---|---|
| Runtime directory | `$XDG_RUNTIME_DIR/mapbox-tui-UID`; fallback `$TMPDIR/mapbox-tui-UID` (usually `/tmp`); too long: `/tmp/mapbox-tui-UID` |
| Permissions | Owned by current user; directory 0700, sockets 0600 |
| Discovery | Registry lock protects names/publication; exit removes endpoint; missing/refused endpoints cleaned after crash; timeout alone doesn't mark viewer dead |
| Wire format | Protocol v1; one newline-delimited JSON request/response per connection; deadlines apply |
| Limits | 512 KiB request, 4 MiB response, 32 concurrent connections; [geometry limits](USAGE.md#limits) apply |
| Oversized result | `response_too_large`; check `changed` and original operation before retrying |

Parsing/provider work runs outside viewer update loop. Responses are serialised
snapshots.
