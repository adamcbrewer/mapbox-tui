# Mapbox

Mapbox is the online map provider. The viewer, CLI and MCP support:

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
through `MAPBOX_TUI_CLI`. It owns OAuth/storage/refresh in `~/.mapbox/`, or
`MAPBOX_CONFIG_DIR`; `mapbox-tui` never reads private credential files or saves a second
token copy. Auth commands report the resolved credential path. See
[runtime configuration](#runtime-configuration) for setup and overrides. Synthetic basemap geometry is internal to
the rendering benchmark.

## Free usage and billing

We aim to make Mapbox TUI usable for personal use within Mapbox's free allowances.
That is an aim, not a guarantee: online features, background refreshes and bugs
can generate billable usage. Users are responsible for monitoring their accounts
and any resulting charges. The app cannot enforce a cap on the Mapbox account.

CLI login (`mapbox-tui auth login`) and in-app login use the same risk notice and
explicit acknowledgement. Existing saved logins and environment tokens also need
acknowledgement before billable features run. Acceptance is versioned and saved
locally; rotating a token does not repeat the same warning. Billing guidance
remains available in Mapbox settings.

Acknowledging the notice saves acceptance separately from the online preference.
CLI and in-app login enable online features only after login succeeds. Failed or
cancelled login preserves the previous offline choice. An explicit **Go online**
or `auth online` enables services immediately once acceptance is recorded.

Billing and usage links in the viewer and interactive CLI notice use terminal
hyperlinks (OSC 8). Open them with your terminal's link-opening gesture; the exact
click/modifier depends on the terminal. The billing confirmation keeps only the
offline/acceptance choices; its links are inline. The main Mapbox usage action and
usage details still support Enter to open the web page. CLI output redirected to a file or
pipe, and output under `TERM=dumb`, keeps plain URLs without escape sequences.

Before accepting:

- Review your account's [usage](https://console.mapbox.com/account/statistics/),
  [invoices](https://console.mapbox.com/account/invoices/) and
  [current product pricing](https://www.mapbox.com/pricing/).
- Set [usage alerts](https://console.mapbox.com/account/settings/usage-thresholds/)
  below the free allowance actually shown for your account. For example, choose
  50%, 80% and 90% of that allowance, in the product's own units. Do not assume
  every product has a free allowance.
- Remember that alerts do not stop requests or charges. Standard pay-as-you-go
  accounts have [no configurable spending cap](https://docs.mapbox.com/accounts/faq/can-i-set-up-a-cap-for-monthly-spending/).
  Demo accounts have different restrictions; check the console for your terms.
- Consider other applications using the same account. Traffic adds Vector Tiles
  requests; Search Box sessions, Geocoding and Directions are separate products.
  Stationary maps and traffic can refresh automatically while online.

**Continue offline** and **Go offline** persistently disable online maps, traffic,
place lookup and routing. Imported geometry, playback, coordinate entry and local
favourites remain available. An authenticated account still checks account usage
once at every viewer startup, including offline. This is an explicit exception
for statistics; it does not probe tiles, search or routes. Going online again is
an explicit action in Mapbox settings or `mapbox-tui auth online`.

For unattended use, review the notice before recording acceptance:

```sh
mapbox-tui auth online --accept-billing
# Or acknowledge and sign in through the optional official CLI:
mapbox-tui auth login --accept-billing
# Persist offline mode:
mapbox-tui auth offline
```

`MAPBOX_TUI_ACCEPT_BILLING=1` acknowledges the notice for a process, including
headless render and MCP. It does not override saved offline mode. An interactive
login or `auth online` also saves acceptance. Both commands always display the
billing guidance, including when acceptance is already saved or supplied through
a flag or environment variable; they only prompt when acceptance is missing. Unacknowledged CLI/MCP online renders
return an actionable error, without making a tile request. Offline renders using
`--no-provider` or MCP `provider: "none"` remain available without acceptance.

### In-app account usage

Mapbox settings shows current-month, account-wide product totals. **Usage details**
adds days with reported usage, latest reported date, UTC reporting period, report
generation time and fetch time. The saved-login account is named above the totals
in both views. When maps use an environment token, a note explains that its account
may differ; we do not make extra requests to determine ownership. Only app-related
products are displayed; the web usage link shows the full account.

Counts are kept in their original units. Search sessions cannot be added to tile
or Directions requests. Geocoding product variants remain separate. A missing
product is not a confirmed zero. Days reported describes the returned daily rows,
not the number of complete days in the month.

The viewer attempts one asynchronous statistics load at startup and after a new
login, with no periodic polling or cache carried across launches. It queries the
saved login even if an environment token is selected or explicitly empty. Missing
login, unsupported accounts, permission failures and malformed/incomplete responses
show usage as unavailable. They do not block otherwise acknowledged online use.
Reports older than 24 hours or from a previous UTC month are labelled stale;
a report generated recently may still omit delayed usage. Restart to fetch again.

The reporting period is the first day of the current UTC month through today,
matching the [standard billing calendar](https://docs.mapbox.com/accounts/guides/invoices/#billing-period).
A newly created/reactivated account may have a shortened first billing period.
Statistics can lag by 24 hours and are not an invoice or remaining allowance.

### Supported interfaces and limitations

Checked on 17 October 2026: the official CLI's
[`usage` command](https://docs.mapbox.com/cli/reference/usage/) documents a private
preview Statistics API requiring account enablement and `statistics:read`.
It worked for the tested account, but access cannot be assumed for other accounts.
The general Statistics guide still says there is no programmatic API, so the
CLI's preview documentation is the more specific source for this integration.

A live query returned daily product usage, reporting period and generation
metadata. It did not return free allowances, prices, remaining allowance,
configured alert thresholds or notification events. No documented supported
account-plan/allowance API or notification webhook was found in the public
Accounts API or CLI. Email alerts are configured in the console; we do not retrieve
or subscribe to them. Statistics request pricing and freshness guarantees beyond
the published guidance remain unverified. No product probes are used for refresh.

This version deliberately shows counts and console links, without inferred free
quotas, allowance percentages, cost estimates, local request accounting or
app-generated threshold alerts. Public pricing alone does not identify an
account's contracted terms.

## Runtime configuration

The [README](../README.md#mapbox) covers the usual saved-login setup.
Mapbox CLI owns the shared `default` profile at `~/.mapbox/credentials.json`;
`MAPBOX_CONFIG_DIR` relocates it. `MAPBOX_TUI_CLI` selects a CLI executable
outside `PATH`. Credentials are owner-only JSON files, rather than an OS keychain.

For scripts, a public token can override the saved login:

```sh
MAPBOX_ACCESS_TOKEN="pk.your-public-token" ./bin/mapbox-tui
./bin/mapbox-tui auth status --usage --json
```

An explicitly empty `MAPBOX_ACCESS_TOKEN` disables credential discovery and
first-run setup. `.env` and repo-local token files aren't loaded. Tokens stay
runtime-only. Restart a viewer or MCP process to rediscover credentials, or
use the viewer's Mapbox account setup action.

`--no-provider` (MCP: `provider: "none"`) disables the basemap. Search and
routing still use available credentials independently of basemap visibility.
Imports, inspection, playback and manual journey coordinates work offline.

`auth status` reports login, rendering-token source, validity and Streets
access; a Streets probe only runs when online use is acknowledged and enabled.
`--usage` adds current-month account/token statistics for app-related products. Completed reports exit
zero even when sections are unavailable. `auth logout` removes the shared CLI
profile without revoking public tokens or clearing other running processes.

The viewer saves billing acknowledgement, offline mode, traffic and CLI-telemetry preferences in
`~/.config/mapbox-tui/settings.json` on Linux (`XDG_CONFIG_HOME` relocates it).
Theme, camera, workspace and travel-mode choices last for the session. CLI
telemetry is opt-in and an explicit environment opt-out takes precedence.

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
  Streets and the optional viewer Traffic v1 overlay have separate snapshots,
  caches, statistics and request state.
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

## Traffic v1 overlay

The viewer's main Ctrl-K menu includes **Toggle traffic**, an
off-by-default toggle saved alongside the telemetry preference. It uses
`mapbox.mapbox-traffic-v1` through the same bounded Vector Tiles transport and
selected environment/saved-login credential as Streets, with an independent
provider/cache/source. It does not use the separately licensed raw Traffic Data
product or change journey routing modes.

The main-menu entry is greyed out until authenticated saved-login token retrieval,
a successful Streets request or an explicit connection check verifies the selected
connection. An unchecked environment token is not enough. Verification resets for
replacement credentials and access-denied Streets responses. Saved Traffic-on
preferences wait for verification, and disabling Traffic remains available after
lost verification. A verified account does not itself prove Traffic v1 entitlement;
traffic requests still report product-specific access failures separately.
Cache-only Streets loads cannot restore verification after a failed connection
check; restoring it requires fresh account/network success.

- The focus is the **current map centre**, not the selected route point or the last
  clicked coordinate. Traffic follows panning, camera follow and rotation. Visible
  tile requests intersect the viewport with the radius's geographic bounding box;
  tile zoom is clamped to 6–14 and the existing 64-tile budget still applies.
- Rendering clips traffic dots to a geodesic **20 statute mile (32,186.88 m)**
  radius. Roads crossing the circle remain visible inside it even when their
  endpoints lie outside. Clipping happens against the displayed camera, so a warm
  snapshot cannot keep the old focus radius after movement within the same tiles.
- The `traffic` layer's `congestion` values map to low/moderate/heavy/severe roles.
  `closed=yes` takes precedence; missing/unrecognised congestion is not drawn as
  low traffic. The documented tileset does not expose detailed incident records
  or numeric speed measurements.
- Worst severity wins when road directions or nearby roads share a terminal cell.
  Place labels, imported/planned routes, markers and the cursor remain above the
  overlay. This does not claim lane/direction separation or recolour a route from
  nearby road traffic. ASCII symbols are `1`–`4`; closures use `X` in both modes.
  The map footer and credits contain a themed legend.
- Traffic cache freshness subtracts `Age` from response freshness first, then caps
  the remaining lifetime at five
  minutes. Uncacheable responses are not reused and schedule another fetch after
  five minutes. Mapbox documents congestion updates approximately every eight
  minutes; a refresh does not guarantee a new observation. The existing-account
  check returned a roughly one-minute cache deadline.
- Traffic snapshots expose `fetched_at` and `stale` through shared source metadata.
  Fetch time is not a provider observation timestamp. Failed multi-tile batches
  discard partial geometry and retain the last complete snapshot/fetch time with
  stale status in credits. Normal network
  failures and rate limiting use the existing backoff policy.
- HTTP 401/403 stops traffic retries until explicit off/on, a connection recheck
  or credential replacement. A denied traffic source does not repeatedly make
  requests while panning and never replaces the Streets source or imported data.
- `--no-provider` disables only the basemap; an explicitly enabled viewer traffic
  toggle can still use connected credentials. An explicitly empty
  `MAPBOX_ACCESS_TOKEN` disables credential discovery and traffic requests.
  CLI/MCP render calls do not load the viewer's traffic preference or start a
  traffic refresh loop; shared rendering/source rules remain the same.

The live check on **10 October 2026** confirmed Traffic v1 access with the existing
saved-login account: TileJSON advertised zooms **6–16**, and one London zoom-14
tile decoded **1,043 road features**. This proves access for that account, not all
account types; card-free trial access has not been tested. See
[live account checks](#live-account-checks) for the opt-in check.

Traffic calls are additional Vector Tiles requests, including stationary refresh.
See current [pricing](https://www.mapbox.com/pricing#vector-tiles-api),
[trial limits](https://docs.mapbox.com/accounts/guides/signup/), and the
[Traffic v1 schema](https://docs.mapbox.com/data/tilesets/reference/mapbox-traffic-v1/).
The raw Traffic Data licence requirements alone do not establish Traffic v1 tile
entitlements. Traffic-aware Directions and route incident details remain separate.

## Authentication and account reporting

`mapbox-tui auth login` delegates browser signup/sign-in to the official CLI with a
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
matching explicit UTC dates to both unfiltered reports. App-related products are selected
inside `mapbox-tui`; no matching usage is an empty report. The `products` array
includes counts, units, reported days and latest reported date. Legacy Vector Tiles
fields remain available when nonzero; absent counts must be interpreted with the
report state, rather than assumed to be zero. Statistics permissions,
delayed counts and trial-account access are independent of map access.

Logout removes the shared login rather than revoking the account's public token
or environment overrides. No CLI-derived public token is persisted by `mapbox-tui`.
The CLI's v0.3.0 OAuth scope set is broader than `mapbox-tui` needs and cannot be
narrowed through its command interface. See the [README](../README.md#mapbox)
for CLI installation and auth help for signup and pricing links.

## Journey location lookups

Quick search (`/`) and the journey planner share user-triggered location lookups:

- **Mapbox Search Box** supplies address/place/named-POI autocomplete and selected
  coordinates. It is automatically available in quick search and location editors
  when the shared runtime token is present, including saved-login discovery, after
  accepting the billing notice and enabling online mode. Typing three characters schedules
  a debounced lookup; Ctrl-S retries. An empty editor and credential discovery without a query make no
  search calls. If credentials arrive while a query is being edited, lookup starts
  after the usual pause. `--no-provider` disables only the basemap; search availability
  still follows the selected token. An explicitly empty environment token disables
  both discovery and search. Requests include text and the current map-centre bias.
  Complete coordinates and unfinished numeric coordinate prefixes do not trigger
  searches. Numbered addresses and plain numeric postcodes remain searchable.
- Coordinates in latitude/longitude order and the current map centre are usable
  without a token or network. Location lookup does not call Directions or Matrix.

The client uses Go's standard HTTP/JSON libraries, HTTPS, an eight-second timeout,
disabled redirects and a 1 MiB decoded response limit. The host is fixed; request
URLs, tokens, queries and response bodies are excluded from error messages. This
adds no SDK dependencies, following the dependency-minimisation principle in the
[supply-chain reference](https://github.com/lirantal/npm-security-best-practices).

Search waits for three characters and a 350 ms pause, cancelling superseded
requests and rejecting late results. The suggest/retrieve pair shares a unique
session token; new search dialogs/editors, ended sessions and long-running searches
get fresh tokens. Each local session uses at most 49 suggest requests before rotating
and rotates before the documented 180-second endpoint session deadline. Camera motion
does not trigger location lookups. Closing search/the planner or editing the query
cancels work and rejects stale suggestions/retrievals. Opening or reopening the planner
does not automatically look up a starting point.

Errors leave chosen locations and imported geometry intact, with manual entry
available. There are no automatic network retry/refresh loops for location lookup.
Credits returned for selected Mapbox places remain with the session's locations,
appear in provider credits and contribute to the map footer for visible plans.
Quick search retains the last accepted place's credits for the map view until the
next accepted search result or manual coordinate entry replaces them. It frames
about a one-mile radius around the location without editing any journey or calling
Directions.
The quick-search viewport stays inside the renderable Mercator world rather than
wrapping tiles or geometry. Near world edges it shifts inward, resetting north-up
only if needed to keep the accepted location visible.
Active autocomplete suggestions retain their returned attribution and show it in
the context bar and credits dialog until the suggestions are cleared or replaced by
retrieved location data. The map footer joins distinct credits on one line;
the credits dialog lists them separately.
Place names and credits retain printable Unicode through suggestions, workspace
storage and planned geometry. Control and other non-printable characters are
replaced with spaces before display.

Mapbox Search Box is billed by search session for suggest/retrieve; this is separate
from Directions' free allowance. Its documentation lists coverage in the US, Canada
and Europe.

Search results and plans remain in memory for this session only. The local
[favourites library](USAGE.md#favourite-places) saves user-entered locations and
Geocoding results. Alt-S on a search suggestion sends its Mapbox ID to the
Geocoding v6 forward endpoint with `autocomplete=false`, using the API’s default
geocoding mode for all requests, including saves. The returned ID must match the
selected result. Its name, coordinates and ID are persisted, along with the source.
Saved favourites derive their Mapbox display credit from that source; response
attribution notices are not repeated in the JSON. Existing favourites remain
readable.
Saving leaves the current search and journey unchanged and marks the result with
a star. It does not retrieve a Search Box feature or end its session.

Geocoding is billed separately from Search Box. Geocoding v6 excludes POIs, so
named venues/categories are rejected before making a save request.
Missing/mismatched features, invalid data, account errors and cancelled requests
leave the library unchanged. Query edits,
closing the editor and credential changes cancel pending saves; only a response
still owned by the active editor may write a favourite.

## Journey road/path routing

The planner defaults to **Driving** when a Mapbox token is connected, otherwise
**Offline** (offline straight lines). **Travel mode** shows the current selection
in the planner's action list and offers Walking, Cycling, Driving and Offline.
Late credential discovery enables the default Driving plan without overriding an
explicit Offline choice. Search, routing and basemap selection are independent; `--no-provider`
only disables tiles. Routing consumes the same environment/saved-login token,
including explicit-empty environment opt-out. Streets/auth success is not treated
as proof of Directions access.

- A completed location/order/mode edit, reversal or undo requests one full GeoJSON
  route through all ordered locations. Incomplete stops wait without premature
  warnings. Ctrl-D explicitly recalculates/retries. Show journey on map waits for
  the pending current route, or calculates a missing/stale route before fitting;
  failure keeps the planner open with feedback. Dismissal or another edit cancels
  the pending camera-fit action. Typing, camera movement, redraw and reopening do
  not make Directions requests. No automatic retry or refresh loop is added.
  Accepting unchanged locations or the current mode does not invalidate or request
  a route; Ctrl-D remains the explicit refresh/retry action.
- User locations remain unchanged; snapped locations and route geometry are separate.
  A/B and waypoint markers stay at the chosen locations, and map fitting includes
  those markers alongside the routed path.
  The displayed distance/duration come from Directions, with travel time labelled
  estimated. Geometry has no recorded timestamps, playback or elevation.
- Workspace-owned cancellation and monotonically increasing request/edit identities
  reject obsolete successes and failures after edits, retries, mode changes, undo,
  removal, clearing and credential replacement. Closing the planner lets a valid
  route calculation finish for its workspace item. Completion never selects a
  different item or resets its playback. While the planner is open, committed
  locations and resolved geometry fit the live map; closed planners don't move
  the camera on completion.
- Edits retain the previous result as stale but display a current straight-line preview
  without its obsolete estimates. Failures preserve stop intent and straight-line geometry;
  full cause/remedy is available through Ctrl-U's scrollable routing details.
  Static usage explanations live here rather than in the planner action list.
  A failed recalculation also marks a previously current result stale and restores
  the straight-line preview, even when the stops and mode have not changed.
- Validate 2–25 locations and total geodesic distance between stops before HTTP:
  walking 1,000 km, cycling 1,500 km and driving 10,000 km. Limits reject the whole
  plan with its locations intact; no silent splitting/truncation. Provider rejection
  still reports HTTP 422 and the profile limits, without forwarding response bodies.
- HTTPS/fixed host, eight-second timeout, disabled redirects and an 8 MiB decoded
  body limit match existing bounded-provider practices. Errors omit tokens, URLs,
  stop coordinates and raw responses. No SDK or other dependency is added, following
  the [supply-chain reference](https://github.com/lirantal/npm-security-best-practices).
- HTTP 429 applies client-wide backoff of at least 60 seconds, honouring integer
  or HTTP-date `Retry-After`. An explicit request during backoff returns the remaining
  wait instead of calling Mapbox. No assumption is made that trial accounts have
  the standard 300-request/minute allowance.
- Routing data stays in memory for this session only. Current resolved geometry
  contributes `© Mapbox © OpenStreetMap` to the footer and credits dialog even with
  the basemap off; stale snapshots aren't drawn or credited as current routes.

Each multi-stop calculation is **one Directions request**, separate from Search Box
and tile usage. Standard pricing advertises 100,000 requests/month free; card-free
trial access currently lists 10,000/month and lower rate limits. These are account-wide
allowances, not app-enforced spending caps. Follow current
[pricing](https://www.mapbox.com/pricing#directions-api) and
[signup/trial limits](https://docs.mapbox.com/accounts/guides/signup/) rather than
assuming the same allowance or capability for every account. The three profiles
can be checked independently using the opt-in
[live account checks](#live-account-checks).
Traffic-aware routing, alternatives, instructions, elevation and persistent caching
remain separate work.

## Live account checks

These checks use your saved login or public token and make account requests:

```sh
make build
python3 scripts/mapbox_auth_check.py
python3 scripts/mapbox_check.py
python3 scripts/interactions.py --mapbox
```

The authentication script checks the saved CLI login and ignores
`MAPBOX_ACCESS_TOKEN`. The opt-in flags and request counts for the Directions and
Traffic integration tests live beside the tests in
[directions_live_test.go](../go/directions_live_test.go) and
[traffic_live_test.go](../go/traffic_live_test.go). `make mapbox-check` checks
installed CLI schemas without account requests.

Record the revision, tool versions, date, account type and token-free results.
Existing-account checks don't establish card-free trial access.

## Attribution

Mapbox TUI renders text rather than images, so it identifies Mapbox by name instead
of displaying a graphical logo. Mapbox-powered output includes `© Mapbox`,
`© OpenStreetMap` and an Improve this map entry. The viewer footer shows text
credits; press `v` for source status, refresh deadlines and credit links. CLI/MCP
render results include the same attribution information.

Credits and links are combined from enabled source snapshots, with duplicates
removed. `none` adds no basemap credits; Mapbox search results and resolved routes
retain their own attribution even without a basemap.

[Mapbox's published requirements](https://docs.mapbox.com/help/dive-deeper/attribution/)
also specify a graphical logo. Text attribution is the current terminal
implementation; acceptance of text in place of the logo hasn't been confirmed.
