# Route planning

Implementation notes and provider research for
[issue #5](https://github.com/adamcbrewer/shell-on-earth/issues/5).
Research checked against provider documentation on 2026-10-08. The confirmed
journey-planner scope is implemented; other API capabilities below are research
findings, not additional implemented features.

## Product direction

This branch delivers an A-to-B journey planner with ordered intermediate
waypoints. Place search supports editing the journey's locations, including
addresses, towns and named POIs, with autocomplete where supported. Use
[Google Maps route planning](https://support.google.com/maps/answer/144339) as the
interaction reference, translated into keyboard-accessible terminal controls.
Live GPS navigation, voice guidance and in-car/mobile use are outside this scope.
Road/path-following remains an optional way to estimate a planned trip's distance
and time; it does not imply live navigation.

The confirmed baseline is manual plotting that works without a network, token or
account. Account-dependent enhancements are explicit options. Another paid
service needs a substantial benefit over the existing Mapbox integration and
must be offered as a user choice, with its setup and cost explained.

## Confirmed feature scope

- Type a destination and choose a matching location. Use the same search behaviour
  to edit the starting point and intermediate waypoints.
- Create an A-to-B journey alongside imports without modifying recorded data.
- Search/pick A, B and intermediate waypoints; edit, add, delete and reorder them.
- Reverse the complete waypoint order, not just the endpoints. A, W1, W2, B
  becomes B, W2, W1, A. This is one undoable edit.
- Show start/end markers, ordered waypoints and the plotted distance.
- Preserve imported disconnected geometry and unknown measurements.
- Keep controls available through the command palette and keyboard, including
  narrow terminals and ASCII mode.

Favorites and persistent/offline geolocation caching are tracked together in
[issue #10](https://github.com/adamcbrewer/shell-on-earth/issues/10).
Do not introduce automatic destination history or a persistent lookup cache
in this slice. Area/category research and turning research into a trip are excluded
from this feature.

Manual geometry is explicitly labelled straight-line plotting, not road-following
directions. Distance describes the plotted geometry, not a promise of a navigable
route. Unknown elevation stays unknown; manual routes have no recorded timestamps.

Confirmed search provider: Mapbox Search Box, reusing the existing token with an
explicit opt-in. Ctrl-G opens the planner; its location editor uses Ctrl-S to
enable search, or coordinates/Alt-M for offline input. Ctrl-N adds a waypoint,
Ctrl-R reverses, Ctrl-Z undoes, Alt-Up/Down reorders, Delete clears/removes and
Ctrl-F shows/fits the plan. The same actions are available as selectable rows
where applicable. See the [controls](../README.md#journey-planner).

This slice draws manual straight-line geometry. No Directions/Matrix calls,
road-route generation or travel-time estimates are included.

## Mapbox enhancements

### Directions: separate follow-up

Tracked in [issue #12](https://github.com/adamcbrewer/shell-on-earth/issues/12).

The [Directions API](https://docs.mapbox.com/api/navigation/directions/) can supply:

- Walking, cycling, driving and traffic-aware driving profiles.
- Road/path-following geometry through ordered stops, distance and estimated time.
- Turn instructions and alternative routes when available.
- Via points for shaping a route without treating every point as a stop.
- Avoidance options such as motorways, tolls and ferries, depending on profile.
- Departure/arrival planning, curb approaches and heading-aware snapping.

Avoidance is best-effort; provider notifications identify violated exclusions.
Standard profiles accept 2–25 input coordinates per request. Longer plans need an
explicit strategy; silently truncating stops or splitting requests changes the
route and request count. Keep traffic-overlay work in issue #4.

Use the existing Mapbox credential mechanism. A basemap token does not implicitly
enable routing. Offer Manual (offline) and Mapbox road-following as separate
choices, and explain that Mapbox routing uses the user's account allowance.

The [published pricing](https://www.mapbox.com/pricing#directions-api) includes
100,000 Directions requests per month free, then $2 per 1,000 in the next tier.
Multiple stops in one call count as one request. Allowances are account-wide;
local request counts cannot establish whether an account will be charged.
The API's published rate limit is 300 requests/minute. Routing should follow
completed edits or an explicit calculate action, not autocomplete keystrokes.

If online route generation is chosen, walking/cycling/driving, route distance and
estimated duration provide planning benefits. Textual navigation instructions,
alternatives and avoidance controls are research findings, not agreed requirements
for this slice.

### Search: useful, separate from routing

[Geocoding v6](https://docs.mapbox.com/api/search/geocoding/) finds addresses and
places; it does not supply POIs. Temporary geocoding currently includes 100,000
monthly requests free, then $0.75 per 1,000 in the next tier. Each autocomplete
keystroke can count as a request, so an explicit submit is a sensible first UI.

[Search Box](https://docs.mapbox.com/api/search/search-box/) adds POIs,
autocomplete and search along a route, such as cafes with a limited detour.
Its documentation lists coverage in the US, Canada and Europe. It has separate
session/request billing; the pricing page lists both preview and standard rates,
so do not infer the applicable account rate from one advertised free allowance.
Search-result ETAs additionally incur Matrix API charges.

Both products have storage constraints relevant to saved routes. Temporary
Geocoding results cannot be cached; permanent geocoding permits storage but
requires billing eligibility and has separate pricing. Search Box results are
for temporary use; storing position data requires contacting Mapbox. Do not
assume search-selected positions or names can be exported just because they are
now part of a route. Define persistence rights before adding saved search stops.

The account-free fallback is typed coordinates, map picking and user-supplied
waypoints. Search should remain an independent option.

### Elevation: useful for outdoor plans, another data request

Tracked in [issue #13](https://github.com/adamcbrewer/shell-on-earth/issues/13).

Directions does not provide a general elevation profile. The
[elevation guide](https://docs.mapbox.com/data/tilesets/guides/access-elevation-data/)
describes Terrain-RGB raster heights and Terrain vector contours via Tilequery.
These use separate map API requests; contours have 10-metre increments.

[Terrain-RGB](https://docs.mapbox.com/data/tilesets/reference/mapbox-terrain-rgb-v1/)
is directly accessible as raster tiles, but stopped receiving data updates in
December 2021. Its newer replacement,
[Terrain-DEM](https://docs.mapbox.com/data/tilesets/reference/mapbox-terrain-dem-v1/),
is documented as SDK-only and unavailable through the Raster Tiles API. Do not
design this Go terminal viewer around a GL JS-only terrain capability.

Terrain-derived elevation is estimated, distinct from recorded measurements.
Leave missing elevation blank in the baseline and make enrichment optional.

Current Mapbox Raster Tiles pricing includes 750,000 tile requests/month free,
then $0.25 per 1,000 in the next tier. Tilequery includes 100,000 queries/month
free, then $1.50 per 1,000. These allowances are separate from Directions/search.
One raster tile can serve many samples, while a point-query approach consumes a
request for each queried point.

[Open Topo Data](https://www.opentopodata.org/#public-api) is a useful no-account
alternative: up to 100 locations/request, one request/second and 1,000 calls/day
on the free public service. Its [API](https://www.opentopodata.org/api/) supports
batching, interpolation and null elevations for missing coverage. Dataset
resolution and extent vary; global datasets are roughly 30 m, with finer regional
options. A 20 km route sampled every 100 m needs about 201 points, or three batches.
Public hosting is best-effort; choose an explicit usage budget and dataset.

Elevation samples follow the displayed geometry, not just user waypoints. Road
routing gives the profile the real road/path shape, but sampling can also work on
clearly labelled manual geometry. Heights describe terrain, not necessarily a
bridge deck or tunnel. Keep missing values distinct from a valid zero, and define
smoothing before adding ascent/descent totals.

### Power tools worth retaining as future options

| Tool | Useful experience | Boundary |
|---|---|---|
| [Optimization v1](https://docs.mapbox.com/api/navigation/optimization-v1/) | Suggest an efficient visit order | 2–12 coordinates; apply as an undoable user action, not automatic reordering |
| [Matrix](https://docs.mapbox.com/api/navigation/matrix/) | Compare travel times between candidate stops | Billed per origin/destination element; unnecessary for a single ordered route |
| [Map Matching](https://docs.mapbox.com/api/navigation/map-matching/) | Derive a road-aligned copy of a noisy recorded trace | Up to 100 coordinates per call; do not replace originals or treat matching as routing |
| [Isochrone](https://docs.mapbox.com/api/navigation/isochrone/) | Show a reachable area within a time/distance budget | Up to four contours, 60 minutes or 100 km; terminal use requires resolving its published Mapbox-library/SDK display restriction |

Directions, Optimization v1 and Map Matching currently each advertise 100,000
monthly requests free and $2 per 1,000 in the next tier. Matrix advertises those
tiers for elements, not requests. Map Matching also publishes a Mapbox-library/SDK
display restriction. Keep those restrictions visible when evaluating terminal
integrations rather than assuming every HTTP endpoint suits this renderer.

Mapbox remains the simplest integration for users with an existing account.
The alternatives below offer meaningful benefits for account-free browsing or
storing research results. Self-hosted routing is a possible later account-free
enhancement, but requires a routing engine and downloaded road data; it is not
provided by cached visual map tiles.

## User story

| Story | Proposed interaction |
|---|---|
| Plan A to B with waypoints | Search or manually enter the start, destination and waypoints; edit locations, add/reorder waypoints, reverse the entire journey |

Use explicit actions rather than labelling both camera movement and route planning
as "navigate". A search preview does not modify the plan or replace imported data.
The journey's location fields use the same place-search behaviour. Typing previews
matching locations; selecting one updates the field being edited. No standalone
research or favourites workflow is added in this branch.

Autocomplete should wait for a minimum query length and a short pause in typing,
reject superseded responses and preserve keyboard selection. An Enter-to-search
mode remains available for providers without typeahead or when conserving quota.
Provider selection must not silently send the same search to additional services.

## Free alternatives

Allowances below were checked on 2026-10-08. Free hosted services still require
network access; a fully offline global address/POI search requires local datasets.

| Provider | Capabilities and allowance | Main caveat |
|---|---|---|
| [Photon](https://github.com/komoot/photon) | OSM place/address search, typeahead, reverse lookup and location bias; public demo needs no key | Public demo permits reasonable use only, can throttle/ban and has no availability guarantee; not a full nearby-POI catalogue |
| [Geoapify](https://www.geoapify.com/pricing/) | Address search/autocomplete, places and routing; 3,000 shared credits/day, no credit card required | Account/key and attribution required; credit cost depends on operation, result count and route size |
| [LocationIQ](https://locationiq.com/pricing) | Geocoding, autocomplete, nearby POIs and routing; 5,000 requests/day, 2/sec and 60/min | Account/token and attribution required; nearby POI API is beta |

[Geoapify's credit schedule](https://www.geoapify.com/pricing-details/) counts a
geocoding/autocomplete request as one credit. A places request with up to 20
results costs one credit; larger results cost more. Routing costs one credit per
pair of consecutive waypoints, plus costs for long routes. Its
[geocoding documentation](https://www.geoapify.com/geocoding-api/) explicitly
allows stored results with retained attribution: a significant benefit for saved
research and exported plans compared with temporary Mapbox geocoding.
Verify storage rules for each additional API before assuming that geocoding's
permission applies to all provider data.

LocationIQ's pricing FAQ permits stored response data indefinitely but limits
request/response caching on a free account to 48 hours. Keep those two uses
distinct when considering a persisted search cache.

[Public Nominatim policy](https://operations.osmfoundation.org/policies/nominatim/)
forbids client-side autocomplete, caps use at one request/second across an
application's users, requires identifying requests/attribution and does not permit
systematic POI harvesting. It is not a suitable default for the proposed search
experience.

[Overpass](https://wiki.openstreetmap.org/wiki/Overpass_API#Public_Overpass_API_instances)
can query OSM POIs by category and area without an account. Public-instance policy
and reliability vary; the main instance discourages using it as a general app
backend. It is an option for bounded, explicit research queries against a suitable
chosen endpoint, not a guaranteed global search service or typeahead backend.

[openrouteservice](https://openrouteservice.org/services/) offers free-account
routing, POIs, geocoding, elevation and reachability analysis. Exact current
per-endpoint plan quotas need confirmation in its account portal before selection.

## Biggest caveats and proposed delivery order

- POI coverage, address accuracy and available metadata vary by region/provider.
  Missing hours, ratings or results must not be invented or treated as proof that
  a place does not exist. Present full locality/country context for ambiguous names.
- Storage/export rights matter more for research than turn instructions. Carry
  provenance and credits with selected results; distinguish transient previews
  from persistable user points and provider data.
- Free usage is not the same as no account, no network or guaranteed service.
  Display provider requirements; quota/failure leaves the plan and imports usable.
- Autocomplete, POI searches and routing have distinct usage units. Camera motion
  must not repeatedly consume search/route quota.
- A place coordinate is not necessarily an exact entrance.

Delivery scope:

1. Use Mapbox Search Box for destination search with the existing runtime token
   and explicit opt-in.
2. Deliver the journey planner: searched/manually entered start and destination,
   intermediate waypoints, location edits and full-order reversal.
   Retain an offline manual baseline.
3. Track favourites and persistent/offline geolocation caching in issue #10;
   do not implement either here.

Photon is a candidate for light no-signup search. Existing Mapbox is the easiest
account-enabled option. Geoapify is the strongest all-in-one alternative found
for this scope, particularly when saved geocoding results are important. Do not
implement every investigated adapter in the first slice.

## Shared model and lifecycle

- Planned routes own editable intent (points/stops and part boundaries) separately
  from recorded imports and from any provider-resolved path.
- Reuse stable workspace item identity, visibility, colour and shared scene rules.
  Generated routes must not share an empty-path duplicate-import identity.
- Editing prepares a new geometry snapshot and replaces it atomically. Recompute
  bounds/distance and invalidate elevation/playback together, including on undo.
- Pause playback on an edit. Preserve unrelated items, source ownership and colours.
- Reversal changes the waypoint intent. If online routing is enabled, recompute
  the route; reversing a resolved polyline alone can violate one-way/access rules.
  Invalidate old route estimates while retaining the user's reversed waypoints.
- Online results carry the route identity and edit revision. Cancel obsolete
  requests and reject results after another edit, undo, removal or mode change.
- Request after a completed edit, not during every drag event. Camera movement and
  stationary basemap refresh must not trigger directions requests.
- Failure leaves the user's points and last usable result available. Clearly
  distinguish an old routed result from a current manual preview; invalidate old
  estimates/instructions rather than presenting them for edited geometry.
- Estimated journey duration never becomes recorded point timestamps.
- Keep basemap selection independent from routing selection. Optional-provider UI
  identifies the benefit, account/token requirement, usage model and offline choice.

## Verification scope

Focused tests cover edit/undo atomicity, full-order reversal, invalid coordinates,
independent items, stable references, distance/bounds recalculation, unresolved
waypoint gaps and unknown measurements. Planned geometry has no playback or
recorded elevation profile. Fake location transports cover search opt-in,
cancelled queries, stale results, HTTP/body errors, token/query redaction and search
session rotation. Terminal interactions
cover offline input, reversal/undo, waypoint reorder/removal, map fit and reopening
at wide/narrow sizes and in ASCII mode without live/billable lookup calls.

Online route generation remains future work and would require its own fake-provider
checks for profile limits, failures, cancellation and superseded results.

Run the repository checks and terminal interaction script for each delivered slice.
