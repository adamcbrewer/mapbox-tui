package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestContextDrawerKeepsCameraAndCapturesCoveredMouseEvents(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {30, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := themeViewer(t, size[0], size[1], false)
			m.settings.Elevation = elevationHide
			m.layout()
			m.rebuild()
			before, selection := m.camera, m.workspace.selection
			closed := m.content
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			m.rebuild()
			if !m.ui.detailsOpen || m.camera != before || m.workspace.selection != selection {
				t.Fatal("opening context moved the camera or changed selection")
			}
			for _, hint := range []string{"ctrl+b hide", "/ search", "? help"} {
				if !strings.Contains(ansi.Strip(m.content), hint) {
					t.Fatal("drawer obscured persistent hints", hint)
				}
			}
			y, x := m.contextDrawerY()+2, m.journeyPanelX()+2
			m.ui.details.SetContent(strings.Repeat("detail\n", 100))
			m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
			if m.camera != before || m.inspected != nil || !m.ui.detailsOpen {
				t.Fatal("mouse over context reached the covered map")
			}
			if m.ui.details.YOffset() == 0 {
				t.Fatal("wheel did not scroll context")
			}
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			m.rebuild()
			if m.ui.detailsOpen || m.camera != before || m.content != closed {
				t.Fatal("closing context did not restore the same map")
			}
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.ui.detailsOpen || m.camera != before {
				t.Fatal("escape did not close context without moving the camera")
			}
		})
	}
}

func TestContextSurfacesInheritEveryThemeAndFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/ascii=%v", size[0], size[1], ascii), func(t *testing.T) {
				m := themeViewer(t, size[0], size[1], ascii)
				m.settings.Elevation = elevationHide
				m.layout()
				m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
				for i := range m.themes {
					m.applyTheme(i)
					m.rebuild()
					if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) != size[1] {
						t.Fatal("context overflowed terminal", m.theme().ID)
					}
					if m.ui.details.Style.GetBackground() != lipgloss.Color(m.theme().Surface) {
						t.Fatal("context viewport retained another theme's background")
					}
					footer := ansi.Strip(strings.Join(m.contextFooter(), "\n"))
					if !strings.Contains(footer, m.theme().Name) {
						t.Fatal("context footer lost the current theme", footer)
					}
					m.openDialog(dialogCommands)
					m.pushDialog(dialogThemes)
					m.rebuild()
					text := ansi.Strip(m.content)
					separator := " › "
					if ascii {
						separator = " > "
						if strings.ContainsAny(text, "╭╮╰╯") {
							t.Fatal("ASCII mode retained rounded Unicode borders")
						}
					}
					if !strings.Contains(text, separator+"Colour theme") {
						t.Fatal("theme preview lost the active breadcrumb", text)
					}
					m.closeDialog()
				}
			})
		}
	}
}

func TestContextFooterFollowsSearchAndKeepsAttribution(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	m.openDialog(dialogSearch)
	m.applyPlaceLocation(placeLocation{Name: "Café Zürich 東京", Lon: 8.54, Lat: 47.37,
		Attribution: "© Mapbox © OpenStreetMap"})
	m.rebuild()
	text := ansi.Strip(m.content)
	for _, want := range []string{"Café Zürich 東京", "Lat 47.37000", "Lon 8.54000", "© Mapbox © OpenStreetMap"} {
		if !strings.Contains(text, want) {
			t.Fatal("selected place context or attribution missing", want, text)
		}
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.rebuild()
	if !strings.Contains(ansi.Strip(m.content), "Café Zürich 東京") {
		t.Fatal("drawer lost selected place name")
	}
}

func TestContextDrawerPersistsDuringMapNavigation(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := themeViewer(t, size[0], size[1], false)
			m.settings.NoAnimation = true
			m.settings.Elevation = elevationHide
			m.layout()
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			m.rebuild()
			assertOpen := func() {
				t.Helper()
				m.rebuild()
				if !m.ui.detailsOpen || !strings.Contains(ansi.Strip(m.content), "ctrl+b hide") {
					t.Fatal("map interaction closed the context drawer")
				}
			}
			for _, key := range []rune{tea.KeyRight, tea.KeyDown, 'h', 'k'} {
				before := m.camera
				m.Update(tea.KeyPressMsg{Code: key})
				if m.camera == before {
					t.Fatal("drawer captured map navigation", key)
				}
				assertOpen()
			}
			before := m.camera
			m.ui.details.SetContent(strings.Repeat("detail\n", 100))
			m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
			if m.camera != before || m.ui.details.YOffset() == 0 {
				t.Fatal("page down did not scroll only the drawer")
			}
			y := m.mapOriginY() + 1
			m.Update(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseMotionMsg{X: 15, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseReleaseMsg{X: 15, Y: y, Button: tea.MouseLeft})
			if m.camera == before || m.dragging {
				t.Fatal("map drag failed with context open")
			}
			assertOpen()
			zoom := m.camera.Zoom
			m.Update(tea.MouseWheelMsg{X: 10, Y: y, Button: tea.MouseWheelUp})
			if m.camera.Zoom == zoom {
				t.Fatal("wheel over map did not zoom")
			}
			assertOpen()
			m.Update(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseReleaseMsg{X: 10, Y: y, Button: tea.MouseLeft})
			assertOpen()
		})
	}
}

