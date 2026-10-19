package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func openTestWorkspaceLibrary(t *testing.T, m *viewer) {
	t.Helper()
	cmd := m.openSavedWorkspaceDialog("open")
	m.Update(cmd())
}

func TestWorkspaceManagementSearchSortRenameDuplicateDelete(t *testing.T) {
	for _, size := range []struct {
		width, height int
		ascii         bool
	}{{120, 40, false}, {70, 24, false}, {70, 24, true}} {
		t.Run(fmt.Sprintf("%dx%d-ascii-%t", size.width, size.height, size.ascii), func(t *testing.T) {
			m := themeViewer(t, size.width, size.height, size.ascii)
			cp, err := m.saveWorkspaceCheckpoint("Z café", false)
			if err != nil {
				t.Fatal(err)
			}
			m.camera.Zoom++
			openTestWorkspaceLibrary(t, m)
			m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: 'n'})
			m.ui.search.SetValue("東京 / café")
			cmd := m.manageSavedWorkspace()
			if cmd == nil {
				t.Fatal(m.ui.notice)
			}
			m.Update(cmd())
			if m.workspace.checkpoint.manifest.ID != cp.manifest.ID || !m.workspace.modified(m.destination(), m.planner.ref) {
				t.Fatal("rename lost identity or cleared unsaved working edits")
			}
			renamed := m.workspace.checkpoint
			if *renamed.manifest.Camera.Zoom != *cp.manifest.Camera.Zoom {
				t.Fatal("rename saved unsaved camera edits")
			}
			m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: 'd'})
			m.Update(m.manageSavedWorkspace()())
			if len(m.savedWork.entries) != 2 || m.workspace.checkpoint != renamed {
				t.Fatal("duplicate changed the active checkpoint")
			}
			copy := m.savedWork.entries[m.savedWork.selected].checkpoint
			if copy.manifest.ID == cp.manifest.ID || *copy.manifest.Camera.Zoom != *cp.manifest.Camera.Zoom {
				t.Fatal("duplicate reused identity or included working changes")
			}
			m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: 'o'})
			m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: '/'})
			m.Update(tea.PasteMsg{Content: "no matches"})
			_, body, _ := m.savedWorkspaceContent(45, 12)
			if len(m.savedWork.entries) != 0 || !strings.Contains(ansi.Strip(body), "No matching") {
				t.Fatal("missing no-matches state", body)
			}
			m.ui.search.SetValue("CAFÉ")
			m.updateSavedWorkspaceDialog(tea.PasteMsg{Content: ""})
			if len(m.savedWork.entries) != 2 {
				t.Fatal("Unicode search failed")
			}
			savedKey(m, tea.KeyEnter)
			index := 0
			if m.savedWork.entries[index].path != renamed.path {
				index = 1
			}
			m.selectLibraryWorkspace(index)
			m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: tea.KeyDelete})
			_, body, _ = m.savedWorkspaceContent(45, 12)
			if !strings.Contains(ansi.Strip(body), renamed.manifest.ID) {
				t.Fatal("delete did not identify checkpoint", body)
			}
			items := len(m.workspace.items())
			m.Update(m.manageSavedWorkspace()())
			if m.workspace.checkpoint != nil || len(m.workspace.items()) != items {
				t.Fatal("delete removed working copy or retained save target")
			}
			fresh, err := m.saveWorkspaceCheckpoint("New checkpoint", false)
			if err != nil || fresh.manifest.ID == renamed.manifest.ID {
				t.Fatal("Save after delete recreated checkpoint", err)
			}
			if _, err := os.Stat(renamed.path); !os.IsNotExist(err) {
				t.Fatal("deleted checkpoint reappeared", err)
			}
		})
	}
}

