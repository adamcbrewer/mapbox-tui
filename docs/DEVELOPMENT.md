# Development

Run commands from the repository root with Go meeting the minimum in
[go.mod](../go.mod), Make and Python 3.11 or newer.

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

## GPX and journey preview

Build the normal binary, then seed an interactive preview:

```sh
make build
python3 scripts/preview.py
python3 scripts/preview.py --file fixtures/country-ride.gpx \
  --start '51.309174,-2.583389' --end '51.297454,-2.532641'
```

The defaults load the country ride and a nearby journey. Start/end arguments use
latitude,longitude. The script launches the normal viewer with native journey options through tmux and preserves
your configured Mapbox account, provider and theme. Route calculations use your
account normally. It does not clear tokens or substitute offline providers.

Use `--detach` to leave the prepared viewer running and print its attach command.
Use `--capture reports/generated/preview` to save PNG/SVG captures of Journey,
Files and Context, then exit. Captures need `rsvg-convert`; interactive previews
need tmux and a terminal. `--width` and `--height` default to 120×40, with a minimum
of 70×18 while seeding the controls. You can resize the interactive viewer after
startup. These are developer previews; normal offline regression checks remain
separate.

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

The Journey planner’s **Copy journey to clipboard** action generates a command
for the globally installed `mapbox-tui`, with `--start`, `--end`, repeated
`--waypoint` and `--file` options, and `--mode`. It needs neither Python nor a
repository checkout. The preview script remains a development capture/launch
helper and delegates journey setup to these same binary options.
