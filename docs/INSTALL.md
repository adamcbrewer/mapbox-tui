# Install a Linux release

Release binaries support Linux x86-64. You don't need Go or any build tools.

1. Download the Linux `.tar.gz` archive from [GitHub Releases](https://github.com/adamcbrewer/mapbox-tui/releases).
2. Extract it using your archive manager, or `tar -xzf mapbox-tui-v0.1.0-linux-amd64.tar.gz`
   (replace the example version with the one you downloaded).
3. Put the extracted `mapbox-tui` binary in a directory on your `PATH`.
   For example, from the extracted folder:

   ```sh
   mkdir -p ~/.local/bin
   install -m755 mapbox-tui ~/.local/bin/mapbox-tui
   ```

If `~/.local/bin` isn't already on your `PATH`, add it in your shell configuration.
You can also run the extracted binary directly with `./mapbox-tui`.
Keep the bundled `LICENSE` and `THIRD_PARTY_NOTICES.txt` with your downloaded copy.

Run `mapbox-tui --version` to check the installation, then
`mapbox-tui /path/to/ride.gpx --no-provider` to open a local activity.
The viewer needs an interactive terminal with ANSI/VT support.
See the [README](https://github.com/adamcbrewer/mapbox-tui#mapbox) for optional Mapbox setup.

To upgrade, quit the app and replace the installed binary with the new version.
To uninstall, delete that binary. Your imported files and saved settings remain.

## Verify the download

To check the archive before extracting it, download its matching `.sha256` file
into the same directory and run `sha256sum --check mapbox-tui-v0.1.0-linux-amd64.tar.gz.sha256`,
using your downloaded version. Continue only if it reports `OK`.