func TestWorkspaceUnsavedSwitchChoices(t *testing.T) {
	for _, action := range []string{"open", "clear"} {
		for _, choice := range []string{"save", "discard", "cancel"} {
			t.Run(action+"-"+choice, func(t *testing.T) {
				m := themeViewer(t, 70, 24, false)
				cp, err := m.saveWorkspaceCheckpoint("Original", false)
				if err != nil {
					t.Fatal(err)
				}
				store, err := localWorkspaceStore()
				if err != nil {
					t.Fatal(err)
				}
				target, err := store.duplicate(cp, "Other")
				if err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(cp.path)
				if err != nil {
					t.Fatal(err)
				}
				m.camera.Zoom++
				zoom, generation := m.camera.Zoom, m.workspace.liveIdentity()
				if action == "clear" {
					m.runCommand("clear")
				} else {
					openTestWorkspaceLibrary(t, m)
					m.Update(m.beginSavedOpen(target.path)())
				}
				if m.savedWork.mode != "unsaved" || m.workspace.liveIdentity() != generation {
					t.Fatal("switch bypassed unsaved changes prompt")
				}
				switch choice {
				case "cancel":
					savedKey(m, tea.KeyEscape)
				case "discard":
					savedKey(m, 'd')
				case "save":
					savedKey(m, 's')
					cmd := m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
					if action == "open" {
						if cmd == nil {
							t.Fatal("Save did not resume open", m.ui.notice)
						}
						m.Update(cmd())
					}
				}
				if choice == "cancel" {
					if m.workspace.liveIdentity() != generation || m.camera.Zoom != zoom {
						t.Fatal("cancel changed working copy")
					}
				} else if action == "clear" {
					if m.workspace.checkpoint != nil || len(m.workspace.items()) != 0 {
						t.Fatal("switch did not clear workspace")
					}
				} else if m.workspace.checkpoint.path != target.path {
					t.Fatal("switch did not open selected save")
				}
				after, err := os.ReadFile(cp.path)
				if err != nil {
					t.Fatal(err)
				}
				if choice != "save" && !bytes.Equal(before, after) {
					t.Fatal("switch implicitly saved changes")
				}
				if choice == "save" {
					checkpoint, err := readWorkspace(cp.path)
					if err != nil || *checkpoint.manifest.Camera.Zoom != zoom {
						t.Fatal("Save did not preserve edits", err)
					}
				}
			})
		}
	}
}

func TestWorkspaceUnsavedButtonsDefaultToSave(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {30, 12}} {
		for _, choice := range []string{"save-key", "cancel-key", "discard-key", "save-click", "cancel-click", "discard-click"} {
			t.Run(fmt.Sprintf("%dx%d-%s", size[0], size[1], choice), func(t *testing.T) {
				m := themeViewer(t, size[0], size[1], false)
				cp, err := m.saveWorkspaceCheckpoint("Original", false)
				if err != nil {
					t.Fatal(err)
				}
				m.camera.Zoom++
				generation := m.workspace.liveIdentity()
				openTestWorkspaceLibrary(t, m)
				m.Update(m.beginSavedOpen(cp.path)())
				m.rebuild()
				screen := ansi.Strip(m.content)
				for _, want := range []string{"Save before loading?", "Cancel", "Discard changes", "> Save"} {
					if !strings.Contains(screen, want) {
						t.Fatal("missing compact confirmation control", want, screen)
					}
				}
				if strings.Contains(screen, "Opening local content") || strings.Contains(screen, "in-memory") {
					t.Fatal("stale progress or explanatory prose remains in confirmation", screen)
				}
				if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) > size[1] {
					t.Fatal("confirmation overflowed terminal")
				}
				if strings.HasSuffix(choice, "click") {
					index := map[string]int{"cancel-click": 0, "discard-click": 1, "save-click": 2}[choice]
					bounds := m.savedWork.confirmButtons[index]
					if bounds.Empty() || !bounds.In(image.Rect(0, 0, size[0], size[1])) {
						t.Fatal("button has invalid click bounds", bounds)
					}
					m.Update(tea.MouseClickMsg{X: bounds.Min.X, Y: bounds.Min.Y, Button: tea.MouseLeft})
				} else {
					switch choice {
					case "cancel-key":
						savedKey(m, tea.KeyTab)
					case "discard-key":
						m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
					}
					savedKey(m, tea.KeyEnter)
				}
				switch {
				case strings.HasPrefix(choice, "save"):
					if m.savedWork.mode != "save" || m.workspace.liveIdentity() != generation {
						t.Fatal("Save did not open the explicit save flow")
					}
				case strings.HasPrefix(choice, "cancel"):
					if m.ui.dialog != dialogNone || m.workspace.liveIdentity() != generation {
						t.Fatal("Cancel changed working copy")
					}
				case strings.HasPrefix(choice, "discard"):
					if m.workspace.liveIdentity() == generation || m.ui.dialog != dialogNone {
						t.Fatal("Discard did not finish opening")
					}
				}
			})
		}
	}
}

