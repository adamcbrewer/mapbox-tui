package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/filepicker"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type savedWorkspaceUI struct {
	generation  string
	revision    uint64
	entries     []workspaceListing
	selected    int
	mode        string
	request     uint64
	candidate   *Workspace
	camera      Camera
	repairID    string
	busy        bool
	repairError string
}

type savedWorkspaceReady struct {
	request   uint64
	candidate *Workspace
	camera    Camera
	err       error
}

type savedImportReady struct {
	request   uint64
	reference savedImport
	document  *Document
	err       error
}

func (m *viewer) openSavedWorkspaceDialog(mode string) tea.Cmd {
	m.openDialog(dialogSavedWorkspace)
	m.savedWork.request++
	m.savedWork.mode, m.savedWork.busy = mode, false
	m.savedWork.candidate = nil
	m.savedWork.repairError = ""
	m.ui.search.CharLimit = 4096
	m.ui.search.Placeholder = "Workspace JSON path"
	m.ui.search.SetValue("")
	m.ui.inputID = rand.Text()
	if mode == "save" || mode == "save-as" {
		m.ui.search.CharLimit = 256
		m.ui.search.Placeholder = "Workspace name"
		if m.workspace.checkpoint != nil {
			m.ui.search.SetValue(m.workspace.checkpoint.manifest.Name)
		}
	}
	if mode == "open" {
		return m.loadWorkspaceLibrary()
	}
	if mode == "repair" {
		return nil
	}
	return m.ui.search.Focus()
}

func (m *viewer) browseSavedWorkspaces() tea.Cmd {
	store, err := localWorkspaceStore()
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	picker := filepicker.New()
	picker.CurrentDirectory = existingParentDirectory(store.directory)
	picker.AllowedTypes = []string{".json"}
	picker.AutoHeight = false
	picker.SetHeight(max(1, min(17, m.height-10)-3))
	picker.ShowPermissions, picker.ShowSize = false, false
	m.ui.picker = picker
	m.stylePicker()
	m.savedWork.mode = "browse"
	m.ui.search.Blur()
	return picker.Init()
}

func (m *viewer) beginSavedOpen(path string) tea.Cmd {
	if strings.TrimSpace(path) == "" {
		m.ui.notice = "Enter a workspace JSON path, or Tab to choose a file."
		return nil
	}
	m.savedWork.request++
	request := m.savedWork.request
	m.savedWork.generation, m.savedWork.revision = m.workspace.liveIdentity(), m.workspace.liveRevision
	m.savedWork.busy = true
	m.savedWork.candidate = nil
	m.ui.notice = "Opening local content..."
	return func() tea.Msg {
		candidate, camera, err := prepareWorkspace(path)
		return savedWorkspaceReady{request: request, candidate: candidate, camera: camera, err: err}
	}
}

func (m *viewer) acceptSavedWorkspace(result savedWorkspaceReady) tea.Cmd {
	if result.request != m.savedWork.request || m.ui.dialog != dialogSavedWorkspace {
		return nil
	}
	m.savedWork.busy = false
	m.dirty = true
	if result.err != nil {
		m.ui.notice = result.err.Error()
		return nil
	}
	if !m.savedOpenCurrent() {
		return nil
	}
	m.savedWork.candidate, m.savedWork.camera = result.candidate, result.camera
	if m.savedWork.mode == "library" {
		return m.applySavedWorkspace()
	}
	m.savedWork.mode = "confirm-open"
	m.ui.page.GotoTop()
	m.ui.notice = ""
	m.ui.search.Blur()
	return nil
}

func (m *viewer) savedOpenCurrent() bool {
	if m.savedWork.generation == m.workspace.liveIdentity() && m.savedWork.revision == m.workspace.liveRevision {
		return true
	}
	m.savedWork.candidate = nil
	m.ui.notice = "Working copy changed while opening. Open the workspace again to replace it."
	return false
}

