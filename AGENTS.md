# Mapbox TUI

Go/Bubble Tea terminal map viewer (`mapbox-tui`) with GPX/FIT/TCX/KML imports, optional
Mapbox basemaps, a CLI and stdio MCP tools.

Keep workspace lifecycle and scene rules shared across viewer, CLI and MCP.
Plain CLI/MCP rendering stays ANSI-free; imported geometry remains usable offline.

## Checks

Use the Go version in `go.mod`. Run checks from the repository root:
- `MAPBOX_ACCESS_TOKEN='' make test` builds, tests, vets and checks CLI/MCP parity.
- For viewer changes, run `python3 scripts/interactions.py` after building.

In this environment, prefix Go/Make commands with `mise exec go@1.27.1 --`.

## Read when relevant

- Go changes: [code-style skill and project exceptions](docs/GO_STYLE.md).
- Feature scope and workspace interactions: [implementation plan](docs/IMPLEMENTATION_PLAN.md).
- Verification commands and terminal requirements: [validation](docs/VALIDATION.md).
- Provider changes: [loading, caching and attribution policies](docs/PROVIDERS.md).
- Theme changes: [palette format and API](docs/THEMES.md).
- CLI/MCP contract changes: [README](README.md) and [tool schemas](shared/tools.json).