func TestWorkspaceUnsavedButtonsKeepFocusAfterCommandMenu(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	if _, err := m.saveWorkspaceCheckpoint("Original", false); err != nil {
		t.Fatal(err)
	}
	m.camera.Zoom++
	m.startCleanWorkspace()
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	savedKey(m, tea.KeyEscape)
	if m.savedWork.mode != "unsaved" || m.ui.search.Focused() || m.savedWork.confirmSelected != 2 {
		t.Fatal("command menu restored text input instead of Save button focus")
	}
	savedKey(m, tea.KeyEnter)
	if m.savedWork.mode != "save" {
		t.Fatal("Enter did not choose the focused Save button")
	}
}

func TestWorkspaceUnsavedSaveFailureAndSaveAs(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	cp, err := m.saveWorkspaceCheckpoint("Original", false)
	if err != nil {
		t.Fatal(err)
	}
	m.camera.Zoom++
	identity := m.workspace.liveIdentity()
	if err := os.WriteFile(cp.path, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	m.startCleanWorkspace()
	savedKey(m, 's')
	savedKey(m, tea.KeyEnter)
	if m.savedWork.mode != "conflict" || m.workspace.liveIdentity() != identity {
		t.Fatal("failed save replaced working copy")
	}
	savedKey(m, 's')
	m.ui.search.SetValue("Preserved copy")
	savedKey(m, tea.KeyEnter)
	if len(m.workspace.items()) != 0 || m.workspace.checkpoint != nil {
		t.Fatal("successful Save As did not finish Start clean")
	}
	data, err := os.ReadFile(cp.path)
	if err != nil || string(data) != "external edit" {
		t.Fatal("Save As overwrote external edit", err)
	}
}

func TestWorkspaceSaveBeforeReopeningSameCheckpoint(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	cp, err := m.saveWorkspaceCheckpoint("Original", false)
	if err != nil {
		t.Fatal(err)
	}
	m.camera.Zoom++
	zoom := m.camera.Zoom
	openTestWorkspaceLibrary(t, m)
	m.Update(m.beginSavedOpen(cp.path)())
	savedKey(m, 's')
	cmd := m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("save did not resume opening")
	}
	m.Update(cmd())
	if m.camera.Zoom != zoom || m.workspace.checkpoint.manifest.Revision != cp.manifest.Revision+1 {
		t.Fatal("opening used the candidate from before Save")
	}
}

func TestWorkspaceModifiedComparesSavableContent(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	if _, err := m.saveWorkspaceCheckpoint("Original", false); err != nil {
		t.Fatal(err)
	}
	if m.workspace.modified(m.destination(), m.planner.ref) {
		t.Fatal("fresh save is dirty")
	}
	m.workspace.edited()
	m.settings.ASCII = !m.settings.ASCII
	if m.workspace.modified(m.destination(), m.planner.ref) {
		t.Fatal("live revision or global preference made workspace dirty")
	}
	m.camera.Zoom++
	if !m.workspace.modified(m.destination(), m.planner.ref) {
		t.Fatal("camera change was not detected")
	}
	m.camera.Zoom--
	if m.workspace.modified(m.destination(), m.planner.ref) {
		t.Fatal("reverted edit stayed dirty")
	}
	m.workspace.newJourney()
	if !m.workspace.modified(m.destination(), m.planner.ref) {
		t.Fatal("new journey was not detected")
	}
}

