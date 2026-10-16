package main

import (
	"fmt"
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type favouritesUI struct {
	entries       []favourite
	pending       favourite
	expected      *favourite
	deleteButtons [2]image.Rectangle
}

func (m *viewer) favouritesDialogOpen() bool {
	return m.ui.dialog == dialogFavourites || m.ui.dialog == dialogFavouriteName || m.ui.dialog == dialogFavouriteDelete
}

func (m *viewer) textDialogOpen() bool {
	return m.ui.dialog == dialogCommands || m.placeSearchOpen() ||
		m.ui.dialog == dialogFavourites || m.ui.dialog == dialogFavouriteName
}

func (m *viewer) loadFavourites() {
	m.favourites.entries = nil
	store, err := localFavouriteStore()
	if err == nil {
		m.favourites.entries, err = store.read()
	}
	if err != nil {
		m.ui.notice = err.Error()
	}
}

func (m *viewer) matchingFavourites() []favourite {
	query := favouriteNameKey(m.ui.search.Value())
	var matches []favourite
	for _, entry := range m.favourites.entries {
		if strings.Contains(favouriteNameKey(entry.Name), query) {
			matches = append(matches, entry)
		}
	}
	return matches
}

func (m *viewer) favouriteCandidate() (placeLocation, error) {
	if m.placeSearchOpen() {
		location, matched, err := coordinateLocation(m.ui.search.Value())
		if err != nil {
			return placeLocation{}, err
		}
		if !matched {
			return placeLocation{}, fmt.Errorf("choose a search result or enter coordinates to save a favourite")
		}
		if m.ui.dialog == dialogJourneyLocation && !m.planner.addingWaypoint {
			location = m.workspace.journeyCoordinateEdit(m.planner.ref, m.planner.stopID, location)
		}
		return location, nil
	}
	if m.inspected == nil {
		return placeLocation{}, fmt.Errorf("select a location on the map first")
	}
	if m.inspected == m.searchPosition && m.searchLocation != nil {
		return *m.searchLocation, nil
	}
	return placeLocation{Lon: m.inspected.X, Lat: m.inspected.Y, Source: placeSourceMapPoint}, nil
}

func (m *viewer) saveFavouritePrompt() tea.Cmd {
	if m.placeSearchOpen() && len(m.planner.suggestions) > 0 {
		return m.saveSearchFavourite()
	}
	m.ui.feedback = ""
	location, err := m.favouriteCandidate()
	if err == nil {
		err = validateFavourite(newFavourite("Favourite", location))
	}
	if err != nil && location.Source == placeSourceMapboxSearch {
		err = fmt.Errorf("this result is temporary; save the place from the search results")
	}
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	m.favourites.pending = newFavourite("", location)
	m.favourites.expected = nil
	cmd := m.pushDialog(dialogFavouriteName)
	if _, coordinate, _ := coordinateLocation(location.Name); !coordinate {
		m.ui.search.SetValue(location.Name)
	}
	return cmd
}

func (m *viewer) updateFavouritesDialog(msg tea.Msg) tea.Cmd {
	if m.ui.dialog == dialogFavouriteDelete {
		return m.updateFavouriteDelete(msg)
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		pressed := key.String()
		if pressed == "esc" {
			return m.backDialog()
		}
		if m.ui.dialog == dialogFavouriteName {
			if pressed == "enter" {
				return m.commitFavourite()
			}
		} else {
			matches := m.matchingFavourites()
			m.ui.selected = min(m.ui.selected, max(0, len(matches)-1))
			switch pressed {
			case "up", "down":
				direction := 1
				if pressed == "up" {
					direction = -1
				}
				m.ui.selected = menuStep(m.ui.selected, direction, len(matches))
				m.ui.menuNavigated = true
				return nil
			case "enter", "ctrl+r", "delete", "backspace":
				editingFilter := m.ui.search.Value() != "" && !m.ui.menuNavigated
				if pressed == "backspace" && (editingFilter || len(matches) == 0) {
					break
				}
				if len(matches) == 0 {
					return nil
				}
				selected := matches[m.ui.selected]
				if pressed == "enter" {
					// Restore the editor and its stop ID before copying the value.
					m.backDialog()
					for m.ui.dialog == dialogCommands && len(m.ui.parents) > 0 {
						m.backDialog()
					}
					if !m.placeSearchOpen() {
						m.openDialog(dialogSearch)
					}
					return m.applyPlaceLocation(selected.location())
				}
				m.favourites.pending = selected
				m.favourites.expected = &selected
				if pressed == "delete" || pressed == "backspace" {
					return m.pushDialog(dialogFavouriteDelete)
				}
				cmd := m.pushDialog(dialogFavouriteName)
				m.ui.search.SetValue(selected.Name)
				return cmd
			}
		}
	}
	before := m.ui.search.Value()
	var cmd tea.Cmd
	m.ui.search, cmd = m.ui.search.Update(msg)
	cmd = m.clipboardCommand(msg, cmd)
	if before != m.ui.search.Value() {
		m.ui.selected = 0
		m.ui.menuNavigated = false
		m.ui.feedback = ""
	}
	return cmd
}

func (m *viewer) commitFavourite() tea.Cmd {
	deleting := m.ui.dialog == dialogFavouriteDelete
	store, err := localFavouriteStore()
	if err == nil {
		var replacement *favourite
		if m.ui.dialog == dialogFavouriteName {
			m.favourites.pending.Name = strings.TrimSpace(m.ui.search.Value())
			replacement = &m.favourites.pending
		}
		err = store.change(m.favourites.expected, replacement)
	}
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	cmd := m.backDialog()
	m.ui.notice, m.ui.feedback = "", ""
	if m.ui.dialog == dialogFavourites {
		m.loadFavourites()
	}
	if m.ui.notice == "" {
		m.ui.feedback = "Favourite saved"
		if deleting {
			m.ui.feedback = "Favourite deleted"
		}
	}
	return cmd
}

func (m *viewer) favouritesContent(width, height int) (string, string, string) {
	m.ui.search.SetWidth(max(1, width-2))
	if m.ui.dialog == dialogFavouriteDelete {
		return m.favouriteDeleteContent(width, height)
	}
	if m.ui.dialog == dialogFavouriteName {
		title := "Save favourite"
		if m.favourites.expected != nil {
			title = "Rename favourite"
		}
		return title, m.ui.search.View(), "enter save / esc cancel"
	}
	rows := []string{m.ui.search.View()}
	matches := m.matchingFavourites()
	if len(matches) == 0 && height > 1 {
		rows = append(rows, m.styles.dialogMuted.Render("No favourites found"))
	}
	count := max(1, height-len(rows))
	m.ui.selected = min(m.ui.selected, max(0, len(matches)-1))
	start := max(0, m.ui.selected-count+1)
	for i := start; i < min(len(matches), start+count); i++ {
		style := m.styles.dialogText
		if i == m.ui.selected {
			style = m.styles.selected
		}
		rows = append(rows, style.Render(pad(matches[i].Name, width)))
	}
	return "Favourites", strings.Join(rows, "\n"), "enter use / ctrl+r rename / del/backspace delete / esc"
}

func (m *viewer) updateFavouriteDelete(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
		for i, bounds := range m.favourites.deleteButtons {
			if image.Pt(click.X, click.Y).In(bounds) {
				if i == 0 {
					return m.backDialog()
				}
				m.ui.selected = i
				return m.commitFavourite()
			}
		}
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			return m.backDialog()
		case "tab", "shift+tab", "left", "right":
			m.ui.selected = 1 - m.ui.selected
		case "enter", "space":
			if m.ui.selected == 0 {
				return m.backDialog()
			}
			return m.commitFavourite()
		}
	}
	return nil
}

