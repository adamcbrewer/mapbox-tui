# Terminal themes

Themes are data-only TOML palettes. Built-in and custom files use the same decoder,
registry and rendering API. The viewer owns its selected theme; imports, playback,
source loading and geometry do not own presentation colours.

## Use and add a palette

`--theme` accepts a built-in ID or a local TOML path. The default is `catppuccin`.
Ctrl-T cycles the session's registry, even inside dialogs. Ctrl-P's **Cycle Theme**
applies the next palette directly.

For a custom palette, copy any file in [`go/themes/`](../go/themes/), change `id`
and `name`, then edit colours and pass the file to the viewer:

```sh
./bin/mapbox-tui fixtures/example.gpx --theme "/path/to/my-theme.toml"
```

The custom palette joins the three built-ins for that viewer session. It is read
once at startup. To bundle another theme, place its TOML file in `go/themes/` and
rebuild. The embed pattern discovers it automatically; no registry entry, renderer
change or widget implementation is needed. `make dev` watches bundled palettes.

## File format

Use a bundled palette as a complete template. Every field is required:

| Field | Purpose |
|---|---|
| `id` | Unique lowercase words/numbers separated by hyphens; used by `--theme` |
| `name` | Non-empty printable ASCII label in the picker |
| `background` | Main terminal canvas |
| `surface` | Dialog background |
| `selected` | Selected row and search input background |
| `text`, `muted` | Primary and secondary UI text |
| `accent`, `highlight` | Keys, headings, title/border gradients and input cursor |
| `border` | Map frame |
| `dimmed` | Map/UI text beneath an open dialog |
| `progress_empty` | Unfilled playback progress bar |
| `activities` | Exactly six colours, assigned to stable per-item slots |
| `[map]` | Colours for map roles listed below |

Every colour must be a quoted `#RRGGBB` value. `[map]` requires `none`, `water`,
`road`, `major_road`, `place`, `route`, `marker`, `cursor`, `active_route` and
`active_marker`. An item's activity slot overrides its route, marker and moving
position colour. A profile's vertical cursor uses the `cursor` role.

Traffic adds optional map colours `traffic_low`, `traffic_moderate`, `traffic_heavy`,
`traffic_severe` and `traffic_closed`. Built-ins define them; older custom palettes
receive defaults when these keys are absent. Invalid explicit traffic colours still
fail validation. Traffic roles control colour, while severity priority and `1`–`4`/
`X` glyphs stay in the shared renderer.

Unknown fields/roles, missing or invalid colours, incorrect activity palette sizes,
invalid metadata and duplicate IDs fail at startup with an error. Custom palettes
must have IDs distinct from built-ins.

## API and ownership

| Entry point | Responsibility |
|---|---|
| `decodeTheme(io.Reader)` in `go/themes.go` | Decode and validate one TOML file into `presentationTheme` |
| `loadThemes(choice)` in `go/themes.go` | Load embedded palettes, resolve an ID or append a custom file; return the registry and selected index |
| `presentationTheme.styles()` in `go/theme.go` | Derive UI styles from named colour roles |
| `presentationTheme.mapColor(role, slot)` | Resolve a map role or activity slot to the selected palette's colour |
| `viewer.applyTheme(index)` | Select a loaded palette, restyle existing widgets/progress and request a redraw |

`viewer.settings.Theme` holds an index into that viewer's loaded registry.
Theme switching updates widget styles in place, preserving dialog selection,
file-picker location, text input/focus, scroll offsets and playback position.

The workspace stores activity slots, not RGB colours. Map and profile cells retain
their roles/slots; colours are resolved when painting. The cached elevation grid
can therefore be recoloured without rebuilding geometry. Drawing priority and
ASCII glyphs stay in `mapStyles`, independent of theme choice. CLI/MCP plain maps
continue to use the same geometry without ANSI styling.

## Palette sources

The three bundled palettes use colours from Omarchy's catalogue, mapped onto this
app's UI and map roles:

- [Catppuccin](https://github.com/basecamp/omarchy/blob/master/themes/catppuccin/colors.toml)
- [Catppuccin Latte](https://github.com/basecamp/omarchy/blob/master/themes/catppuccin-latte/colors.toml)
- [Tokyo Night](https://github.com/basecamp/omarchy/blob/master/themes/tokyo-night/colors.toml)

TOML parsing uses `github.com/pelletier/go-toml/v2`, pinned in `go.mod` with checksums
in `go.sum`, following the dependency-minimisation and deterministic-resolution
principles in the project's [supply-chain reference](https://github.com/lirantal/npm-security-best-practices).
