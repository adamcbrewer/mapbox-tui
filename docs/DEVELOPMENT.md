# Development

Run commands from the repository root with Go meeting the minimum in
[go.mod](../go.mod), Make and Python 3.

## Local workflow

```sh
make dev
make check
make interactions
```

`make dev` rebuilds and restarts the viewer on changes. It needs an interactive
Unix-like terminal; viewer state resets on restart.

`make check` runs formatting, build, tests, vet, CLI/MCP parity and race checks
offline. Run `make interactions` after viewer changes; it needs tmux,
`rsvg-convert` and DejaVu fonts. Captures go in ignored `reports/`.

The [Makefile](../Makefile) defines individual checks, watcher arguments,
benchmarks, installation and packaging options. The [CI workflow](../.github/workflows/ci.yml)
defines the automated checks and their dependencies. See
[architecture](ARCHITECTURE.md#run-benchmarks-and-retain-results) for benchmark
methods and measurement limits.

## Optional Git hooks

Check your existing `core.hooksPath` before enabling the [bundled hooks](../.githooks/):

```sh
git config --get core.hooksPath
git config --local core.hooksPath .githooks
```

If you already have hooks, add the bundled commands to them. Restore the previous
`core.hooksPath` value or unset it to disable this setup.

Before merging, pass all three CI checks and resolve review conversations.

## Live Mapbox checks

See [Mapbox account checks](MAPBOX.md#live-account-checks) for opt-in checks using
your saved login or token.