func (m *viewer) favouriteDeleteContent(width, height int) (string, string, string) {
	message := ansi.Wrap("Remove "+m.favourites.pending.Name+"?", width, "")
	lines := strings.Split(message, "\n")
	limit := max(1, height-2)
	if len(lines) > limit {
		lines = lines[:limit]
		lines[limit-1] = ansi.Truncate(lines[limit-1]+"…", width, "…")
	}
	lines = append(lines, "")
	buttonRow := len(lines)
	left := max(0, width-22)
	buttons := []string{}
	for i, label := range []string{"Cancel", "Delete"} {
		style := m.styles.dialogText.Background(lipgloss.Color(m.theme().Selected)).Padding(0, 1)
		prefix := "  "
		if i == m.ui.selected {
			colour := m.theme().Accent
			if i == 1 {
				colour = m.theme().Highlight
			}
			style = style.Background(lipgloss.Color(colour)).Foreground(lipgloss.Color(m.theme().Surface)).Bold(true)
			prefix = "> "
		}
		buttons = append(buttons, style.Render(prefix+label))
		x := left + i*12
		m.favourites.deleteButtons[i] = image.Rect(x, buttonRow, x+10, buttonRow+1)
	}
	lines = append(lines, strings.Repeat(" ", left)+strings.Join(buttons, "  "))
	hints := "tab switch / enter select / esc cancel"
	if width < len(hints) {
		hints = "tab switch\nenter select / esc"
	}
	return "Delete favourite?", strings.Join(lines, "\n"), hints
}