func TestSelectedPositionMarkerFollowsMapAndTheme(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		m := themeViewer(t, 120, 40, ascii)
		m.settings.NoAnimation = true
		m.settings.Elevation = elevationHide
		m.layout()
		m.openDialog(dialogSearch)
		m.applyPlaceLocation(placeLocation{Name: "Selected place", Lon: 8.54, Lat: 47.37})
		for theme := range m.themes {
			m.applyTheme(theme)
			for _, key := range []rune{tea.KeyRight, '+', '.'} {
				m.Update(tea.KeyPressMsg{Code: key})
				m.rebuild()
				point := m.camera.screen(project(m.inspected.X, m.inspected.Y))
				x, y := int(point.X/2), int(point.Y/4)+m.mapOriginY()
				rows := strings.Split(ansi.Strip(m.content), "\n")
				if []rune(rows[y])[x] != '*' {
					t.Fatalf("selected position marker missing: theme=%s ascii=%v key=%v", m.theme().ID, ascii, key)
				}
				if !strings.Contains(m.content, m.paint("*", roleCursor)) {
					t.Fatal("selected position marker did not inherit the theme cursor style")
				}
			}
		}
		y := m.mapOriginY() + 1
		m.Update(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 10, Y: y, Button: tea.MouseLeft})
		m.rebuild()
		rows := strings.Split(ansi.Strip(m.content), "\n")
		if m.inspected == nil || []rune(rows[y])[10] != '*' {
			t.Fatal("map click did not mark its inspected coordinates")
		}
		before := m.camera
		m.camera.pan(10000, 10000)
		m.rebuild()
		if strings.Contains(ansi.Strip(m.content), "*") {
			t.Fatal("offscreen inspection marker was not clipped")
		}
		m.camera = before
		m.inspected = nil
		m.rebuild()
		if strings.Contains(ansi.Strip(m.content), "*") {
			t.Fatal("cleared inspection left a marker behind")
		}
	}
}

