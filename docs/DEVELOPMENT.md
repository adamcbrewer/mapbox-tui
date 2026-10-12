# Development

Run commands from the repository root with Go meeting the minimum in
[go.mod](../go.mod), Make and Python 3. A version manager is optional; activate
Go once so `go` and `gofmt` are on `PATH`.

## Checks and the watcher

```sh
make dev
make dev ARGS='fixtures/example.gpx --zoom 13'
make test
make check
make interactions
```

The watcher rebuilds and restarts the viewer when Go files, build inputs or
bundled palettes change. It needs an interactive Unix-like terminal; viewer
state resets on restart.

`make test` builds, tests, vets and checks CLI/MCP parity. `make check` adds
formatting and the race detector. The targets and offline scripts disable
Mapbox credentials themselves.

`make interactions` needs tmux, `rsvg-convert` and DejaVu fonts. Run it after
viewer changes. It exercises headless Linux pseudo-terminals, including terminal
restoration, rather than physical mouse forwarding. Captures go in ignored `reports/`.

CI uses `make format-check test`, `make race` and `make interactions`, with
Go selected from `go.mod`. See [the workflow](../.github/workflows/ci.yml).

## Benchmarks and packaging

```sh
make fixtures
make bench
make install
make package
```

Benchmarks generate synthetic dense fixtures and reports under `reports/`.
See [architecture](ARCHITECTURE.md#run-benchmarks-and-retain-results) for the
method and measurement limits.

Installation and packaging support Linux x86-64. `PREFIX` changes the default
`~/.local` install location; `DESTDIR` supports staged installs. Packages go in
`dist/` and include the binary, `LICENSE` and `THIRD_PARTY_NOTICES.txt`.

## Git hooks and pull requests

The bundled hooks run `make pre-commit` and `make check`. Check your existing
`core.hooksPath` before enabling them:

```sh
git config --get core.hooksPath
git config --local core.hooksPath .githooks
```

Pre-commit checks staged whitespace and Go formatting without rewriting files.
Pre-push runs `make check`; terminal interactions stay separate. If you already
have hooks, add these commands to them. Unset `core.hooksPath` or restore its
previous value to disable this setup.

`main` requires a pull request, all three CI checks from GitHub Actions and
resolved review conversations. Branches needn't be up to date. A second review
is useful but isn't mandatory. Direct pushes, force pushes and deletion are
blocked, including for maintainers and admins.

## Optional live provider checks

These use your saved login or token and make account requests. After building:

```sh
make mapbox-check
python3 scripts/mapbox_auth_check.py
python3 scripts/mapbox_check.py
python3 scripts/interactions.py --mapbox
MAPBOX_TUI_DIRECTIONS_CHECK=1 go test -tags=integration ./go -run '^TestDirectionsAccountAccess$' -v
MAPBOX_TUI_TRAFFIC_CHECK=1 go test -tags=integration ./go -run '^TestTrafficAccountAccess$' -v
```

`make mapbox-check` only checks installed CLI schemas, without account requests.
The authentication script checks the saved CLI login and ignores
`MAPBOX_ACCESS_TOKEN`. Directions checks make three requests, one per profile;
traffic normally requests TileJSON and one tile.

Record the revision, tool versions, date, account type and token-free results.
Existing-account checks don't establish card-free trial access.
