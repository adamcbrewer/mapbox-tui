# Mapbox TUI

Keep workspace lifecycle and scene rules shared across viewer, CLI and MCP.
Plain CLI/MCP rendering stays ANSI-free; imported geometry remains usable offline.

## Checks

For code changes, run `make test` from the repository root. For viewer changes,
also run the interaction checks using the [terminal requirements](docs/DEVELOPMENT.md).

For this agent environment only, prefix Go/Make commands with `mise exec go@1.27.1 --`.
Public documentation examples use plain `go` and `make` commands with version requirements stated in prose.

## Pull requests

When creating a branch for an issue, include the issue number in its name,
for example `feat/10-local-favourites`. Include the issue number in the PR title
too, for example `feat(favourites): Adds locally saved places (#10)`.

Follow the [PR title guidelines](CONTRIBUTING.md#pull-request-titles) when opening
or updating a PR, for example `feat(journeys): Adds route planning`.
Keep the description aligned with the final changes and checks performed.
Resolve review threads once their feedback is addressed, relevant checks pass
and the fixes are pushed. Leave outstanding feedback unresolved.

## Read when relevant

- Go changes: [code-style skill and project exceptions](docs/GO_STYLE.md).
- Workspace lifecycle, selection, playback and scene changes: [architecture](docs/ARCHITECTURE.md#shared-model-and-rendering).
- Provider changes: [loading, caching and attribution policies](docs/MAPBOX.md).
- Theme changes: [palette format and API](docs/THEMES.md).
- CLI/MCP contract changes: [usage](docs/USAGE.md), [MCP notes](docs/MCP.md) and [tool schemas](shared/tools.json).