func (m *viewer) applySavedWorkspace() tea.Cmd {
	candidate := m.savedWork.candidate
	if candidate == nil || !m.savedOpenCurrent() {
		return nil
	}
	cmd := m.installSavedWorkspace(candidate, m.savedWork.camera)
	if len(m.workspace.unresolved) > 0 {
		return tea.Batch(m.openSavedWorkspaceDialog("repair"), cmd)
	}
	return cmd
}

// installSavedWorkspace shares replacement semantics without opening confirmation/repair dialogs.
func (m *viewer) installSavedWorkspace(candidate *Workspace, camera Camera) tea.Cmd {
	m.closeDialog()
	m.cancelPlaceSearch()
	m.savedWork.request++
	m.loadID++
	m.loading, m.pendingPaths = false, nil
	m.startupCameraPending = false
	m.args.Journey = nil
	m.workspace.replace(candidate)
	m.planner = journeyUI{ref: m.workspace.restoredPlannerRef(), searchID: m.planner.searchID}
	m.searchLocation, m.searchPosition, m.inspected = nil, nil, nil
	m.searchName, m.searchCredit, m.path = "", "", ""
	m.initialised = true
	m.motion.Active = false
	m.setFollowing(false)
	camera.Width, camera.Height, camera.Aspect = m.camera.Width, m.camera.Height, m.camera.Aspect
	m.camera = camera
	m.viewRestoreID++
	m.viewChanged = false
	m.lastTick, m.lastMotionTick = time.Now(), time.Now()
	m.savedWork.candidate = nil
	m.selectionChanged()
	routing := m.routePendingJourneys()
	return tea.Batch(m.fetch(), routing)
}

func (m *viewer) saveWorkspaceCheckpoint(name string, saveAs bool) (*workspaceCheckpoint, error) {
	store, err := localWorkspaceStore()
	var checkpoint *workspaceCheckpoint
	if err == nil {
		var snapshot savedWorkspace
		snapshot, err = m.workspace.snapshot(name, m.destination(), saveAs)
		if err == nil {
			if _, err := m.workspace.journey(m.planner.ref); err == nil {
				entry, _, _ := m.workspace.lookup(m.planner.ref)
				snapshot.Planner = entry.itemIDs[m.planner.ref.ItemID]
			}
			expected := m.workspace.checkpoint
			if saveAs {
				expected = nil
			}
			checkpoint, err = store.save(snapshot, expected)
		}
	}
	if err != nil {
		return nil, err
	}
	m.workspace.checkpoint = checkpoint
	m.workspace.edited()
	return checkpoint, nil
}

func (m *viewer) saveWorkspaceFromDialog() tea.Cmd {
	name := strings.TrimSpace(m.ui.search.Value())
	checkpoint, err := m.saveWorkspaceCheckpoint(name, m.savedWork.mode == "save-as")
	if err != nil {
		m.ui.notice = err.Error()
		if errors.Is(err, errWorkspaceConflict) {
			m.savedWork.mode = "conflict"
			m.ui.search.Blur()
			m.ui.notice = "The checkpoint changed; your working copy is safe."
		}
		return nil
	}
	m.savedWork.mode = "saved"
	m.ui.notice = "Workspace saved: " + checkpoint.manifest.Name
	m.ui.search.Blur()
	m.ui.page.GotoTop()
	return nil
}

func (m *viewer) beginLocateImport() tea.Cmd {
	index := slices.IndexFunc(m.workspace.unresolved, func(entry savedImport) bool {
		return entry.ID == m.savedWork.repairID
	})
	if index < 0 {
		m.ui.notice = "Reference no longer exists."
		return nil
	}
	reference := m.workspace.unresolved[index]
	path, err := replacementImportPath(m.ui.search.Value())
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	reference.Path = path
	m.savedWork.repairError = ""
	m.ui.notice = ""
	m.savedWork.request++
	request := m.savedWork.request
	m.savedWork.busy = true
	return func() tea.Msg {
		document, err := loadSavedImport(reference)
		return savedImportReady{request: request, reference: reference, document: document, err: err}
	}
}

