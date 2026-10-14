# Theme polish: issue 22

Baseline: `main` on 14 October 2026. Related work: [issue 22](https://github.com/adamcbrewer/mapbox-tui/issues/22).
The existing picker already previews palettes and restores the previous theme on
Escape, including its parent command filter.

## Reproduction and findings

At baseline, run `make build`, then open `fixtures/example.gpx` with `--no-provider` and
`--theme catppuccin-latte`. Press `e` to show the activity legend and elevation
profiles. Open Ctrl-K, type `theme`, press Enter and preview each palette. Use
Ctrl-L to inspect activity rows. Repeat at 120×40 and 70×24, then press `a` for
ASCII mode. The terminal interaction suite exercises these same sizes and states.

Baseline captures are in ignored `reports/theme-baseline/`, including
`mapbox-tui-theme-catppuccin-latte-120x40.png` and
`mapbox-tui-theme-preview-70x24.png`. Run colour captures with `NO_COLOR` unset;
this agent environment sets it globally and otherwise hides the palette. Set
`TERM=xterm-256color` and `COLORTERM=truecolor` when launching the capture runner
so tmux retains RGB colours.

- **Catppuccin Latte:** yellow and pink activities fade into the light canvas,
  affecting route/profile strokes and legend labels. Their palette contrast
  against the canvas is 2.31:1 and 2.34:1. Activity labels also use those colours
  on the darker dialog surface. Deepen the six activity hues while preserving
  their stable colour slots; use the same hues for matching route/traffic roles.
- **Catppuccin Latte:** selected text is 3.52:1 against the selected-row fill;
  blue modal headings are 3.71:1 against the surface. Deepen highlight/accent.
  Files rows additionally override selected text with an activity colour; keep
  the selection foreground on the highlighted row so it remains readable.
- **Catppuccin Latte:** elevation axis labels use the low-contrast road colour
  (1.91:1 against the canvas). Paint axis text with the primary text role;
  retain subdued roads in the basemap.
- **Tokyo Night:** `muted` is brighter than `text`, reversing the intended
  hierarchy in modal descriptions and footer hints. Brighten primary text and
  use the former primary colour for secondary text.
- **Catppuccin:** primary/secondary modal text, selected rows and activity
  colours have clear contrast. Keep these UI and activity colours.

Contrast ratios describe the RGB palette before terminal colour reduction. The
target for ordinary text, selected text and activity labels is at least 4.5:1
against their intended surfaces; map roads remain deliberately subordinate.
Geometry, glyphs and explicit selection markers carry meaning alongside colour.

## Synthwave direction

The requested new palette uses a deep plum canvas, candy-pink accents, pearl-pink
text, electric cyan and lilac. Neon gradient frames and pink title gradients give
the existing rounded boxes their glossy character. All gradient stops belong to
the TOML palette; themes without them retain solid borders. Imported activities
retain six distinct colours and work offline.

## Nord and Strava additions

Nord adds slate backgrounds, snow-coloured text and icy blue accents from the
[Nord palette](https://www.nordtheme.com/docs/colors-and-palettes/), with brighter
orange, purple and blue activity tints for panel contrast. Strava is a new light
palette based on the supplied map screenshot: pale grey-green canvas, white
panels, grey roads, blue water and orange routes. Orange is deepened slightly for
readable activity labels. Both use solid borders and the existing map roles.

The repository binary is rebuilt with the palettes. Theme choices remain
session-only; a plain launch starts in Catppuccin. Use `--theme nord`,
`--theme strava` or Ctrl-K's Colour theme, and restart an existing process after
rebuilding to load the new embedded files.

## Route visibility follow-up

The supplied full-size country-ride screenshots exposed a gap in the initial
checks: a readable activity colour against the canvas can still blend into road
strokes. Nord's first activity was only 1.35:1 against major roads, and
Synthwave's first activity was 2.25:1. Catppuccin's first yellow activity was
3.84:1. All routes also had normal text weight, even when active.

Screenshot pixel inspection found the intended route foregrounds within one RGB
level, including Nord's cyan and Synthwave's pink. The local Omarchy terminal
palette does not replace the application's explicit RGB foregrounds. Window
effects may slightly alter the captured appearance, but the weak route/basemap
separation is reproduced in the application's own captures.

The first visibility fix made road colours quieter across all six themes. Every activity slot had at
least 3:1 luminance contrast against both road roles. Nord's first activity uses
its warm yellow and Synthwave uses neon pink. Active routes and markers use bold
weight, alongside the existing bold moving cursor, while retaining their colour
slots and geometry. The SVG capture helper now preserves ANSI bold styling so
previews represent that emphasis.

In that iteration, first-activity/major-road contrast was 4.72:1 for Nord and 3.48:1
for Synthwave. The lowest activity/road contrast across every built-in slot was
3.01:1. Rebuilt Go/parity/formatting checks and the full terminal suite passed
again, including both terminal sizes and ASCII mode. Live Mapbox previews were
regenerated; previous map captures remain in ignored `reports/theme-route-before/`
for comparison. No Omarchy configuration was changed.

## Brighter routes and Noir

The next requested palette pass strengthens road contrast in Latte and Strava,
uses vivid `#ff5a00` orange for Strava's first activity and profile, and switches
Synthwave's first activity/profile to electric yellow-green `#dfff00`. Latte's
first activity is a brighter copper-orange. Noir adds black land, grey roads and
oceans, brighter grey place names, and neon green `#39ff14` activities/profiles.

The optional `activity_text` palette separates readable activity names from vivid
map/profile strokes. Latte and Strava retain deeper text shades; existing custom
palettes default to their activity colours. Decoder and rendered-legend tests
cover both paths. Map/road separation accepts luminance contrast or colour
distance in CIE Lab so vivid orange can stand apart from grey without being
forced to a dark brown. This is a palette heuristic, alongside visual review.

That iteration's rebuilt binary included all seven themes. Go/parity/formatting checks and the
full terminal suite passed, including all seven palettes at both sizes and ASCII
mode. Live Mapbox captures were refreshed; the receipt now lists 48 PNG/SVG pairs.
Earlier Latte, Strava and Synthwave map captures are retained in ignored
`reports/theme-colour-before/` for comparison.

## Paper replaces the light palettes

The orange strokes in Latte and Strava remained difficult to see at large
terminal sizes. Both palettes are removed from the built-ins and replaced by
Paper. The initial version used warm off-white land, grey roads, muted blue-grey water and a dark
ink-blue `#243b53` first activity and elevation profile. The first activity had
10.37:1 contrast against the canvas and 4.69:1 against major roads; the previous
orange strokes had about 3:1 canvas contrast. All six Paper activities have at
least 7:1 canvas contrast and use the same dark shades for their text labels.

The current registry contains Catppuccin, Noir, Nord, Paper, Synthwave and Tokyo
Night. CLI help and theme-switching checks use that set. Preview cancellation,
wrapping and colour captures derive their expected palettes from the bundled
files, including when Catppuccin becomes the first entry after removing Latte.

## Paper e-ink refinement

The supplied map screenshot still showed too much competition between roads and
the activity. The Greptile reference suggests cooler grey paper and dark ink,
with green accents. Paper now uses a matte `#ecece8` canvas and `#f3f3ef` panel
surface, lighter `#d4d6cf` roads and `#c6c8c1` major roads, subdued `#a8b8b5` grey water, and
near-black charcoal `#20212b` for the first activity and elevation profile.
Place names stay darker than roads. Green headings and selection accents carry
the reference into the UI while keeping text readable.

The first activity has 13.49:1 canvas contrast and 9.46:1 against major roads.
All six activity colours remain above 7:1 against the canvas and use the same
colours for their labels. Previous Paper captures are retained in ignored
`reports/theme-paper-before-eink/` for comparison.

## Verification

Before palette edits, theme interactions passed at 120×40 and 70×24 with all
three built-ins, live preview, confirmation/cancellation and ASCII playback.

After implementation, `make test format-check` passed: build, Go tests, vet,
CLI/MCP parity and formatting. The tests require at least 4.5:1 RGB contrast for
ordinary UI text, selected text and `activity_text` labels on their intended surfaces,
and reject incomplete or invalid gradient definitions. Existing custom palettes
still load without gradient stops.

The full `python3 scripts/interactions.py` suite passed with all six themes at
120×40 and 70×24, including live preview, confirmation/cancellation and ASCII
playback. Navigation, imports, profiles, workspace controls, journey editing and
quick search also passed across the suite's compact terminal sizes. Synthwave
country ride, offline journey, Context and Files captures are retained in ignored
`reports/generated/mapbox-tui-theme-synthwave-*.png` and matching SVG files.

The implementation checks used offline sources. Paper previews use the normal
saved Mapbox login and `fixtures/country-ride.gpx`, with the default provider and
routed driving journey. The ride is fitted again after each terminal resize.
Streets access was ready; the initial tile statistics were 2 requests, 0 cache
hits and 0 errors. The live basemap, theme picker and ASCII map were captured at
200×60, 120×40 and 70×24. The nine PNG/SVG pairs and receipt are in ignored
`reports/generated/theme-paper/`. Previous palette captures remain in
`reports/generated/theme-mapbox/`.

Traffic was not visually audited; traffic palette roles pass the same colour
decoder as the other map roles.
