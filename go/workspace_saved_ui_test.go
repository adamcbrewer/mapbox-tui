package main

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func savedKey(m *viewer, code rune) { m.Update(tea.KeyPressMsg{Code: code}) }

func TestSavedWorkspaceViewerSaveOpenAndCancel(t *testing.T) {
	for _, size := range []struct {
		width, height int
		ascii         bool
	}{{120, 40, false}, {70, 24, false}, {70, 24, true}} {
		t.Run(fmt.Sprintf("%dx%d-ascii-%t", size.width, size.height, size.ascii), func(t *testing.T) {
			m := themeViewer(t, size.width, size.height, size.ascii)
			m.openSavedWorkspaceDialog("save")
			m.ui.search.SetValue("Journey café")
			savedKey(m, tea.KeyEnter)
			if m.workspace.checkpoint == nil {
				t.Fatal("save failed", m.ui.notice)
			}
			path := m.workspace.checkpoint.path
			m.rebuild()
			if !strings.Contains(m.View().Content, "Workspace saved") {
				t.Fatal("no visible save result")
			}
			savedCamera := m.camera
			oldRef := m.workspace.selection.ref
			savedKey(m, tea.KeyEscape)
			m.openSavedWorkspaceDialog("open")
			m.savedWork.mode = "open"
			cmd := m.beginSavedOpen(path)
			m.Update(cmd())
			if m.savedWork.mode != "confirm-open" {
				t.Fatal("no open confirmation")
			}
			if m.workspace.selection.ref != oldRef {
				t.Fatal("open replaced workspace before confirmation")
			}
			savedKey(m, tea.KeyEscape)
			if m.workspace.selection.ref != oldRef {
				t.Fatal("cancel replaced workspace")
			}
			m.openSavedWorkspaceDialog("open")
			m.savedWork.mode = "open"
			m.Update(m.beginSavedOpen(path)())
			m.planner.searchID = 99
			m.loadID = 80
			m.planner.searchBusy = true
			savedKey(m, tea.KeyEnter)
			if math.Abs(m.camera.Center.X-savedCamera.Center.X) > 1e-12 || math.Abs(m.camera.Center.Y-savedCamera.Center.Y) > 1e-12 || m.camera.Zoom != savedCamera.Zoom {
				t.Fatal("lost saved view")
			}
			if m.workspace.selection.ref == oldRef || m.loadID <= 80 || m.planner.searchID <= 99 {
				t.Fatal("switch reused async identities")
			}
			before := len(m.workspace.documents)
			m.Update(loaded{id: 80, document: &Document{}})
			if len(m.workspace.documents) != before {
				t.Fatal("late import applied")
			}
			m.openSavedWorkspaceDialog("open")
			m.savedWork.mode = "open"
			late := m.beginSavedOpen(path)()
			savedKey(m, tea.KeyEscape)
			m.Update(late)
			if m.savedWork.candidate != nil {
				t.Fatal("cancelled open accepted")
			}
			m.openSavedWorkspaceDialog("open")
			m.savedWork.mode = "open"
			m.Update(m.beginSavedOpen(filepath.Join(t.TempDir(), "missing.json"))())
			if m.ui.notice == "" || len(m.workspace.documents) != before {
				t.Fatal("failed open changed working copy")
			}
		})
	}
}