func (m *viewer) acceptSavedImport(result savedImportReady) tea.Cmd {
	if result.request != m.savedWork.request || m.ui.dialog != dialogSavedWorkspace {
		return nil
	}
	m.savedWork.busy = false
	m.dirty = true
	err := result.err
	if err == nil {
		err = m.workspace.completeImportRepair(result.reference.ID, result.document)
	}
	if err != nil {
		m.savedWork.repairError = err.Error()
		m.savedWork.mode = "locate"
		return m.ui.search.Focus()
	}
	m.selectionChanged()
	return m.openSavedWorkspaceDialog("repair")
}

func (m *viewer) updateSavedWorkspaceDialog(msg tea.Msg) tea.Cmd {
	if pressed, ok := msg.(tea.KeyPressMsg); ok {
		key := pressed.String()
		if key == "esc" {
			if m.savedWork.mode == "locate-browse" {
				m.savedWork.mode = "locate"
				return m.ui.search.Focus()
			}
			m.closeDialog()
			return nil
		}
		if m.savedWork.mode == "library" {
			return m.updateWorkspaceLibrary(pressed)
		}
		if m.savedWork.busy {
			return nil
		}
		switch m.savedWork.mode {
		case "save", "save-as":
			if key == "enter" {
				return m.saveWorkspaceFromDialog()
			}
		case "saved", "confirm-open":
			if key == "enter" {
				if m.savedWork.mode == "confirm-open" {
					return m.applySavedWorkspace()
				}
				m.closeDialog()
				return nil
			}
			switch key {
			case "home":
				m.ui.page.GotoTop()
				return nil
			case "end":
				m.ui.page.GotoBottom()
				return nil
			}
			var cmd tea.Cmd
			m.ui.page, cmd = m.ui.page.Update(msg)
			return cmd
		case "open":
			if key == "tab" {
				return m.browseSavedWorkspaces()
			}
			if key == "enter" {
				return m.beginSavedOpen(m.ui.search.Value())
			}
		case "browse":
			if key == "tab" {
				m.savedWork.mode = "open"
				return m.ui.search.Focus()
			}
		case "conflict":
			switch key {
			case "s":
				m.savedWork.mode = "save-as"
				m.ui.notice = ""
				return m.ui.search.Focus()
			case "r":
				return m.beginSavedOpen(m.workspace.checkpoint.path)
			}
			return nil
		case "remove-reference":
			if key == "enter" {
				if err := m.workspace.removeUnresolved(m.savedWork.repairID); err != nil {
					m.ui.notice = err.Error()
					return nil
				}
				return m.openSavedWorkspaceDialog("repair")
			}
			return nil
		case "repair":
			count := len(m.workspace.unresolved)
			if count == 0 {
				m.closeDialog()
				return nil
			}
			m.ui.selected = min(m.ui.selected, count-1)
			switch key {
			case "up", "k":
				m.ui.selected = menuStep(m.ui.selected, -1, count)
			case "down", "j":
				m.ui.selected = menuStep(m.ui.selected, 1, count)
			case "enter":
				entry := m.workspace.unresolved[m.ui.selected]
				m.savedWork.repairID, m.savedWork.mode = entry.ID, "locate"
				m.ui.search.SetValue(entry.Path)
				m.ui.search.CursorEnd()
				m.savedWork.repairError = ""
				m.ui.search.Placeholder = "Replacement file path"
				return m.ui.search.Focus()
			case "delete", "backspace":
				m.savedWork.repairID = m.workspace.unresolved[m.ui.selected].ID
				m.savedWork.mode = "remove-reference"
			}
			return nil
		case "locate-browse":
			if key == "tab" {
				m.savedWork.mode = "locate"
				return m.ui.search.Focus()
			}
		case "locate":
			if key == "tab" {
				return m.browseReplacementImport()
			}
			if key == "ctrl+u" {
				m.ui.search.SetValue("")
				m.savedWork.repairError = ""
				return nil
			}
			if key == "enter" {
				return m.beginLocateImport()
			}
		}
	}
	if m.savedWork.mode == "library" {
		var cmd tea.Cmd
		m.ui.page, cmd = m.ui.page.Update(msg)
		return cmd
	}
	if m.savedWork.mode == "confirm-open" || m.savedWork.mode == "saved" {
		var cmd tea.Cmd
		m.ui.page, cmd = m.ui.page.Update(msg)
		return cmd
	}
	if m.savedWork.mode == "browse" || m.savedWork.mode == "locate-browse" {
		var cmd tea.Cmd
		m.ui.picker, cmd = m.ui.picker.Update(msg)
		if selected, path := m.ui.picker.DidSelectFile(msg); selected {
			if m.savedWork.mode == "locate-browse" {
				m.savedWork.mode = "locate"
				m.ui.search.SetValue(path)
				m.ui.search.CursorEnd()
				m.ui.search.Focus()
				return m.beginLocateImport()
			}
			return m.beginSavedOpen(path)
		}
		return cmd
	}
	var cmd tea.Cmd
	m.ui.search, cmd = m.ui.search.Update(msg)
	return m.clipboardCommand(msg, cmd)
}

