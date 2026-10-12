# Contributing

Use the Go version in [go.mod](go.mod), Make and Python 3. Run `make build`
from the repository root. See [development and checks](README.md#development-and-checks)
and [Go style](docs/GO_STYLE.md).

Keep PRs small and focused. Link the related issue, explain what changed and why,
and list the checks you ran. Run `MAPBOX_ACCESS_TOKEN='' make test` for offline
checks. After viewer changes, also run `make interactions`; include a terminal
capture when it helps explain a visible change.

Use [GitHub issues](https://github.com/adamcbrewer/mapbox-tui/issues) to report bugs
or propose features. Include reproduction steps for bugs and a clear use case
for features. Use synthetic or minimised sample files, and remove credentials
and personal location data from files, logs and captures.

Contributions are submitted under the [MIT licence](LICENSE).
Contributors retain their copyright.