func TestPlannerPreservesImportedContextAndDrawer(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	m.settings.Elevation = elevationHide
	m.layout()
	m.openDialog(dialogSearch)
	m.applyPlaceLocation(placeLocation{Name: "Country viewpoint", Lon: -2.58, Lat: 51.31})
	m.ui.detailsOpen = true
	m.rebuild()
	m.ui.details.GotoBottom()
	selection, position, offset := m.workspace.selection, m.inspected, m.ui.details.YOffset()
	playback := *m.activePlayback()
	m.activePlayback().Playing = true
	m.openJourney()
	if m.workspace.selection != selection || m.inspected != position || !m.activePlayback().Playing {
		t.Fatal("opening planner replaced the imported activity, place or playback")
	}
	m.activePlayback().Playing = playback.Playing
	enterJourneyCoordinate(m, 0, "51.31, -2.58")
	enterJourneyCoordinate(m, 1, "51.29, -2.53")
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	m.rebuild()
	if m.workspace.selection != selection || m.inspected != position || !reflect.DeepEqual(*m.activePlayback(), playback) {
		t.Fatal("journey edits replaced the imported context")
	}
	for _, want := range []string{"Country viewpoint", "Journey", "esc close"} {
		if !strings.Contains(ansi.Strip(m.content), want) {
			t.Fatal("shared context bar lost context", want)
		}
	}
	camera := m.camera
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.rebuild()
	if m.journeyPanelVisible() || m.camera != camera || !strings.Contains(ansi.Strip(m.content), "ctrl+b planner") {
		t.Fatal("collapsing planner lost its context or changed the camera")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m.rebuild()
	if m.ui.dialog != dialogNone || m.ui.detailsOpen || m.ui.details.YOffset() != offset {
		t.Fatal("Escape did not close the sidebar while preserving context scroll")
	}
	m.openJourney()
	m.showJourneyOnMap()
	if m.workspace.selection != selection || m.inspected != position {
		t.Fatal("showing a journey replaced the inspected file or place")
	}
}

func TestJourneyDetailsShowCoordinatesOnce(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	enterJourneyCoordinate(m, 0, "51.31, -2.58")
	enterJourneyCoordinate(m, 1, "51.29, -2.53")
	details := strings.Join(m.journeyDetails(), "\n")
	for _, coordinates := range []string{"51.310000, -2.580000", "51.290000, -2.530000"} {
		if strings.Count(details, coordinates) != 1 {
			t.Fatal("stop coordinates duplicated", details)
		}
	}
	_, body, _ := m.journeyDialogContent(60, 20)
	if strings.Contains(body, "feedback / usage") {
		t.Fatal("static routing documentation remained a planner action")
	}
}

func TestNarrowPlannerPickingIsExplicitAndCancellable(t *testing.T) {
	m := journeyViewer(t)
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
	m.openJourney()
	j, _ := m.workspace.journey(m.planner.ref)
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	clickJourneyMap(m, 5, 2)
	if j.Stops[0].Location != nil || m.inspected == nil {
		t.Fatal("ordinary map click changed a journey stop")
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.ui.search.SetValue("unfinished place")
	m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if m.journeyPanelVisible() || !m.planner.pickingStop {
		t.Fatal("map picking did not expose the map")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !m.journeyPanelVisible() || m.ui.search.Value() != "unfinished place" || j.Stops[0].Location != nil {
		t.Fatal("cancelling map picking lost the stop editor")
	}
	m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	clickJourneyMap(m, 5, 2)
	if j.Stops[0].Location == nil || !m.journeyPanelVisible() || m.ui.dialog != dialogJourney {
		t.Fatal("explicit map picking did not commit the stop and restore the planner")
	}
}

func TestContextDrawerAlignsWithRightEdgeWithoutMapFrame(t *testing.T) {
	for _, width := range []int{120, 70, 30} {
		for _, ascii := range []bool{false, true} {
			m := themeViewer(t, width, 24, ascii)
			m.ui.detailsOpen = true
			m.rebuild()
			rows := strings.Split(ansi.Strip(m.content), "\n")
			top := []rune(rows[m.contextDrawerY()])
			border := m.boxBorder()
			if string(top[m.journeyPanelX()]) != border.TopLeft || string(top[width-1]) != border.TopRight {
				t.Fatal("drawer did not align with the right edge", width, ascii)
			}
			m.Update(tea.MouseWheelMsg{X: m.journeyPanelX() - 1, Y: m.contextDrawerY() + 1, Button: tea.MouseWheelDown})
			if m.ui.details.YOffset() != 0 {
				t.Fatal("drawer consumed the wheel outside its inset border")
			}
		}
	}
}

func TestPlannerFitKeepsStopsOutsideSidebar(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	enterJourneyCoordinate(m, 0, "51.309174, -2.583389")
	enterJourneyCoordinate(m, 1, "51.297454, -2.532641")
	j, _ := m.workspace.journey(m.planner.ref)
	for _, stop := range j.Stops {
		point := m.camera.screen(project(stop.Location.Lon, stop.Location.Lat))
		if point.X < 0 || point.X/2 >= float64(m.journeyPanelX()-1) {
			t.Fatal("fitted stop was hidden behind the sidebar", point)
		}
	}
}

func TestCollapsedStopEditorPreservesInputAndCamera(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	m.editJourneyLocation(0)
	m.ui.search.SetValue("uncommitted stop")
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	camera := m.camera
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.camera == camera || m.ui.search.Value() != "uncommitted stop" {
		t.Fatal("collapsed editor captured map navigation or changed its input")
	}
	m.Update(tea.KeyPressMsg{Code: '?'})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.ui.dialog != dialogJourneyLocation || !m.planner.collapsed {
		t.Fatal("help did not return to the collapsed stop editor")
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !m.journeyPanelVisible() || !m.ui.search.Focused() || m.ui.search.Value() != "uncommitted stop" {
		t.Fatal("expanding the editor did not restore its input and focus")
	}
}

func TestPlannerAlignsWithRightEdgeWithoutMapFrame(t *testing.T) {
	for _, width := range []int{120, 70, 30} {
		m := journeyViewer(t)
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m.openJourney()
		m.rebuild()
		row := []rune(strings.Split(ansi.Strip(m.content), "\n")[m.mapOriginY()])
		if string(row[m.journeyPanelX()]) != m.boxBorder().TopLeft || string(row[width-1]) != m.boxBorder().TopRight {
			t.Fatal("planner did not align with the right edge", width)
		}
		selected := m.planner.row
		m.Update(tea.MouseWheelMsg{X: m.journeyPanelX() - 1, Y: m.mapOriginY(), Button: tea.MouseWheelDown})
		if m.planner.row != selected {
			t.Fatal("wheel outside the inset planner changed its selection")
		}
	}
}

func TestSharedPanelSwitchesViewsWithoutLosingContext(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {30, 12}} {
		m := themeViewer(t, size[0], size[1], false)
		m.settings.Elevation = elevationHide
		m.layout()
		selection := m.workspace.selection
		m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		m.rebuild()
		m.ui.details.GotoBottom()
		offset := m.ui.details.YOffset()
		for _, kind := range []dialogKind{dialogWorkspace, dialogJourney, dialogNone} {
			switch kind {
			case dialogWorkspace:
				m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
			case dialogJourney:
				m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
			default:
				m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			}
			m.rebuild()
			rows := strings.Split(ansi.Strip(m.content), "\n")
			if !strings.Contains(rows[m.mapOriginY()], "Context · Files · Journey") || !m.panelVisible() {
				t.Fatal("panel switch lost the shared header", size, kind)
			}
			if m.workspace.selection != selection {
				t.Fatal("switching views changed the selected file")
			}
			if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) != size[1] {
				t.Fatal("shared panel overflowed terminal", size, kind)
			}
		}
		if m.ui.details.YOffset() != offset {
			t.Fatal("context scroll position was lost across panel views")
		}
		// The header also works with the mouse, including the narrow overlay.
		x := m.journeyPanelX() + 2 + ansi.StringWidth("Context · ")
		m.Update(tea.MouseClickMsg{X: x, Y: m.mapOriginY(), Button: tea.MouseLeft})
		if m.ui.dialog != dialogWorkspace {
			t.Fatal("Files header did not switch panel view")
		}
		m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		if m.ui.dialog != dialogNone || m.panelVisible() {
			t.Fatal("Escape did not close the whole sidebar")
		}
		m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
		m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		before := m.camera
		m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		if m.panelVisible() || m.camera == before {
			t.Fatal("collapsed Files panel captured map navigation")
		}
	}
}

func TestEscapeClearsLocationAfterClosingContext(t *testing.T) {
	for _, source := range []string{"click", "search"} {
		t.Run(source, func(t *testing.T) {
			m := themeViewer(t, 120, 40, false)
			if source == "click" {
				m.inspected = &XY{-2.5, 51.3}
			} else {
				m.openDialog(dialogSearch)
				m.ui.search.SetValue("51.3, -2.5")
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			camera, selection := m.camera, m.workspace.selection
			playback, following := *m.activePlayback(), m.following
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.ui.detailsOpen || m.inspected == nil {
				t.Fatal("first escape did not close context while retaining selection")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.inspected != nil || m.searchPosition != nil || m.searchName != "" {
				t.Fatal("second escape did not clear the location and search label")
			}
			if m.camera != camera || m.workspace.selection != selection ||
				!reflect.DeepEqual(*m.activePlayback(), playback) || m.following != following {
				t.Fatal("deselecting changed the camera, loaded selection or playback")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.camera != camera || m.workspace.selection != selection {
				t.Fatal("escape with no location changed the map")
			}
		})
	}
}

func TestMenuShortcutsKeepViewsOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
		kind dialogKind
	}{
		{"files", tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}, dialogWorkspace},
		{"journey", tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}, dialogJourney},
		{"open file", tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}, dialogFiles},
		{"commands", tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}, dialogCommands},
		{"help", tea.KeyPressMsg{Code: '?', Text: "?"}, dialogHelp},
		{"credits", tea.KeyPressMsg{Code: 'v', Text: "v"}, dialogCredits},
		{"speed", tea.KeyPressMsg{Code: 'p', Text: "p"}, dialogSpeed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := themeViewer(t, 120, 40, false)
			m.Update(tc.key)
			if m.ui.dialog != tc.kind {
				t.Fatal("shortcut did not open the expected view")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			selected, row, input := m.ui.selected, m.planner.row, m.ui.search.Value()
			for range 2 {
				m.Update(tc.key)
				if m.ui.dialog != tc.kind || m.ui.selected != selected ||
					m.planner.row != row || m.ui.search.Value() != input {
					t.Fatal("repeated shortcut closed or reset the active view")
				}
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.ui.dialog != dialogNone {
				t.Fatal("Escape did not close the view")
			}
		})
	}
}

