# Terminal themes

Themes are data-only TOML palettes. Built-in and custom files use the same decoder,
registry and rendering API. The viewer owns its selected theme; imports, playback,
source loading and geometry do not own presentation colours.

## Use and add a palette

`--theme` accepts a built-in ID or local TOML path. We use the saved theme, or
Catppuccin if none is saved. The flag overrides that choice for one launch.

Open **Colour theme** in Ctrl-K. Arrows or `j`/`k` preview a palette; Enter saves
it and returns to the map. Esc returns to the command menu and restores the
previous theme. Closing or replacing the picker also cancels the preview.
Dialogs don't dim the map, so you can judge the full colours. The context bar
shows the current theme, including previews.

Built-ins are `catppuccin`, `noir`, `nord`, `paper`,
`synthwave` and `tokyo-night`.
Catppuccin uses mauve for its first activity, with blue UI accents.
Synthwave pairs a deep plum canvas with candy pink, pearl-pink text, cyan and
lilac, with neon gradient frames and a lilac first activity:

```sh
./bin/mapbox-tui fixtures/country-ride.gpx --theme synthwave
```

Nord uses the [Nord palette](https://www.nordtheme.com/docs/colors-and-palettes/):
slate backgrounds, snow-coloured text and a snow-white first activity, with icy blue UI accents.
Its activity colours use brighter tints to keep labels readable on dialog surfaces.
Tokyo Night uses purple for its first activity, with blue UI accents.
Paper replaces the Latte and Strava palettes with one light theme: a cool off-white
canvas inspired by e-ink, pale grey roads, soft grey water, charcoal routes and elevation
profiles, and green UI accents. Its six activity colours have at least 7:1 contrast against the canvas,
so small Braille dots remain visible on large maps. Nord and Paper use solid borders.
Noir uses black land, grey roads and oceans, brighter grey place names, and
cyan activities and elevation profiles, with green UI accents.

Activity palettes use blue, cyan, purple, pink and neutral shades, leaving green,
yellow and red for traffic. The six slots remain stable as themes change.

```sh
./bin/mapbox-tui fixtures/country-ride.gpx --theme nord
./bin/mapbox-tui fixtures/country-ride.gpx --theme paper
./bin/mapbox-tui fixtures/country-ride.gpx --theme noir
```

Confirmed choices survive restarts. We save built-ins by ID and custom palettes
by absolute path, so changing directories won't break them. Unavailable saved
palettes warn and fall back to Catppuccin. See [saved preferences](USAGE.md#saved-viewer-preferences)
for storage and reset. Rebuild and restart to see changes to bundled palettes.

For a custom palette, copy any file in [`go/themes/`](../go/themes/), change `id`
and `name`, then edit colours and pass the file to the viewer:

```sh
./bin/mapbox-tui fixtures/example.gpx --theme "/path/to/my-theme.toml"
```

Swap in `fixtures/country-ride.gpx` to preview the palette on a longer recorded
ride with elevation data.

Custom palettes join the built-ins for that session. We read them once at startup,
with a 64 KiB limit. To bundle one, put its TOML file in `go/themes/` and rebuild.
The embed pattern picks it up; `make dev` watches that directory.

## Remote access

List and set palettes in a running viewer:

```sh
mapbox-tui remote --session main theme list --json
mapbox-tui remote --session main theme set --theme nord
```

MCP uses `viewer_theme_list` and `viewer_theme_set`. These commands change the
session theme without saving a preference. See [remote access](REMOTE_ACCESS.md).

## File format

Use a bundled palette as a complete template. These fields are required:

| Field | Purpose |
|---|---|
| `id` | Unique lowercase words/numbers separated by hyphens; used by `--theme` |
| `name` | Non-empty printable ASCII label in the picker |
| `background` | Main terminal canvas |
| `surface` | Dialog background |
| `selected` | Selected row and search input background |
| `text`, `muted` | Primary and secondary UI text |
| `accent`, `highlight` | Keys, headings, active border titles and input cursor |
| `border` | Map, modal and context drawer frames |
| `dimmed` | Disabled command text |
| `progress_empty` | Unfilled playback progress bar |
| `activities` | Exactly six colours, assigned to stable per-item slots |
| `[map]` | Colours for map roles listed below |

Every colour must be a quoted `#RRGGBB` value. `[map]` requires `none`, `water`,
`road`, `major_road`, `place`, `route`, `marker`, `cursor`, `active_route` and
`active_marker`. An item's activity slot overrides its route, marker and moving
position colour. A profile's vertical cursor uses the `cursor` role.

`activity_text` is an optional array of exactly six `#RRGGBB` colours for activity
names in the Files panel and legend. It defaults to `activities`. Light themes
can use vivid map/profile strokes and deeper text shades of the same hues.
Selected Files rows still use the selection foreground.

Active routes, markers and the moving cursor use bold text weight in the viewer;
other activities keep their assigned colour at normal weight. This changes
presentation only, including the elevation profile. Geometry and plain CLI/MCP
output keep their existing glyphs.

Built-in activity labels keep at least 4.5:1 text contrast against the canvas
and dialog surface. Map strokes must stand apart from both road roles by
luminance contrast or colour distance in CIE Lab. These are palette regression
checks; live basemap previews also check how the terminal strokes read together.

Traffic adds optional map colours `traffic_low`, `traffic_moderate`, `traffic_heavy`,
`traffic_severe` and `traffic_closed`. Built-ins use green for low, yellow for
moderate and red for heavy, severe and closed traffic; Paper uses darker shades
for its light canvas. Closed roads also retain their `X` glyph.
Older custom palettes receive defaults when these keys are absent. Invalid explicit traffic colours still
fail validation. Traffic roles control colour, while severity priority and `1`–`4`/
`X` glyphs stay in the shared renderer.

`border_gradient` is an optional array of at least two `#RRGGBB` stops. It blends
around map, modal and context/files/journey panel borders. Repeat the first stop
at the end for a smooth loop. Themes without it retain solid `border` colours.
With gradient borders, modal titles also blend from `accent` to `highlight`.
Gradient effects change colours only; ASCII mode retains its plain box glyphs.

Unknown fields or roles, invalid colours or metadata, and wrong activity palette
sizes fail validation. Custom IDs must differ from built-ins. An invalid explicit
`--theme` stops startup; an invalid saved palette warns and uses Catppuccin.

## API and ownership

| Entry point | Responsibility |
|---|---|
| `decodeTheme(io.Reader)` in `go/themes.go` | Decode and validate one TOML file into `presentationTheme` |
| `loadThemes(choice)` in `go/themes.go` | Load embedded palettes, resolve an ID or append a custom file; return the registry and selected index |
| `presentationTheme.styles()` in `go/theme.go` | Derive UI styles from named colour roles |
| `presentationTheme.mapColor(role, slot)` | Resolve a map role or activity slot to the selected palette's colour |
| `viewer.applyTheme(index)` | Select a loaded palette, restyle existing widgets/progress and request a redraw |

`viewer.settings.Theme` holds an index into that viewer's loaded registry.
Modal titles use `accent` (or the title blend above); breadcrumb ancestors use
`muted`, with `border` for the frame and `surface` for the background. The context
drawer uses the same surface and text roles. Unicode boxes have subtle rounded corners; ASCII mode
uses plain corners and breadcrumb separators. The terminal controls the font.

Theme switching updates widget styles in place, preserving dialog selection,
file-picker location, text input/focus, scroll offsets and playback position.

The workspace stores activity slots, not RGB colours. Map and profile cells retain
their roles/slots; colours are resolved when painting. The cached elevation grid
can therefore be recoloured without rebuilding geometry. Drawing priority and
ASCII glyphs stay in `mapStyles`, independent of theme choice. CLI/MCP plain maps
continue to use the same geometry without ANSI styling.
