# Documentation

The [project README](../README.md) covers building, offline imports, controls,
journey planning, themes, CLI commands and MCP tools. Start there to try the app.

## Current guidance

| Document | Use it for |
|---|---|
| [Mapbox setup and connection checks](MAPBOX_JOURNEYS.md) | Optional CLI installation, saved login, environment tokens, offline fallback and usage reporting |
| [Architecture](ARCHITECTURE.md) | Shared rendering responsibilities, the Go/Charm choice, tradeoffs and benchmark evidence |
| [Workspace rules](WORKSPACE.md) | Document/item identity, loading, selection, visibility, playback, profiles and planned geometry |
| [Provider policies](PROVIDERS.md) | Credentials, independent loading, cancellation, caching, freshness, search, routing, traffic and attribution |
| [Themes](THEMES.md) | Built-in palettes, custom TOML format and the viewer's presentation API |
| [Go style](GO_STYLE.md) | Code-style conventions and project exceptions |
| [Validation](VALIDATION.md) | Local hooks, required CI checks, offline suites, terminal requirements and opt-in live checks |
| [MCP schemas](../shared/tools.json) | Exact tool inputs and outputs; see the [README contract](../README.md#mcp) for usage |

## Historical records

These retain useful decisions and evidence, with an explicit historical notice.
Their branch handovers, commands, completion boxes and provider research describe
the original implementation. Use the current guidance above for development.
Paths are retained so existing references and concurrent documentation work keep
working.

| Document | Preserved context |
|---|---|
| [Workspace implementation plan](IMPLEMENTATION_PLAN.md) | Original slices, acceptance criteria and interaction decisions; current rules are in WORKSPACE.md |
| [Mapbox auth implementation](MAPBOX_AUTH_PLAN.md) | Optional-helper decisions, credential convention and existing-account verification |
| [Route-planning research](ROUTE_PLANNING.md) | Original scope, dated API/provider comparisons, storage caveats and lifecycle decisions |

Historical workspace and traffic measurements, their missing provenance and
reproduction limits are preserved in
[architecture](ARCHITECTURE.md#historical-go-benchmarks). No comparable Rust
benchmark results are available. Generated captures and benchmark reports live
under ignored `reports/`; historical pass claims don't replace a fresh check.

## Outstanding work

GitHub issues hold the current work list. This replaces the stale root TODO and
the unchecked tasks in historical plans; a research option isn't an agreed feature.

- [#3: Activity ordering and sequential playback](https://github.com/adamcbrewer/mapbox-tui/issues/3).
  Current profiles use load order and playback belongs to one selected item.
- [#10: Favourites and offline location caching](https://github.com/adamcbrewer/mapbox-tui/issues/10).
  Storage rights, provenance and offline persistence need explicit decisions.
- [#13: Estimated elevation for planned routes](https://github.com/adamcbrewer/mapbox-tui/issues/13).
  Current plans have no recorded elevation or playback; terrain access is separate.
- [#20: Licence, third-party notices and contribution setup](https://github.com/adamcbrewer/mapbox-tui/issues/20),
  with [#32: Feature-request form](https://github.com/adamcbrewer/mapbox-tui/issues/32)
  and [#33: PR template](https://github.com/adamcbrewer/mapbox-tui/issues/33).
  LICENSE and CONTRIBUTING.md aren't present yet.
- [#23: Protect main after public launch](https://github.com/adamcbrewer/mapbox-tui/issues/23).
  CI checks exist; merge enforcement is a separate setting.
- [#28: Shared architecture flow and SVG diagram](https://github.com/adamcbrewer/mapbox-tui/issues/28).
  Extend the existing ARCHITECTURE.md; [#26](https://github.com/adamcbrewer/mapbox-tui/issues/26)
  already supplied the Go decision and benchmark evidence.
- [#27: Reproducible Mapbox example](https://github.com/adamcbrewer/mapbox-tui/issues/27)
  and [#29: README demo](https://github.com/adamcbrewer/mapbox-tui/issues/29).
  The setup walkthrough is available now; the full integration example and recording remain separate.
- [#36: Terminal Mapbox attribution](https://github.com/adamcbrewer/mapbox-tui/issues/36).
  Text credits exist; acceptance in place of the graphical logo is unconfirmed.
- [#37: Card-free account verification](https://github.com/adamcbrewer/mapbox-tui/issues/37).
  Login, usage, each Directions profile and Traffic v1 need account-specific outcomes.

The old TODO also listed manual plotting (#5), traffic (#4) and CI (#8), which
are complete. Other-source discovery (#6) and package-manager distribution (#25)
were closed as not planned. Their historical proposals aren't launch requirements.

The wider issue list also tracks [viewer interaction work](https://github.com/adamcbrewer/mapbox-tui/issues/21),
[theme triage](https://github.com/adamcbrewer/mapbox-tui/issues/22) and
[shortcuts](https://github.com/adamcbrewer/mapbox-tui/issues/16).
