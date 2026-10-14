# Install a Linux release

Release binaries support Linux x86-64 (`uname -m` reports `x86_64`). You don't
need Go, Make, Python or a Mapbox account to use the binary with local files.
The viewer needs an interactive terminal with ANSI/VT support.

Download the `.tar.gz` archive and matching `.tar.gz.sha256` file for your chosen
version from [GitHub Releases](https://github.com/adamcbrewer/mapbox-tui/releases).
In the directory containing both files, replace the example version as needed:

```sh
VERSION=v0.1.0
sha256sum --check "mapbox-tui-${VERSION}-linux-amd64.tar.gz.sha256"
mkdir "mapbox-tui-${VERSION}"
tar -xzf "mapbox-tui-${VERSION}-linux-amd64.tar.gz" -C "mapbox-tui-${VERSION}"
cd "mapbox-tui-${VERSION}"
install -Dm755 mapbox-tui "$HOME/.local/bin/mapbox-tui"
install -Dm644 LICENSE "$HOME/.local/share/licenses/mapbox-tui/LICENSE"
install -Dm644 THIRD_PARTY_NOTICES.txt "$HOME/.local/share/licenses/mapbox-tui/THIRD_PARTY_NOTICES.txt"
"$HOME/.local/bin/mapbox-tui" --version
```

Stop if checksum verification fails. Add `~/.local/bin` to your shell's `PATH`
if it isn't already there. Then open a local activity:

```sh
mapbox-tui /path/to/ride.gpx --no-provider
```

The [README](https://github.com/adamcbrewer/mapbox-tui#mapbox) explains optional
Mapbox setup. The Mapbox CLI is installed separately.

To upgrade, quit the app and repeat these steps for the new version. To return
to an earlier version, check its compatibility with your saved configuration
and repeat the steps with that version's archive. There is no automatic updater.

To uninstall, remove `~/.local/bin/mapbox-tui` and
`~/.local/share/licenses/mapbox-tui/`. Your imported files and saved settings are
not removed by these commands.