func (m *viewer) savedWorkspaceContent(width, height int) (string, string, string) {
	m.ui.search.SetWidth(max(1, width-2))
	m.ui.search.SetCursor(m.ui.search.Position())
	input := m.ui.search.View()
	switch m.savedWork.mode {
	case "library":
		return m.workspaceLibraryContent(width, height)
	case "save", "save-as":
		title := "Save workspace"
		if m.savedWork.mode == "save-as" {
			title = "Save workspace as"
		}
		body := input + "\n\nOriginal import files must remain available." +
			"\nStops and view are saved; road routes, maps and traffic aren't." +
			"\nRoad routes recalculate when Mapbox is available."
		if len(m.workspace.unresolved) > 0 {
			body += "\nMissing file references will be kept."
		}
		return title, ansi.Hardwrap(body, width, true), "enter save • esc cancel"
	case "saved":
		path := cleanPlaceText(m.workspace.checkpoint.path)
		body := path + "\n\nUse this path to reopen the workspace."
		m.ui.page.SetWidth(max(1, width))
		m.ui.page.SetHeight(max(1, height))
		m.ui.page.SetContent(ansi.Hardwrap(body, max(1, width), true))
		return "Workspace saved", m.ui.page.View(), "↑/↓ scroll • enter close • esc close"
	case "open":
		body := input + "\n\nEnter a JSON path or choose a saved file."
		return "Open saved workspace", body, "enter load • tab files • esc cancel"
	case "browse":
		return "Open saved workspace", m.ui.picker.View(), "enter choose • tab path • esc cancel"
	case "conflict":
		body := "Save As keeps your work in a separate checkpoint." +
			"\nReload prepares the on-disk version for confirmation."
		return "Workspace changed", ansi.Hardwrap(body, width, true), "s save as • r reload • esc cancel"
	case "confirm-open":
		m.ui.page.SetWidth(max(1, width))
		m.ui.page.SetHeight(max(1, height))
		m.ui.page.SetContent(m.savedWorkspaceSummary(width))
		return "Open workspace?", m.ui.page.View(), "↑/↓ scroll • enter open • esc keep current"
	case "repair":
		if len(m.workspace.unresolved) == 0 {
			return "Workspace files", "All referenced files are available.", "esc close"
		}
		count := len(m.workspace.unresolved)
		m.ui.selected = min(m.ui.selected, count-1)
		entry := m.workspace.unresolved[m.ui.selected]
		filename := fmt.Sprintf("%d/%d %s", m.ui.selected+1, count, cleanPlaceText(filepath.Base(entry.Path)))
		problem := ansi.Hardwrap(cleanPlaceText(entry.problem), width, true)
		body := ansi.Hardwrap(filename, width, true) + "\n\n" + m.styles.dialogAccent.Render(problem)
		hints := "↑/↓ select • enter locate • del remove • esc skip for now"
		return "Missing or changed files", body, hints
	case "locate-browse":
		m.ui.picker.SetHeight(max(1, height))
		return "Locate original import", m.ui.picker.View(), "enter choose • tab path • esc back"
	case "locate":
		body := input + "\n\nChoose an identical copy to restore item state." +
			"\nFor changed content, remove the reference and add the new file."
		if m.savedWork.repairError != "" {
			body += "\n\n" + m.styles.dialogAccent.Render(m.savedWork.repairError)
		}
		return "Locate original import", ansi.Hardwrap(body, width, true), "enter locate • tab files • ctrl+u replace path • esc skip"
	case "remove-reference":
		name := "this missing import"
		for _, entry := range m.workspace.unresolved {
			if entry.ID == m.savedWork.repairID {
				name = cleanPlaceText(filepath.Base(entry.Path))
			}
		}
		body := "Remove the reference to " + name + "?\nThe original file stays untouched." +
			"\nOnly an explicit Save updates the named checkpoint."
		return "Remove file reference?", ansi.Hardwrap(body, width, true), "enter remove • esc cancel"
	}
	return "Workspace", "", "esc close"
}

