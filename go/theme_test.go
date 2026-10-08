package main

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func themeViewer(t *testing.T, width, height int, ascii bool) *viewer {
	t.Helper()
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	m := &viewer{
		provider:    newProvider("none"),
		ctx:         context.Background(),
		width:       width,
		height:      height,
		showProfile: true,
		args:        Args{ASCII: ascii, NoAnimation: true},
	}
	if err := m.initUI(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
		t.Fatal(err)
	}
	m.layout()
	m.camera = m.workspace.fitVisible(m.camera.Width, m.camera.Height, 2)
	return m
}

func TestThemeRegistryLoadsBuiltinsAndCustomPalette(t *testing.T) {
	for _, id := range []string{"catppuccin", "catppuccin-latte", "tokyo-night"} {
		themes, selected, err := loadThemes(id)
		builtInSelectionWrong := err != nil || len(themes) != 3 || themes[selected].ID != id
		if builtInSelectionWrong {
			t.Fatal("built-in selection failed", id, err)
		}
	}
	data, err := themeFiles.ReadFile("themes/catppuccin.toml")
	if err != nil {
		t.Fatal(err)
	}
	custom := strings.ReplaceAll(string(data), "catppuccin", "custom")
	custom = strings.ReplaceAll(custom, "Catppuccin", "Custom palette")
	path := importFile(t, "custom palette.toml", []byte(custom))
	m := &viewer{args: Args{Theme: path}}
	err = m.initUI()
	customThemeMissing := err != nil || len(m.themes) != 4 || m.theme().ID != "custom"
	if customThemeMissing {
		t.Fatal("custom theme did not join the same registry", err)
	}
	m.openDialog(dialogThemes)
	_, content, _ := m.dialogContent(50, 10)
	if !strings.Contains(ansi.Strip(content), "Custom palette (current)") {
		t.Fatal("custom palette was absent from the picker")
	}
	for _, choice := range []string{"unknown-theme", path + ".missing.toml", importFile(t, "duplicate.toml", data)} {
		if _, _, err := loadThemes(choice); err == nil {
			t.Fatal("accepted unknown, missing or conflicting custom theme", choice)
		}
	}
}

func TestThemeDecoderRejectsIncompleteAndInvalidPalettes(t *testing.T) {
	data, err := themeFiles.ReadFile("themes/catppuccin.toml")
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	for name, data := range map[string]string{
		"syntax": "[map",
		"unknown field": strings.Replace(
			valid,
			"background =",
			"backgrounnd =",
			1,
		),
		"missing UI colour": strings.Replace(
			valid,
			"background = \"#1e1e2e\"\n",
			"",
			1,
		),
		"missing map role": strings.Replace(
			valid,
			"water = \"#89b4fa\"\n",
			"",
			1,
		),
		"unknown map role": valid + "traffic = \"#123456\"\n",
		"invalid colour": strings.Replace(
			valid,
			"#1e1e2e",
			"#gggggg",
			1,
		),
		"too few activity colours": strings.Replace(
			valid,
			", \"#f38ba8\"",
			"",
			1,
		),
		"too many activity colours": strings.Replace(
			valid,
			", \"#f38ba8\"]",
			", \"#f38ba8\", \"#123456\"]",
			1,
		),
		"invalid id": strings.Replace(
			valid,
			"id = \"catppuccin\"",
			"id = \"Bad ID\"",
			1,
		),
		"blank name": strings.Replace(
			valid,
			"name = \"Catppuccin\"",
			"name = \" \"",
			1,
		),
		"multiline name": strings.Replace(
			valid,
			"name = \"Catppuccin\"",
			"name = \"Line\\nBreak\"",
			1,
		),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeTheme(strings.NewReader(data)); err == nil {
				t.Fatal("accepted an invalid custom palette")
			}
		})
	}
}

func TestThemeCyclePreservesPlaybackImportsProfileAndSourceFreshness(t *testing.T) {
	m := themeViewer(
		t,
		120,
		40,
		false,
	)
	now := time.Now()
	m.configureSources()
	var requests int
	m.sources.set("test", sourceFunc(func(context.Context, Camera) (sourceSnapshot, error) {
		requests++
		return sourceSnapshot{Features: benchmarkFeatures(m.activeTrack()), RefreshAt: now.Add(time.Hour)}, nil
	}), true)
	t.Cleanup(m.sources.close)
	runLoadCommand(m, m.fetchAt(now))
	m.activePlayback().toggle()
	m.activePlayback().advance(m.activeTrack(), 30)
	m.openDialog(dialogSpeed)
	m.ui.selected = 2
	m.rebuild()
	selection, profile, progress := m.workspace.selection, m.visibleProfile(), m.progress
	items, camera, position := m.workspace.items(), m.camera, *m.position()
	base := slices.Clone(profile.Grid.Cells)
	sources := m.sources.view()
	text := ansi.Strip(m.content)
	initial := m.settings.Theme
	for i := 1; i <= len(m.themes); i++ {
		_, cmd := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
		cycleNotApplied := cmd != nil || m.settings.Theme != (initial+i)%len(m.themes) || !m.dirty
		if cycleNotApplied {
			t.Fatal("cycling did not update presentation without starting work")
		}
		runLoadCommand(m, m.fetchAt(now.Add(time.Duration(i)*time.Millisecond)))
		m.rebuild()
		workspaceChanged := requests != 1 ||
			!reflect.DeepEqual(sources, m.sources.view()) ||
			!reflect.DeepEqual(items, m.workspace.items()) ||
			m.workspace.selection != selection ||
			m.camera != camera ||
			!reflect.DeepEqual(*m.position(), position) ||
			!selection.playback.Playing
		if workspaceChanged {
			t.Fatal("theme change refetched data, reimported files, moved the camera or reset playback")
		}
		cachedPresentationChanged := m.visibleProfile() != profile ||
			!slices.Equal(profile.Grid.Cells, base) ||
			m.progress != progress ||
			m.ui.dialog != dialogSpeed ||
			m.ui.selected != 2 ||
			ansi.Strip(m.content) != text
		if cachedPresentationChanged {
			t.Fatal("theme change rebuilt cached geometry, recreated controls or changed dialog content")
		}
		paletteNotApplied := m.progress.FullColor != lipgloss.Color(m.activeColor()) ||
			m.progress.EmptyColor != lipgloss.Color(m.theme().ProgressEmpty) ||
			m.View().BackgroundColor != lipgloss.Color(m.theme().Background)
		if paletteNotApplied {
			t.Fatal("existing progress or viewer background kept the old palette")
		}
	}
}

