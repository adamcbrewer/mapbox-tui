# Map providers

The viewer supports two providers:

- `mapbox`: Mapbox Streets v8 vector tiles, using your own access token.
- `none`: imported file geometry on an empty canvas; no authentication or network
  requests are needed.

The viewer, CLI `render` and MCP `render_map` use Mapbox when a token is configured,
otherwise `none`. The file/track is optional. No file and no token produces an
empty canvas with setup guidance. Explicit Mapbox requests without credentials
warn without hiding the imported geometry. Use `--provider none` or MCP's
`provider: "none"` to skip basemap requests even with a configured token.

Credentials are read at runtime from `MAPBOX_ACCESS_TOKEN`, or an ignored
`mapbox.local.json` in the working directory. An explicitly empty environment
variable disables the local token. See the [authentication table](../README.md#which-commands-need-mapbox-authentication)
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
- Missing credentials do not cause periodic retries. Credentials are read at
  startup; restart with the configured token to enable requests.
- CLI/MCP renders fetch one snapshot per call; they do not start background
  refresh loops. Their `scene.sources` metadata includes each source's status,
  tile statistics and optional `refresh_at` deadline.
- Imported geometry remains usable when the provider fails.

## Attribution

[Mapbox's attribution requirements](https://docs.mapbox.com/help/dive-deeper/attribution/)
include a graphical logo and text credits for maps using its data. The current
terminal renderer displays text-only `© Mapbox`, `© OpenStreetMap` and an Improve
this map entry. Credits and links are combined from enabled source snapshots,
with duplicate entries removed. `none` displays no Mapbox attribution or links.
Press `v` for source status, refresh deadlines and credit links; CLI/MCP render
results include the same attribution information.

The official graphical logo isn't implemented, and a terminal-specific exception
hasn't been verified. The text credits alone don't fulfil that published requirement.
