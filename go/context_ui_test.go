package main

import (
	"fmt"
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
			m.showProfile = false
			m.layout()
			m.rebuild()
			before, selection := m.camera, m.workspace.selection
			closed := m.content
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			m.rebuild()
			if !m.ui.detailsOpen || m.camera != before || m.workspace.selection != selection {
				t.Fatal("opening context moved the camera or changed selection")
			}
			for _, hint := range []string{"tab close", "/ search", "? help"} {
				if !strings.Contains(ansi.Strip(m.content), hint) {
					t.Fatal("drawer obscured persistent hints", hint)
				}
			}
			y := m.contextDrawerY() + 1
			m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseReleaseMsg{X: 3, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseWheelMsg{X: 3, Y: y, Button: tea.MouseWheelDown})
			if m.camera != before || m.inspected != nil || !m.ui.detailsOpen {
				t.Fatal("mouse over context reached the covered map")
			}
			if m.ui.details.YOffset() == 0 {
				t.Fatal("wheel did not scroll context")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			m.rebuild()
			if m.ui.detailsOpen || m.camera != before || m.content != closed {
				t.Fatal("closing context did not restore the same map")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
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
				m.showProfile = false
				m.layout()
				m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
				for i := range m.themes {
					m.applyTheme(i)
					m.rebuild()
					if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) != size[1] {
						t.Fatal("context overflowed terminal", m.theme().ID)
					}
					if m.ui.details.Style.GetBackground() != lipgloss.Color(m.theme().Surface) {
						t.Fatal("context viewport retained another theme's background")
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
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
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
			m.showProfile = false
			m.layout()
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			m.rebuild()
			assertOpen := func() {
				t.Helper()
				m.rebuild()
				if !m.ui.detailsOpen || !strings.Contains(ansi.Strip(m.content), "tab close") {
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
		m.showProfile = false
		m.layout()
		m.openDialog(dialogSearch)
		m.applyPlaceLocation(placeLocation{Name: "Selected place", Lon: 8.54, Lat: 47.37})
		for theme := range m.themes {
			m.applyTheme(theme)
			for _, key := range []rune{tea.KeyRight, '+', '.'} {
				m.Update(tea.KeyPressMsg{Code: key})
				m.rebuild()
				point := m.camera.screen(project(m.inspected.X, m.inspected.Y))
				x, y := int(point.X/2)+1, int(point.Y/4)+m.mapOriginY()
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