func TestJourneyShortcutPreservesNestedEditor(t *testing.T) {
	m := journeyViewer(t)
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.ui.search.SetValue("unfinished place")
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if m.ui.dialog != dialogJourneyLocation || m.ui.search.Value() != "unfinished place" {
		t.Fatal("journey shortcut dismissed or reset the nested editor")
	}
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if !m.journeyPanelVisible() || !m.ui.search.Focused() || m.ui.search.Value() != "unfinished place" {
		t.Fatal("journey shortcut did not restore the collapsed editor")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.ui.dialog != dialogJourney {
		t.Fatal("Escape did not return to the parent journey view")
	}
}

func TestSidebarVisibilityAndTabCycle(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	m.settings.Elevation = elevationHide
	m.layout()
	selection, camera := m.workspace.selection, m.camera
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	for _, kind := range []dialogKind{dialogWorkspace, dialogJourney, dialogNone} {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		if m.ui.dialog != kind || !m.panelVisible() {
			t.Fatal("Tab did not cycle to the next sidebar view", kind)
		}
		m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		if m.panelVisible() || m.ui.dialog != kind {
			t.Fatal("Ctrl-B did not hide the sidebar while retaining its tab")
		}
		m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		if !m.panelVisible() || m.ui.dialog != kind {
			t.Fatal("Ctrl-B did not restore the same sidebar tab")
		}
		for _, code := range []rune{'1', '2', '3'} {
			m.Update(tea.KeyPressMsg{Code: code, Mod: tea.ModAlt})
			if m.ui.dialog != kind {
				t.Fatal("removed Alt-number shortcut changed tabs")
			}
		}
	}
	if m.workspace.selection != selection || m.camera.Center != camera.Center || m.camera.Zoom != camera.Zoom {
		t.Fatal("sidebar navigation changed the selected file or map view")
	}
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if m.ui.dialog != dialogWorkspace || !m.panelVisible() {
		t.Fatal("Ctrl-L did not open Files from a hidden Journey panel")
	}
}

func TestEscapeClosesSidebarAfterCyclingTabs(t *testing.T) {
	for _, tabs := range []int{0, 1, 2, 3, 5} {
		t.Run(fmt.Sprintf("tabs=%d", tabs), func(t *testing.T) {
			m := themeViewer(t, 120, 40, false)
			m.inspected = &XY{-2.5, 51.3}
			selection, position := m.workspace.selection, m.inspected
			m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			for range tabs {
				m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			}
			camera := m.camera
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.panelVisible() || m.ui.dialog != dialogNone || len(m.ui.parents) != 0 {
				t.Fatal("Escape navigated tab history instead of closing the sidebar")
			}
			if m.camera != camera || m.workspace.selection != selection || m.inspected != position {
				t.Fatal("closing sidebar changed the map or selected location")
			}
		})
	}
}

func TestEmptyJourneyDraftDoesNotBecomeLoadedFile(t *testing.T) {
	for _, imported := range []bool{false, true} {
		t.Run(fmt.Sprintf("imported=%t", imported), func(t *testing.T) {
			m := journeyViewer(t)
			if imported {
				if _, err := m.workspace.open("../fixtures/country-ride.gpx"); err != nil {
					t.Fatal(err)
				}
			}
			selection, rows := m.workspace.selection, m.workspaceRows()
			m.openJourney()
			ref := m.planner.ref
			m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
			if m.workspace.selection != selection || !reflect.DeepEqual(rows, m.workspaceRows()) {
				t.Fatal("opening an empty planner changed the active file or listed an empty journey")
			}
			m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
			if m.planner.ref != ref {
				t.Fatal("reopening an empty planner created another draft")
			}
			enterJourneyCoordinate(m, 0, "51.309174, -2.583389")
			if !reflect.DeepEqual(rows, m.workspaceRows()) || m.workspace.selection != selection {
				t.Fatal("a single-location draft appeared in Files or became selected")
			}
			enterJourneyCoordinate(m, 1, "51.297454, -2.532641")
			if len(m.workspaceRows()) != len(rows)+2 {
				t.Fatal("a complete journey did not appear in Files")
			}
			m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
			if len(m.workspaceRows()) != len(rows)+2 {
				t.Fatal("uncommitted waypoint removed the journey from Files")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if len(m.workspaceRows()) != len(rows)+2 {
				t.Fatal("undoing the unset waypoint did not restore the complete journey")
			}
			if imported && m.workspace.selection != selection {
				t.Fatal("setting a journey location replaced the imported file selection")
			}
			m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			if !reflect.DeepEqual(rows, m.workspaceRows()) || m.workspace.selection != selection {
				t.Fatal("undoing the first location left an empty journey listed or selected")
			}
		})
	}
}

func TestHiddenSidebarUsesMapKeysWithoutChangingFiles(t *testing.T) {
	for _, kind := range []dialogKind{dialogWorkspace, dialogJourney} {
		m := themeViewer(t, 120, 40, false)
		m.switchPanel(kind)
		m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		rows := m.workspaceRows()
		selection := m.workspace.selection
		m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
		m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
		m.Update(tea.KeyPressMsg{Code: 'D', Text: "D"})
		if !reflect.DeepEqual(rows, m.workspaceRows()) || m.workspace.selection != selection {
			t.Fatal("hidden panel deleted or selected a file", kind)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
		if !m.activePlayback().Playing || !reflect.DeepEqual(rows, m.workspaceRows()) {
			t.Fatal("hidden panel swallowed playback or toggled file visibility", kind)
		}
		beforeProfile := m.profileEnabled()
		m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
		if m.profileEnabled() == beforeProfile {
			t.Fatal("hidden panel swallowed the profile toggle", kind)
		}
	}
}

func TestFilesKeysAfterLeavingJourneyMapPick(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	m.openJourney()
	m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	selected, camera := m.ui.selected, m.camera
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.ui.selected == selected || m.camera != camera {
		t.Fatal("stale journey picking state captured Files navigation")
	}
}

func TestContextSummaryShowsBuildVersion(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	for _, build := range []string{"dev", "v0.1.0", "v0.1.0-rc.1", "v0.1.0-" + strings.Repeat("long", 20)} {
		version = build
		for _, width := range []int{30, 39, 40, 59, 60, 70, 109, 110, 120, 160} {
			t.Run(fmt.Sprintf("%s/%d", build, width), func(t *testing.T) {
				m := themeViewer(t, width, 40, false)
				rows := m.contextSummaryRows()
				_, first, _ := m.contextSummary()
				if width < 60 && !strings.Contains(ansi.Strip(rows[1]), first) {
					t.Fatalf("build version displaced context fact %q: %q", first, ansi.Strip(rows[1]))
				}
				text := ansi.Strip(strings.Join(rows, "\n"))
				if len(build) <= 10 && !strings.Contains(text, build) {
					t.Fatalf("missing build version %q: %s", build, text)
				}
				for _, hint := range []string{"ctrl+b sidebar", "/ search", "? help"} {
					if !strings.Contains(text, hint) {
						t.Fatalf("missing hint %q: %s", hint, text)
					}
				}
				for _, row := range rows {
					if ansi.StringWidth(row) > width {
						t.Fatalf("row exceeds width %d: %q", width, ansi.Strip(row))
					}
				}
			})
		}
	}
}

func TestWideContextSummaryKeepsInspectedCoordinates(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	version = "v0.1.0-" + strings.Repeat("long", 20)
	for _, width := range []int{110, 120, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := themeViewer(t, width, 40, false)
			m.inspected = &XY{X: -179.12345, Y: -89.12345}
			rows := m.contextSummaryRows()
			text := ansi.Strip(strings.Join(rows, "\n"))
			_, latitude, longitude := m.contextSummary()
			for _, want := range []string{latitude, longitude, "v0.1.0-", "ctrl+b sidebar", "/ search", "? help"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q at width %d: %s", want, width, text)
				}
			}
			if len(rows) != 1 || ansi.StringWidth(rows[0]) > width {
				t.Fatalf("summary exceeds its row: %q", text)
			}
		})
	}
}
