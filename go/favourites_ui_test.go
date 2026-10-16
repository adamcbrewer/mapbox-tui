package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func saveTestFavourite(t *testing.T, m *viewer) {
	t.Helper()
	m.openDialog(dialogSearch)
	m.ui.search.SetValue("51.5, -0.1")
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
	if m.ui.dialog != dialogFavouriteName {
		t.Fatal("save shortcut did not open name input", m.ui.notice)
	}
	m.Update(tea.PasteMsg{Content: "Home"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.ui.dialog != dialogSearch || m.ui.search.Value() != "51.5, -0.1" {
		t.Fatal("saving did not return to the original location editor", m.ui.notice)
	}
	m.closeDialog()
}

func TestFavouritesOfflineReuseAndWorkspaceIndependence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestFavourite(t, journeyViewer(t))
	m := journeyViewer(t) // A fresh viewer loads the persisted library.
	m.places.token = "token"
	m.places.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("using a favourite called the search provider")
		return nil, nil
	})
	m.openDialog(dialogSearch)
	m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: "hom"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.ui.dialog != dialogNone || m.searchName != "Home" || *m.inspected != (XY{-0.1, 51.5}) {
		t.Fatal("offline quick search did not use saved coordinates")
	}
	m.openJourney()
	j, _ := m.workspace.journey(m.planner.ref)
	j.Mode = journeyManual
	for _, position := range []string{"start", "destination", "waypoint"} {
		switch position {
		case "start":
			m.editJourneyLocation(0)
		case "destination":
			m.editJourneyLocation(1)
		case "waypoint":
			m.editNewWaypoint()
		}
		m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if m.ui.dialog != dialogJourney {
			t.Fatal("favourite did not return to journey", position, m.ui.dialog)
		}
	}
	for _, stop := range j.Stops {
		if stop.Location == nil || stop.Location.Name != "Home" || stop.Location.Lat != 51.5 {
			t.Fatal("favourite was not copied to every stop", j.Stops)
		}
	}
	m.runCommand("favourites")
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m.ui.search.SetValue("New home")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(m.favourites.entries) != 1 || m.favourites.entries[0].Name != "New home" {
		t.Fatal("cancelled deletion changed the library")
	}
	for _, stop := range j.Stops {
		if stop.Location.Name != "Home" {
			t.Fatal("renaming favourite changed a copied journey stop")
		}
	}
	m.closeDialog()
	m.clearWorkspace()
	m.runCommand("favourites")
	if len(m.favourites.entries) != 1 {
		t.Fatal("clearing workspace removed favourites")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	store, _ := localFavouriteStore()
	entries, err := store.read()
	if err != nil || len(entries) != 0 {
		t.Fatal("confirmed deletion was not saved", entries, err)
	}
	for _, stop := range j.Stops {
		if stop.Location.Name != "Home" {
			t.Fatal("deleting favourite changed a copied journey stop")
		}
	}
}

func TestFavouritesProviderLocationsRemainTemporary(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := journeyViewer(t)
	provider := placeLocation{Name: "Cafe", Lon: -0.1, Lat: 51.5, Source: placeSourceMapboxSearch}
	m.openDialog(dialogSearch)
	m.applyPlaceLocation(provider)
	m.runCommand("save-favourite")
	if m.ui.dialog == dialogFavouriteName || !strings.Contains(m.ui.notice, "temporary") {
		t.Fatal("selected provider result could be saved", m.ui.notice)
	}
	m.openDialog(dialogSearch)
	m.Update(tea.KeyPressMsg{Code: 'm', Mod: tea.ModAlt})
	m.runCommand("save-favourite")
	if m.ui.dialog == dialogFavouriteName || !strings.Contains(m.ui.notice, "temporary") {
		t.Fatal("reusing searched map centre removed its origin", m.ui.notice)
	}
	m.openJourney()
	m.editJourneyLocation(0)
	m.applyJourneyLocation(provider)
	for _, input := range []string{"51.5, -0.1", "51.51, -0.11"} {
		m.editJourneyLocation(0)
		m.ui.search.SetValue(input)
		m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
		if m.ui.dialog != dialogJourneyLocation || !strings.Contains(m.ui.notice, "temporary") {
			t.Fatal("editing provider coordinates removed their origin", m.ui.notice)
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	m.editJourneyLocation(0)
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
	if m.ui.dialog != dialogJourneyLocation || !strings.Contains(m.ui.notice, "temporary") {
		t.Fatal("accepted coordinate edit removed provider origin", m.ui.notice)
	}
}

func TestFavouritesLayoutsAndNestedInput(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/ascii=%v", size[0], size[1], ascii), func(t *testing.T) {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				m := journeyViewer(t)
				saveTestFavourite(t, m)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.settings.ASCII = ascii
				m.runCommand("favourites")
				m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
				m.ui.search.SetValue("Quiet home")
				m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				if m.ui.search.Value() != "Quiet home" || m.ui.dialog != dialogFavouriteName {
					t.Fatal("nested command palette lost rename input")
				}
				m.rebuild()
				if m.cursor == nil || !strings.Contains(ansi.Strip(m.content), "Rename favourite") {
					t.Fatal("rename input or cursor missing")
				}
				if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
					t.Fatal("favourite dialog overflowed terminal")
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				m.Update(tea.PasteMsg{Content: "qhjkl"})
				if m.ui.search.Value() != "qhjkl" {
					t.Fatal("favourite filter intercepted map keys")
				}
			})
		}
	}
}