// Explicit launch camera flags override the checkpoint for this launch only.
// Global last_view is intentionally absent from this precedence chain.
func workspaceLaunchCamera(camera Camera, args Args) Camera {
	if args.Lon != nil && args.Lat != nil {
		camera.Center = project(*args.Lon, *args.Lat)
	}
	if args.Zoom != nil {
		camera.Zoom = *args.Zoom
	}
	if args.Bearing != nil {
		camera.Bearing = *args.Bearing
	}
	if args.Aspect != nil {
		camera.Aspect = *args.Aspect
	}
	return camera
}

func (m *viewer) savedWorkspaceSummary(width int) string {
	candidate := m.savedWork.candidate
	preview := previewWorkspace(candidate.checkpoint, candidate.unresolved, true)
	return preview.confirmation(width, m.settings.ASCII)
}

// Keep the filename and nearest parent directories when a path exceeds the row.
func abbreviatedWorkspacePath(path string, width int, ascii bool) string {
	path = cleanPlaceText(path)
	length := ansi.StringWidth(path)
	if length <= width {
		return path
	}
	prefix := "…"
	if ascii {
		prefix = "..."
	}
	if width <= ansi.StringWidth(prefix) {
		return ansi.Truncate(path, width, "")
	}
	cut := length - width + ansi.StringWidth(prefix)
	shortened := ansi.TruncateLeft(path, cut, prefix)
	// A cut inside a double-width grapheme can retain one extra cell.
	for ansi.StringWidth(shortened) > width {
		cut++
		shortened = ansi.TruncateLeft(path, cut, prefix)
	}
	return shortened
}

func replacementImportPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("choose a file or enter its path")
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	return filepath.Abs(value)
}

func (m *viewer) browseReplacementImport() tea.Cmd {
	path, err := replacementImportPath(m.ui.search.Value())
	if err != nil {
		path, _ = os.Getwd()
	}
	picker := filepicker.New()
	picker.CurrentDirectory = existingParentDirectory(path)
	picker.AllowedTypes = []string{".gpx", ".fit", ".tcx", ".kml", ".GPX", ".FIT", ".TCX", ".KML"}
	picker.AutoHeight = false
	picker.SetHeight(max(1, min(17, m.height-10)-3))
	picker.ShowPermissions, picker.ShowSize = false, false
	m.ui.picker = picker
	m.stylePicker()
	m.savedWork.mode = "locate-browse"
	m.ui.search.Blur()
	return picker.Init()
}

// A fresh workspace library or moved import may have no parent directory yet.
func existingParentDirectory(path string) string {
	for {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}