func TestWorkspaceModifiedIgnoresTransientRoutingAndTracksStops(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	ref := m.workspace.newJourney()
	m.planner.ref = ref
	if _, err := m.saveWorkspaceCheckpoint("Draft", false); err != nil {
		t.Fatal(err)
	}
	j, err := m.workspace.journey(ref)
	if err != nil {
		t.Fatal(err)
	}
	j.Busy, j.Error = true, "provider unavailable"
	j.Route = &journeyRoute{Distance: 1234, Duration: 567}
	j.automaticRoutePending = true
	m.workspace.edited()
	if m.workspace.modified(m.destination(), ref) {
		t.Fatal("transient route state made workspace dirty")
	}
	err = m.workspace.setJourneyLocation(ref, j.Stops[0].ID, placeLocation{
		Name: "New stop", Lon: 1, Lat: 51, Source: placeSourceCoordinates,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !m.workspace.modified(m.destination(), ref) {
		t.Fatal("stop edit was not detected")
	}
}

func TestWorkspaceManagementKeepsStaleWorkingCheckpoint(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	cp, err := m.saveWorkspaceCheckpoint("Original", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := localWorkspaceStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.rename(cp, "Other viewer"); err != nil {
		t.Fatal(err)
	}
	openTestWorkspaceLibrary(t, m)
	m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: 'n'})
	m.ui.search.SetValue("Renamed again")
	m.Update(m.manageSavedWorkspace()())
	if m.workspace.checkpoint != cp {
		t.Fatal("metadata operation made an older working copy target the newer revision")
	}
	if _, err := m.saveWorkspaceCheckpoint("Original", false); !errors.Is(err, errWorkspaceConflict) {
		t.Fatal("stale working copy overwrote external changes", err)
	}
}

func TestWorkspaceUnsavedSwitchRejectsInterveningEdits(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	cp, err := m.saveWorkspaceCheckpoint("Original", false)
	if err != nil {
		t.Fatal(err)
	}
	m.camera.Zoom++
	openTestWorkspaceLibrary(t, m)
	m.Update(m.beginSavedOpen(cp.path)())
	if m.savedWork.mode != "unsaved" {
		t.Fatal("missing prompt")
	}
	m.workspace.newJourney()
	savedKey(m, 'd')
	if m.savedWork.mode != "open" || len(m.workspace.items()) < 2 {
		t.Fatal("discard used a stale switch confirmation")
	}
	m.rebuild()
}

func TestWorkspaceManagementLongNamesRemainScrollable(t *testing.T) {
	m := themeViewer(t, 70, 24, true)
	name := strings.Repeat("界", 256)
	cp, err := m.saveWorkspaceCheckpoint(name, false)
	if err != nil {
		t.Fatal(err)
	}
	openTestWorkspaceLibrary(t, m)
	m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: tea.KeyDelete})
	_, body, _ := m.savedWorkspaceContent(45, 8)
	if !strings.Contains(body, cp.manifest.ID) {
		t.Fatal("long name obscures checkpoint identity")
	}
	savedKey(m, tea.KeyEnd)
	_, body, _ = m.savedWorkspaceContent(45, 8)
	if !strings.Contains(body, "working copy") {
		t.Fatal("long delete confirmation cannot be scrolled", body)
	}
}

func TestWorkspaceDeleteRetainsPartialContentsAndDraftAccess(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	m.workspace.clear()
	m.planner.ref = m.workspace.newJourney()
	source := importFile(t, "missing.gpx", []byte(`<gpx><wpt lat="51" lon="0"/></gpx>`))
	openWorkspaceFile(t, &m.workspace, source)
	openWorkspaceFile(t, &m.workspace, "../fixtures/example.gpx")
	cp, err := m.saveWorkspaceCheckpoint("Partial", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	candidate, camera, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	m.installSavedWorkspace(candidate, camera)
	if m.workspace.modified(m.destination(), m.planner.ref) {
		t.Fatal("missing imports made freshly restored work dirty")
	}
	rows := len(m.workspaceRows())
	ref := m.planner.ref
	preferences := filepath.Join(filepath.Dir(cp.path), "..", "settings.json")
	favourites := filepath.Join(filepath.Dir(cp.path), "..", "favourites.json")
	for _, path := range []string{preferences, favourites} {
		if err := os.WriteFile(path, []byte("keep me"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	openTestWorkspaceLibrary(t, m)
	m.updateWorkspaceLibrary(tea.KeyPressMsg{Code: tea.KeyDelete})
	m.Update(m.manageSavedWorkspace()())
	if len(m.workspaceRows()) != rows {
		t.Fatal("deletion hid incomplete journey drafts")
	}
	if _, err := m.workspace.journey(ref); err != nil {
		t.Fatal("deletion lost editable draft", err)
	}
	copy, err := m.saveWorkspaceCheckpoint("Retained contents", false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copy.manifest.Order, cp.manifest.Order) || len(copy.manifest.Imports) != 2 {
		t.Fatal("deletion or new Save lost missing imports or document order")
	}
	for _, path := range []string{preferences, favourites} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "keep me" {
			t.Fatal("deletion affected preferences or favourites", err)
		}
	}
	if _, err := os.Stat("../fixtures/example.gpx"); err != nil {
		t.Fatal("deletion removed original import", err)
	}
}