func TestFavouriteReplacesStopWithItsOwnNameAndOrigin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := journeyViewer(t)
	saveTestFavourite(t, m)
	m.openJourney()
	m.editJourneyLocation(0)
	m.applyJourneyLocation(placeLocation{
		Name: "Temporary cafe", Lon: -0.1, Lat: 51.5, Source: placeSourceMapboxSearch, Attribution: "Search credits",
	})
	m.editJourneyLocation(0)
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m.runCommand("favourites")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	j, _ := m.workspace.journey(m.planner.ref)
	location := j.Stops[0].Location
	if m.ui.dialog != dialogJourney || location.Name != "Home" || !location.userEntered() {
		t.Fatal("replacing a stop with a favourite lost its name/origin or editor target", location)
	}
}

func TestFavouriteNestedEditsRetainTheirIdentity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := journeyViewer(t)
	saveTestFavourite(t, m)
	store, _ := localFavouriteStore()
	park := newFavourite("Park", placeLocation{Lon: 1, Lat: 51, Source: placeSourceCoordinates})
	if err := store.change(nil, &park); err != nil {
		t.Fatal(err)
	}
	m.runCommand("favourites")
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m.ui.search.SetValue("Maison")
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m.runCommand("favourites")
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m.ui.search.SetValue("Garden")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // Back to Commands.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // Back to the original rename.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	entries, err := store.read()
	if err != nil || len(entries) != 2 || entries[0].Name != "Maison" || entries[1].Name != "Garden" {
		t.Fatal("nested forms changed the wrong favourite", entries, err)
	}
}

func TestFavouriteDeleteKeysAndFilterEditing(t *testing.T) {
	for _, key := range []rune{tea.KeyDelete, tea.KeyBackspace} {
		t.Run(fmt.Sprint(key), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			m := journeyViewer(t)
			saveTestFavourite(t, m)
			m.runCommand("favourites")
			m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
			if m.ui.dialog != dialogFavourites {
				t.Fatal("Ctrl-D still opened deletion")
			}
			m.Update(tea.PasteMsg{Content: "Home"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
			if m.ui.dialog != dialogFavourites || m.ui.search.Value() != "Hom" {
				t.Fatal("Backspace stopped editing the filter")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			m.Update(tea.KeyPressMsg{Code: key})
			if m.ui.dialog != dialogFavouriteDelete {
				t.Fatal("delete key did not act on selected favourite")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.ui.search.Value() != "Hom" || len(m.favourites.entries) != 1 {
				t.Fatal("cancel lost the filter or favourite")
			}
			m.Update(tea.KeyPressMsg{Code: key})
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			store, _ := localFavouriteStore()
			entries, err := store.read()
			if err != nil || len(entries) != 0 || m.ui.feedback != "Favourite deleted" {
				t.Fatal("deletion was not persisted or reported", entries, err)
			}
		})
	}
}

func TestSearchFeedbackAndAttributionLayout(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := journeyViewer(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			prepareFavouriteSearch(t, m)
			m.planner.suggestions[1].Attribution = "Unique Mapbox copyright"
			m.ui.feedback = "Favourite saved"
			_, body, hints := m.dialogContent(22, 4)
			if strings.Contains(body+hints, "Unique Mapbox copyright") {
				t.Fatal("copyright is duplicated in search modal")
			}
			if !strings.Contains(m.journeyCredits(), "Unique Mapbox copyright") {
				t.Fatal("removing modal copyright removed context attribution")
			}
			m.rebuild()
			screen := ansi.Strip(m.content)
			feedbackAt, resultAt := strings.Index(screen, "Favourite saved"), strings.Index(screen, "Hinton Blewett")
			if feedbackAt < 0 || resultAt < 0 || feedbackAt < resultAt {
				t.Fatal("feedback is missing or above results", screen)
			}
			if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
				t.Fatal("feedback overflowed terminal")
			}
		})
	}
}

func TestFavouriteConfirmationButtons(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 18}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/ascii=%v", size[0], size[1], ascii), func(t *testing.T) {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				m := journeyViewer(t)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.settings.ASCII = ascii
				store, _ := localFavouriteStore()
				entry := newFavourite(
					"Compton Martin, Bath and North East Somerset, England, United Kingdom",
					placeLocation{Source: placeSourceCoordinates, Lat: 51, Lon: -2},
				)
				if err := store.change(nil, &entry); err != nil {
					t.Fatal(err)
				}
				m.runCommand("favourites")
				m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if m.ui.dialog != dialogFavourites || len(m.favourites.entries) != 1 {
					t.Fatal("default confirmation did not cancel")
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
				m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
				m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
				m.rebuild()
				clickButton := func(label string) {
					t.Helper()
					lines := strings.Split(ansi.Strip(m.content), "\n")
					for y := len(lines) - 1; y >= 0; y-- {
						line := lines[y]
						if x := strings.Index(line, label); x >= 0 {
							m.Update(tea.MouseClickMsg{X: ansi.StringWidth(line[:x]), Y: y, Button: tea.MouseLeft})
							return
						}
					}
					t.Fatal("button missing", label, ansi.Strip(m.content))
				}
				clickButton("> Cancel")
				if m.ui.dialog != dialogFavourites || len(m.favourites.entries) != 1 {
					t.Fatal("Cancel button did not preserve the favourite")
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
				m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
				m.rebuild()
				if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
					t.Fatal("confirmation overflowed terminal")
				}
				clickButton("> Delete")
				entries, err := store.read()
				if err != nil || len(entries) != 0 || m.ui.dialog != dialogFavourites {
					t.Fatal("Delete button did not persist deletion", entries, err)
				}
			})
		}
	}
}