func TestThemeCycleRestylesOpenWidgetsWithoutResettingInputOrScrolling(t *testing.T) {
	m := themeViewer(
		t,
		120,
		40,
		false,
	)
	m.openDialog(dialogFiles)
	m.ui.picker.CurrentDirectory = "../fixtures"
	m.openDialog(dialogCommands)
	m.ui.search.SetValue("theme")
	m.ui.search.SetCursor(2)
	m.ui.page.SetHeight(2)
	m.ui.page.SetContent("one\ntwo\nthree\nfour")
	m.ui.page.SetYOffset(1)
	m.ui.details.SetHeight(2)
	m.ui.details.SetContent("one\ntwo\nthree\nfour")
	m.ui.details.SetYOffset(1)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	widgetStateReset := cmd != nil ||
		m.ui.dialog != dialogCommands ||
		m.ui.search.Value() != "theme" ||
		m.ui.search.Position() != 2 ||
		!m.ui.search.Focused() ||
		m.ui.picker.CurrentDirectory != "../fixtures" ||
		m.ui.page.YOffset() != 1 ||
		m.ui.details.YOffset() != 1
	if widgetStateReset {
		t.Fatal("theme cycling reset an existing widget")
	}
	theme := m.theme()
	for name, style := range map[string]lipgloss.Style{
		"help":          m.ui.help.Styles.ShortKey,
		"spinner":       m.ui.spinner.Style,
		"file picker":   m.ui.picker.Styles.Directory,
		"search prompt": m.ui.search.Styles().Focused.Prompt,
	} {
		if style.GetForeground() != lipgloss.Color(theme.Accent) {
			t.Fatal("widget retained an old theme", name)
		}
	}
	widgetPaletteNotApplied := m.ui.picker.Styles.File.GetBackground() != lipgloss.Color(theme.Surface) ||
		m.ui.search.Styles().Focused.Text.GetBackground() != lipgloss.Color(theme.Selected) ||
		m.ui.search.Styles().Cursor.Color != lipgloss.Color(theme.Highlight)
	if widgetPaletteNotApplied {
		t.Fatal("file picker or search background/cursor retained an old theme")
	}
}

func TestThemePickerAndPaintingAcrossLayoutsAndASCIIMode(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf(
				"%dx%d/ascii=%t",
				size[0],
				size[1],
				ascii,
			), func(t *testing.T) {
				m := themeViewer(
					t,
					size[0],
					size[1],
					ascii,
				)
				m.runCommand("theme")
				if m.ui.dialog != dialogThemes || m.ui.selected != m.settings.Theme {
					t.Fatal("theme command did not open the current palette")
				}
				for i := range m.themes {
					m.applyTheme(i)
					m.rebuild()
					text := ansi.Strip(m.content)
					layoutInvalid := !strings.Contains(text, m.themes[i].Name) ||
						lipgloss.Width(m.content) > size[0] ||
						lipgloss.Height(m.content) != size[1]
					if layoutInvalid {
						t.Fatal("theme selection was hidden or exceeded the viewport", text)
					}
					g := newGrid(2, 1)
					g.label(
						XY{},
						"~",
						roleWater,
						false,
					)
					g.label(
						XY{2, 0},
						"@",
						roleCursor,
						false,
						m.activeSwatch(),
					)
					line := m.gridRow(g, 0, ascii)
					paintingIncomplete := !strings.Contains(line, m.paint("~", roleWater)) ||
						!strings.Contains(line, m.paint("@", roleCursor, m.activeSwatch())) ||
						ansi.Strip(line) != g.plain(ascii)
					if paintingIncomplete {
						t.Fatal("painting lost basemap roles, activity colour or plain geometry")
					}
				}
				m.ui.selected = 1
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if m.settings.Theme != 1 || m.ui.dialog != dialogNone {
					t.Fatal("picker did not apply the selected palette")
				}
			})
		}
	}
}
