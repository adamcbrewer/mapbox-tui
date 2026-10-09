# Map providers

The viewer supports two basemap providers:

- `mapbox`: Mapbox Streets v8 vector tiles, using your own access token.
- `none`: imported file geometry on an empty canvas; no authentication or network
  requests are needed.

The viewer, CLI `render` and MCP `render_map` use Mapbox when a token is configured,
otherwise `none`. The file/track is optional. No file and no token produces an
empty canvas with setup guidance. Explicit Mapbox requests without credentials
warn without hiding the imported geometry. Viewer launches, including imported
files, show **No basemap · Mapbox not connected** when credentials are unavailable
and echo it once to stdout. The detailed install/login guidance remains available
through account setup/status; human CLI renders print the short notice too.
Use `--no-provider` or MCP's
`provider: "none"` to skip basemap requests even with a configured token.

Runtime credentials use `MAPBOX_ACCESS_TOKEN` first, then the default public token
retrieved through a saved official Mapbox CLI login. An explicitly empty
environment variable disables discovery. Repo-local token files aren't read.
The CLI is an optional, separately installed runtime helper, located on PATH or
through `SHOE_MAPBOX_CLI`. It owns OAuth/storage/refresh in `~/.mapbox/`, or
`MAPBOX_CONFIG_DIR`; `shoe` never reads private credential files or saves a second
token copy. Auth commands report the resolved credential path. See the [authentication table](../README.md#which-commands-need-mapbox-authentication)
for command and service requirements. Synthetic basemap geometry is internal to
the rendering benchmark.

## Mapbox requests

Tiles come from the [Vector Tiles API](https://docs.mapbox.com/api/maps/vector-tiles/),
using the `mapbox.mapbox-streets-v8` tileset. The renderer reads water, roads and
place labels. Tile zoom is capped at 14; visible coverage is limited to 64 tiles
by reducing tile zoom when necessary.

Requests use HTTPS. Tokens, request URLs and provider response bodies aren't
included in application diagnostics. Redirects are disabled, requests time out
after eight seconds, and tile bodies are limited to 8 MiB.

## Caching and failures

- Each process keeps up to 64 decoded tiles in an in-memory LRU cache.
- Response cache headers determine freshness, capped at 12 hours; `Age` is subtracted.
- `no-store` and `no-cache` responses aren't retained for reuse.
- There are no persistent tile files, offline-region downloads or bundled Mapbox tiles.
- Each source owns its request state, usable snapshot and refresh deadline. A slow
  or failed source does not block other sources or imported geometry. Mapbox
  Streets is currently the only external source; traffic is not implemented.
- Changes to source request inputs cancel obsolete work and are debounced for 100 ms.
  For Mapbox, relevance follows the visible tile set: movement within the same
  coverage lets an in-flight batch finish and retains fresh snapshots. Continuously
  moving playback can request an update every 250 ms without waiting for the
  camera to settle. Late results are rejected by source generation and request relevance,
  including after source replacement or disabling. Sources without a request-input
  matcher retain exact-viewport relevance checks.
- One tile batch per provider runs at a time, with sequential requests. Waiting
  for a batch is cancellable; network I/O does not hold the cache/statistics mutex.
  Cancellation itself does not increment provider errors or start backoff.
- Failures pause requests for ten seconds; HTTP 429 pauses for at least 60 seconds
  and honours integer `Retry-After` values. Invalid or oversized tile bodies also
  use the ten-second pause. Provider diagnostics exclude response bodies and URLs.
- When a viewer refresh fails without replacement geometry, retain the last usable
  source features and their credits while updating status, statistics and the retry
  deadline. Successful empty responses clear old geometry; replacing or disabling
  a source clears its snapshot. Retained display data does not become a fresh tile
  cache entry.
- The viewer refreshes a stationary view when the earliest displayed tile expires,
  subject to provider backoff. Responses without reusable freshness, including
  `no-store` and `no-cache`, schedule another fetch after 60 seconds. They are still
  never reused from the tile cache for a new request.
- Refresh scheduling has a one-second minimum delay to avoid a tight loop when
  data expires during a batch. Automatic refresh can make Mapbox requests while
  the camera is still and may incur the same account charges as navigation.
- Missing credentials do not cause periodic retries. Viewer discovery runs
  alongside imports; CLI rendering resolves once and MCP resolves lazily for a map
  render, retaining completed unavailable results until restart. Concurrent MCP
  renders share that lookup; a cancelled attempt can be retried. In-viewer setup
  is available through the command palette and replaces
  map/search clients through source lifecycle without resetting the workspace or
  camera. Otherwise restart to rediscover credentials.
- CLI/MCP renders fetch one snapshot per call; they do not start background
  refresh loops. Their `scene.sources` metadata includes each source's status,
  tile statistics and optional `refresh_at` deadline.
- Imported geometry remains usable when the provider fails.

## Authentication and account reporting

`shoe auth login` delegates browser signup/sign-in to the official CLI with a
seven-minute whole-process bound. JSON/account children have a 15-second bound,
an eight-second API budget and 2 MiB output limits; status has an overall
one-minute budget. Cancellation also covers credential locks/refresh operations.
The adapter checks `--schema`, uses the shared `default` profile, clears inherited
account/debug/token overrides for login-based queries and disables update checks.
CLI telemetry is off unless the user explicitly opts in through Mapbox settings;
an environment opt-out wins. The preference is stored under the user's config
directory, separately from Mapbox credentials. Known JSON fields become token-free app messages;
raw API/error bodies are not forwarded by status/discovery.

Viewer connection rechecks report the final credential and Streets result, rather
than treating a working CLI as proof of map access. These rechecks also make tile
requests, including when an environment token is selected.

Status separates login/storage, rendering credentials, token validation (including
the verdict carried in HTTP 200) and Streets access. Login/status Streets probes
are actual tile requests. `--usage` uses the logged-in account, validates the
active public token's account/ID before token-specific statistics, and passes
matching explicit UTC dates to both unfiltered reports. Vector Tiles is selected
inside `shoe`; no matching usage is an empty report. Statistics permissions,
delayed counts and trial-account access are independent of map access.

Logout removes the shared login rather than revoking the account's public token
or environment overrides. No CLI-derived public token is persisted by `shoe`.
The CLI's v0.3.0 OAuth scope set is broader than `shoe` needs and cannot be
narrowed through its command interface. Signup/pricing and optional-install links
are in [auth help and README](../README.md#runtime-configuration).

## Journey location lookups

The journey planner uses user-triggered location lookups:

- **Mapbox Search Box** supplies address/place/named-POI autocomplete and selected
  coordinates. Ctrl-S in the location editor offers an explicit opt-in using the
  existing runtime Mapbox token. A basemap token alone does not start search calls.
  `--no-provider` disables the basemap; explicitly allowed place search remains
  available. Search requests include the text and the current map centre as a bias.
- Coordinates in latitude/longitude order and the current map centre are usable
  without a token or network. This slice does not call Directions or Matrix.

The client uses Go's standard HTTP/JSON libraries, HTTPS, an eight-second timeout,
disabled redirects and a 1 MiB decoded response limit. The host is fixed; request
URLs, tokens, queries and response bodies are excluded from error messages. This
adds no SDK dependencies, following the dependency-minimisation principle in the
[supply-chain reference](https://github.com/lirantal/npm-security-best-practices).

Search waits for three characters and a 350 ms pause, cancelling superseded
requests and rejecting late results. The suggest/retrieve pair shares a unique
session token; new editors, ended sessions and long-running searches get fresh
tokens. Each local session uses at most 49 suggest requests before rotating and
rotates before the documented 180-second endpoint session deadline. Camera motion
does not trigger location lookups. Closing the planner or editing the query cancels
work and rejects stale suggestions/retrievals. Opening or reopening the planner
does not automatically look up a starting point.

Errors leave chosen locations and imported geometry intact, with manual entry
available. There are no automatic network retry/refresh loops for location lookup.
Credits returned for selected Mapbox places remain with the session's locations,
appear in provider credits and contribute to the map footer for visible plans.
Active autocomplete suggestions retain their returned attribution and show it in
the location editor and footer until the suggestions are cleared or replaced by
retrieved location data. The map footer joins distinct credits on one line;
the credits dialog lists them separately.
Place names and credits retain printable Unicode through suggestions, workspace
storage and planned geometry. Control and other non-printable characters are
replaced with spaces before display.

Mapbox Search Box is billed by search session for suggest/retrieve; this is separate
from Directions' free allowance. Its documentation lists coverage in the US, Canada
and Europe.

Search results and plans remain in memory for this session only. The
[Search Box documentation](https://docs.mapbox.com/api/search/search-box/)
restricts returned data to temporary use. No disk cache, favorites or export is
added here. Provider-compatible persistence is tracked in
[issue #10](https://github.com/adamcbrewer/shell-on-earth/issues/10).

## Attribution

[Mapbox's attribution requirements](https://docs.mapbox.com/help/dive-deeper/attribution/)
include a graphical logo and text credits for maps using its data. The current
terminal renderer displays text-only `© Mapbox`, `© OpenStreetMap` and an Improve
this map entry. Credits and links are combined from enabled source snapshots,
with duplicate entries removed. `none` adds no basemap attribution or links;
selected Mapbox places still carry their own search credits.
Press `v` for source status, refresh deadlines and credit links; CLI/MCP render
results include the same attribution information.

The official graphical logo isn't implemented, and a terminal-specific exception
hasn't been verified. The text credits alone don't fulfil that published requirement.