func TestSavedWorkspaceStartupPrecedence(t *testing.T) {
	command, err := parseCLI([]string{"--workspace", "saved.json", "--zoom", "7"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	c := fit(nil, 80, 24, 2)
	c.Center = project(10, 20)
	actual := workspaceLaunchCamera(c, command.Args)
	if actual.Center != c.Center || actual.Zoom != 7 {
		t.Fatal("launch override lost saved centre")
	}
	for _, args := range [][]string{
		{"--workspace", "saved.json", "file.gpx"},
		{"--workspace", "saved.json", "--item-id", "1"},
		{"--workspace", "saved.json", "--start", "51,0", "--end", "52,0"},
	} {
		if _, err := parseCLI(args, io.Discard); err == nil {
			t.Fatal("accepted conflicting startup content", args)
		}
	}
}

func TestSavedWorkspaceRepairUIAndDraftAccess(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	source := importFile(t, "missing.gpx", []byte(`<gpx><wpt lat="51" lon="0"/></gpx>`))
	openWorkspaceFile(t, &m.workspace, source)
	m.workspace.newJourney()
	m.workspace.newJourney()
	store := workspaceStore{directory: t.TempDir()}
	cp := saveTestWorkspace(t, &m.workspace, store, false)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	m.openSavedWorkspaceDialog("open")
	m.savedWork.mode = "open"
	m.Update(m.beginSavedOpen(cp.path)())
	savedKey(m, tea.KeyEnter)
	if m.savedWork.mode != "repair" {
		t.Fatal("missing import was not explained")
	}
	if len(m.workspaceRows()) < 4 {
		t.Fatal("incomplete drafts are inaccessible")
	}
	savedKey(m, tea.KeyEscape)
	if len(m.workspace.unresolved) != 1 {
		t.Fatal("skip removed reference")
	}
	m.openSavedWorkspaceDialog("repair")
	savedKey(m, tea.KeyDelete)
	if m.savedWork.mode != "remove-reference" {
		t.Fatal("removal lacked confirmation")
	}
	savedKey(m, tea.KeyEnter)
	if len(m.workspace.unresolved) != 0 {
		t.Fatal("remove did not update working copy")
	}
	unchanged, err := readWorkspace(cp.path)
	if err != nil || len(unchanged.manifest.Imports) != 2 {
		t.Fatal("repair modified named checkpoint")
	}
}

func TestSavedWorkspaceViewerConflictOffersSaveAs(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	m.openSavedWorkspaceDialog("save")
	m.ui.search.SetValue("Original")
	savedKey(m, tea.KeyEnter)
	original := m.workspace.checkpoint
	if original == nil {
		t.Fatal(m.ui.notice)
	}
	if err := os.WriteFile(original.path, []byte("changed externally"), 0600); err != nil {
		t.Fatal(err)
	}
	m.openSavedWorkspaceDialog("save")
	savedKey(m, tea.KeyEnter)
	if m.savedWork.mode != "conflict" {
		t.Fatal("conflict choices not offered", m.ui.notice)
	}
	savedKey(m, 's')
	if m.savedWork.mode != "save-as" {
		t.Fatal("Save As unavailable")
	}
	savedKey(m, tea.KeyEnter)
	if m.workspace.checkpoint.manifest.ID == original.manifest.ID {
		t.Fatal("Save As overwrote original")
	}
	data, err := os.ReadFile(original.path)
	if err != nil || string(data) != "changed externally" {
		t.Fatal("conflict lost external edit")
	}
}

func TestSavedWorkspaceSummaryDatesPathsAndScrolling(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	created := time.Date(2026, 10, 17, 10, 15, 0, 0, time.UTC)
	modified := created.Add(25 * time.Hour)
	manifest := newSavedWorkspace("Weekend")
	manifest.Created, manifest.Modified = created, modified
	for i := range 20 {
		manifest.Imports = append(manifest.Imports, savedImport{
			ID:   fmt.Sprintf("import-%d", i),
			Path: fmt.Sprintf("/a/very/long/directory/with/many/parents/recordings/activity-%02d.gpx", i),
		})
	}
	candidate := &Workspace{checkpoint: &workspaceCheckpoint{manifest: manifest}}
	candidate.unresolved = []savedImport{manifest.Imports[0]}
	m.openSavedWorkspaceDialog("open")
	m.savedWork.mode = "open"
	m.acceptSavedWorkspace(savedWorkspaceReady{request: m.savedWork.request, candidate: candidate})
	body := m.savedWorkspaceSummary(45)
	for _, expected := range []string{
		"Created:  " + created.Local().Format("02 Jan 2006 15:04 MST"),
		"Modified: " + modified.Local().Format("02 Jan 2006 15:04 MST"),
		"Imports: 20", "activity-00.gpx (missing/changed)", "activity-19.gpx",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("summary missing %q: %s", expected, body)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if ansi.StringWidth(line) > 45 {
			t.Fatal("summary overflowed", line)
		}
	}
	_, visible, _ := m.savedWorkspaceContent(45, 8)
	if strings.Contains(visible, "activity-19.gpx") {
		t.Fatal("long list did not use viewport")
	}
	savedKey(m, tea.KeyEnd)
	_, visible, _ = m.savedWorkspaceContent(45, 8)
	if !strings.Contains(visible, "activity-19.gpx") {
		t.Fatal("last import inaccessible by scrolling", visible)
	}
	for _, ascii := range []bool{false, true} {
		path := abbreviatedWorkspacePath("/very/long/旅行/recordings/café.gpx", 24, ascii)
		if ansi.StringWidth(path) > 24 || !strings.HasSuffix(path, "café.gpx") {
			t.Fatal("abbreviation lost filename or overflowed", path)
		}
		if ascii && !strings.HasPrefix(path, "...") {
			t.Fatal("ASCII abbreviation missing", path)
		}
	}
}

func TestSavedWorkspaceRestoresPlannerAndRoutesOnce(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			m, requests := routingTestViewer(t)
			m.preferences.BillingAcceptedVersion = billingNoticeVersion
			original, _ := m.workspace.journey(m.planner.ref)
			for i := range original.Stops {
				original.Stops[i].Location.Source = placeSourceCoordinates
			}
			if fail {
				m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
					*requests++
					return nil, fmt.Errorf("connection unavailable")
				})
			}
			openWorkspaceFile(t, &m.workspace, "../fixtures/example.gpx")
			cp := saveTestWorkspace(t, &m.workspace, workspaceStore{directory: t.TempDir()}, false)
			candidate, camera, err := prepareWorkspace(cp.path)
			if err != nil {
				t.Fatal(err)
			}
			m.preferences.Offline = true
			m.savedWork.candidate, m.savedWork.camera = candidate, camera
			m.applySavedWorkspace()
			ref := m.planner.ref
			j, err := m.workspace.journey(ref)
			if err != nil || !j.hasCompleteLocations() {
				t.Fatal("planner did not restore saved stops", err)
			}
			m.openJourney()
			if m.planner.ref != ref {
				t.Fatal("opening planner created another draft")
			}
			if m.routePendingJourneys() != nil || *requests != 0 {
				t.Fatal("offline open requested a route")
			}
			before := m.camera
			m.preferences.Offline = false
			cmd := m.routePendingJourneys()
			if cmd == nil {
				t.Fatal("online restore did not schedule route")
			}
			var run func(tea.Cmd)
			run = func(cmd tea.Cmd) {
				if cmd == nil {
					return
				}
				msg := cmd()
				if batch, ok := msg.(tea.BatchMsg); ok {
					for _, child := range batch {
						run(child)
					}
					return
				}
				m.Update(msg)
			}
			run(cmd)
			if *requests != 1 || j.routeCurrent() == fail {
				t.Fatal("restored route result incorrect", *requests, j.Error)
			}
			if m.camera != before {
				t.Fatal("background route changed saved camera")
			}
			if m.routePendingJourneys() != nil {
				t.Fatal("restored route scheduled repeatedly")
			}
		})
	}
}

