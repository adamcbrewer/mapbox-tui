# Mapbox TUI

Go/Bubble Tea terminal map viewer (`mapbox-tui`) with GPX/FIT/TCX/KML imports, optional
Mapbox basemaps, a CLI and stdio MCP tools.

Keep workspace lifecycle and scene rules shared across viewer, CLI and MCP.
Plain CLI/MCP rendering stays ANSI-free; imported geometry remains usable offline.

## Checks

Use the Go version in `go.mod`. Run checks from the repository root:
- `make test` builds, tests, vets and checks CLI/MCP parity; it disables Mapbox credentials internally.
- For viewer changes, run `python3 scripts/interactions.py` after building.

For this agent environment only, prefix Go/Make commands with `mise exec go@1.27.1 --`.
Public documentation examples use plain `go` and `make` commands with version requirements stated in prose.

## Read when relevant

- Go changes: [code-style skill and project exceptions](docs/GO_STYLE.md).
- Workspace lifecycle, selection, playback and scene changes: [architecture](docs/ARCHITECTURE.md#shared-model-and-rendering).
- Verification commands and terminal requirements: [development notes](docs/DEVELOPMENT.md).
- Provider changes: [loading, caching and attribution policies](docs/MAPBOX.md).
- Theme changes: [palette format and API](docs/THEMES.md).
- CLI/MCP contract changes: [usage](docs/USAGE.md), [MCP notes](docs/MCP.md) and [tool schemas](shared/tools.json).