func TestSavedWorkspacePersistsPlannerWithImportedSelection(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	m.planner.ref = routedTestJourney(t, &m.workspace)
	want := m.planner.ref
	j, _ := m.workspace.journey(want)
	for i := range j.Stops {
		j.Stops[i].Location.Source = placeSourceCoordinates
	}
	m.openSavedWorkspaceDialog("save")
	m.ui.search.SetValue("Planner selection")
	m.saveWorkspaceFromDialog()
	cp := m.workspace.checkpoint
	if cp == nil || cp.manifest.Planner == "" {
		t.Fatal("planner identity not saved")
	}
	candidate, _, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	ref := candidate.restoredPlannerRef()
	entry, _, err := candidate.lookup(ref)
	if err != nil || entry.itemIDs[ref.ItemID] != cp.manifest.Planner {
		t.Fatal("planner identity not restored", want, err)
	}
}

func TestSavedWorkspaceConfirmationShowsFullPath(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	m.openSavedWorkspaceDialog("save")
	m.ui.search.SetValue("First workspace (v2)")
	m.savedWorkspaceContent(42, 12)
	m.saveWorkspaceFromDialog()
	if m.workspace.checkpoint == nil {
		t.Fatal(m.ui.notice)
	}
	// The full persisted path must survive wrapping, regardless of the old input cursor.
	path := m.workspace.checkpoint.path
	_, body, _ := m.savedWorkspaceContent(42, 12)
	compact := strings.ReplaceAll(ansi.Strip(body), "\n", "")
	if !strings.Contains(compact, path) {
		t.Fatalf("saved path clipped: %q in %q", path, body)
	}
	m.updateSavedWorkspaceDialog(tea.KeyPressMsg{Code: 'x'})
	if m.workspace.checkpoint.path != path {
		t.Fatal("confirmation edited the saved path")
	}
	m.workspace.checkpoint.path = "/" + strings.Repeat("long-directory/", 40) + "workspace.json"
	m.ui.page.GotoTop()
	_, body, _ = m.savedWorkspaceContent(30, 4)
	if strings.Contains(body, "workspace.json") {
		t.Fatal("expected scrollable long path")
	}
	savedKey(m, tea.KeyEnd)
	_, body, _ = m.savedWorkspaceContent(30, 4)
	if !strings.Contains(strings.ReplaceAll(ansi.Strip(body), "\n", ""), "workspace.json") {
		t.Fatal("full path inaccessible by scrolling", body)
	}
}
