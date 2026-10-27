package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestActivitySourceFailurePersistsUntilRecovery(t *testing.T) {
	for _, data := range []string{"previous", "partial", "none", "empty previous"} {
		t.Run(data, func(t *testing.T) {
			m := themeViewer(t, 120, 40, false)
			now := time.Now()
			snapshot := sourceSnapshot{}
			if data == "previous" {
				snapshot.Features = []Feature{{Label: "retained road"}}
			}
			if data == "empty previous" {
				snapshot.FetchedAt = now
			}
			m.sources.set("traffic", sourceFunc(func(context.Context, Camera) (sourceSnapshot, error) {
				return snapshot, nil
			}), true)
			t.Cleanup(m.sources.close)
			request := oneSourceRequest(t, m.sources.poll(m.ctx, m.camera, now, false))
			if row := ansi.Strip(m.activityRow()); !strings.Contains(row, "Loading traffic") {
				t.Fatal("initial load was not described", row)
			}
			m.sources.accept(request.run(), m.camera, now)
			state := m.sources.states[0]
			state.snapshot.RefreshAt = now.Add(time.Second)
			request = oneSourceRequest(t, m.sources.poll(m.ctx, m.camera, now.Add(time.Second), false))
			if data == "previous" && !strings.Contains(ansi.Strip(m.activityRow()), "Updating traffic") {
				t.Fatal("background refresh looked like an initial load")
			}
			snapshot = sourceSnapshot{Failed: true, Status: "Network failed"}
			if data == "partial" {
				snapshot.Features = []Feature{{Label: "partial road"}}
			}
			m.sources.accept(request.run(), m.camera, now.Add(time.Second))
			want := "no data available"
			if data == "previous" {
				want = "previous data shown"
			}
			if data == "partial" {
				want = "partial data shown"
			}
			before := ansi.Strip(m.activityRow())
			if !strings.Contains(before, want) || !strings.Contains(before, "alt+v details") {
				t.Fatal("failure lost its data state or recovery hint", before)
			}
			request = oneSourceRequest(t, m.sources.poll(m.ctx, m.camera, now.Add(11*time.Second), false))
			if !strings.Contains(ansi.Strip(m.activityRow()), want) {
				t.Fatal("retry hid the last failure")
			}
			if details := strings.Join(m.activityDetails(), " "); !strings.Contains(details, "retrying") {
				t.Fatal("overlapping retry is not discoverable", details)
			}
			m.confirmActivity("Workspace saved")
			if strings.Contains(ansi.Strip(m.activityRow()), "Workspace saved") {
				t.Fatal("success hid a source failure")
			}
			snapshot = sourceSnapshot{Features: []Feature{{Label: "current road"}}}
			m.sources.accept(request.run(), m.camera, now.Add(11*time.Second))
			if strings.Contains(ansi.Strip(m.activityRow()), "failed") {
				t.Fatal("successful retry retained a failure warning")
			}
		})
	}
}

func nextImportMessage(t *testing.T, cmd tea.Cmd) loaded {
	t.Helper()
	switch msg := cmd().(type) {
	case loaded:
		return msg
	case tea.BatchMsg:
		for _, next := range msg {
			if msg, ok := next().(loaded); ok {
				return msg
			}
		}
	}
	t.Fatal("command did not return an import result")
	return loaded{}
}

func TestActivityImportsReportQueueAndKeepSuccessfulFilesOnFailure(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	m.pendingPaths = []string{"../fixtures/waypoints.gpx", "missing-last-file.fit"}
	cmd := m.load("../fixtures/country-ride.gpx")
	if row := ansi.Strip(m.activityRow()); !strings.Contains(row, "Opening 1 of 3") ||
		!strings.Contains(row, "country-ride.gpx") || !strings.Contains(row, "0 completed") {
		t.Fatal(row)
	}
	first := nextImportMessage(t, cmd)
	_, cmd = m.Update(first)
	if row := ansi.Strip(m.activityRow()); !strings.Contains(row, "Opening 2 of 3") ||
		!strings.Contains(row, "waypoints.gpx") || !strings.Contains(row, "1 completed") {
		t.Fatal(row)
	}
	m.Update(first)
	if m.imports.completed != 1 {
		t.Fatal("stale result advanced queue progress")
	}
	_, cmd = m.Update(nextImportMessage(t, cmd))
	before := len(m.workspace.documents)
	m.Update(nextImportMessage(t, cmd))
	if m.loading || len(m.pendingPaths) > 0 || len(m.workspace.documents) != before || m.imports.completed != 2 {
		t.Fatal("failed import discarded completed files or left the queue running")
	}
	for _, want := range []string{"missing-last-file.fit", "2 of 3 files opened", "queue stopped"} {
		if !strings.Contains(m.ui.notice, want) {
			t.Fatal("failure did not identify the file and queue outcome", m.ui.notice)
		}
	}
}

func TestActivityImportConfirmationExpiresAndBackgroundWorkIsQuiet(t *testing.T) {
	m := themeViewer(t, 120, 40, false)
	m.pendingPaths = []string{"../fixtures/waypoints.gpx"}
	runLoadCommand(m, m.load("../fixtures/country-ride.gpx"))
	if row := ansi.Strip(m.activityRow()); !strings.Contains(row, "✓ 2 files opened") {
		t.Fatal(row)
	}
	m.expireActivity(m.confirmation.expires)
	if strings.TrimSpace(ansi.Strip(m.activityRow())) != "" {
		t.Fatal("completed imports kept activity visible")
	}
	state := &sourceState{name: "basemap", enabled: true}
	m.sources.states = []*sourceState{state}
	state.snapshot = sourceSnapshot{Features: []Feature{{Label: "road"}}}
	if strings.TrimSpace(ansi.Strip(m.activityRow())) != "" {
		t.Fatal("routine refresh announced success")
	}
}

func TestActivityFeedbackFitsWithoutMovingMapOrFooter(t *testing.T) {
	for _, width := range []int{30, 39, 60, 70, 109, 110, 120} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/ascii=%v", width, ascii), func(t *testing.T) {
				m := themeViewer(t, width, 24, ascii)
				m.openJourney()
				m.rebuild()
				camera, footer := m.camera, strings.Join(m.contextFooter(), "\n")
				m.loading = true
				m.planner.searchBusy = true
				m.imports = importActivity{path: strings.Repeat("長い filename ", 15) + ".gpx", total: 4, completed: 1}
				m.sources.states = []*sourceState{{name: "traffic", enabled: true, retained: true,
					snapshot: sourceSnapshot{Failed: true, Features: []Feature{{Label: "old"}}}}}
				for _, failure := range []bool{true, false} {
					m.sources.states[0].snapshot.Failed = failure
					m.rebuild()
					if m.camera != camera || strings.Join(m.contextFooter(), "\n") != footer {
						t.Fatal("activity changed map dimensions or persistent context")
					}
					if lipgloss.Width(m.content) > width || lipgloss.Height(m.content) != m.height {
						t.Fatal("activity overflowed the terminal")
					}
					row := ansi.Strip(m.activityRow())
					retained := strings.Contains(row, "old data") || strings.Contains(row, "previous data shown")
					if failure && (!retained || !strings.Contains(row, "v")) {
						t.Fatal("compact failure lost retained data or next step", row)
					}
					if !failure && (!strings.Contains(row, "Opening 2/") && !strings.Contains(row, "Opening 2 of")) {
						t.Fatal("filename displaced queue position", row)
					}
					if !failure && !strings.Contains(row, "1 done") && !strings.Contains(row, "1 completed") {
						t.Fatal("overlapping work displaced the completed-file count", row)
					}
					if ascii && strings.ContainsAny(row, "…⠋█░✓") {
						t.Fatal("ASCII activity retained Unicode decorations", row)
					}
					rows := strings.Split(ansi.Strip(m.content), "\n")
					if rows[m.camera.Height] != ansi.Strip(m.activityRow()) {
						t.Fatal("Journey panel covered the activity row", rows[m.camera.Height])
					}
				}
			})
		}
	}
}

func TestActivityRowKeepsCompactPlannerBorderIntact(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		t.Run(fmt.Sprintf("ascii=%v", ascii), func(t *testing.T) {
			m := themeViewer(t, 30, 12, ascii)
			m.settings.Elevation = elevationHide
			m.openJourney()
			m.rebuild()
			panel := strings.Split(ansi.Strip(m.journeyPanel()), "\n")
			border := m.boxBorder()
			last := panel[len(panel)-1]
			if !strings.HasPrefix(last, border.BottomLeft) || !strings.HasSuffix(last, border.BottomRight) {
				t.Fatal("compact planner clipped its bottom border", panel)
			}
		})
	}
}

func TestActivitySpinnersRespectMotionAndASCIIChanges(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	m.openDialog(dialogSearch)
	m.planner.searchBusy = true
	if m.startSpinner() != nil || m.busyMarker() != "*" {
		t.Fatal("reduced motion started an activity animation")
	}
	for _, height := range []int{1, 2, 5} {
		body, hints := m.placeSearchContent(30, height)
		if !strings.Contains(ansi.Strip(body+hints), "* Looking up location") {
			t.Fatal("short search dialog hid activity", height, body, hints)
		}
	}
	m.settings.NoAnimation = false
	cmd := m.startSpinner()
	if cmd == nil {
		t.Fatal("search alone could not start a spinner")
	}
	for range 7 {
		_, cmd = m.Update(cmd())
	}
	m.settings.ASCII = true
	m.configureSpinner()
	if marker := ansi.Strip(m.busyMarker()); !strings.Contains("|/-\\", marker) || marker == "" {
		t.Fatal("changing character mode left an invalid spinner frame", marker)
	}
	m.settings.NoAnimation = true
	notice := controlNotification{action: "load", state: "pending"}
	a := notice.label(nil, time.Unix(1, 0), true, false)
	b := notice.label(nil, time.Unix(2, 0), true, false)
	if a != b || !strings.HasPrefix(a, "* Remote:") {
		t.Fatal("remote busy feedback ignored reduced motion", a, b)
	}
}

func TestActivityWorkspaceLoadingAndFailuresKeepRecoveryVisible(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	m.openSavedWorkspaceDialog("open")
	_, body, hints := m.savedWorkspaceContent(30, 2)
	if !strings.Contains(ansi.Strip(body), "* Reading saved workspaces") || hints != "esc cancel" {
		t.Fatal(body, hints)
	}
	m.acceptWorkspaceLibrary(workspaceLibraryReady{request: m.savedWork.request, err: errors.New("unreadable folder")})
	_, body, _ = m.savedWorkspaceContent(30, 8)
	if strings.Contains(body, "No saved workspaces yet") || !strings.Contains(body, "retry") {
		t.Fatal("failed listing looked like an empty library", body)
	}
	m.beginSavedOpen("saved-workspace.json")
	_, body, hints = m.savedWorkspaceContent(30, 2)
	if !strings.Contains(ansi.Strip(body), "* Opening workspace") || hints != "esc cancel" {
		t.Fatal(body, hints)
	}
	m.closeDialog()
	m.acceptSavedWorkspace(savedWorkspaceReady{request: m.savedWork.request - 1, err: errors.New("late failure")})
	if m.ui.notice != "" || m.ui.dialog != dialogNone {
		t.Fatal("cancelled workspace load changed current feedback")
	}
}

func TestActivityRouteFeedbackAppearsInCompactPlanner(t *testing.T) {
	m := journeyViewer(t)
	m.openJourney()
	enterJourneyCoordinate(m, 0, "51.3, -2.5")
	enterJourneyCoordinate(m, 1, "51.4, -2.6")
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		t.Fatal(err)
	}
	j.Mode, j.Busy = journeyCycling, true
	_, body, _ := m.journeyDialogContent(40, 2)
	if !strings.Contains(ansi.Strip(body), "* Calculating cycling route") ||
		!strings.Contains(ansi.Strip(m.activityRow()), "Calculating cycling route") {
		t.Fatal("routing lacked feedback beside the planner and on the map", body)
	}
}

func TestActivityImportKeepsCommandPickerSelection(t *testing.T) {
	m := journeyViewer(t)
	m.openDialog(dialogCommands)
	for i, command := range m.commands() {
		if command.action == "motion" {
			m.ui.selected = i
		}
	}
	before := m.ui.selected
	document, err := loadDocument("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m.Update(loaded{id: m.loadID, path: document.Filename, document: document})
	if m.ui.dialog != dialogCommands || m.commands()[m.ui.selected].action != "motion" || m.ui.selected == before {
		t.Fatal("import changed the action under the user's selection", m.ui.selected, before)
	}
}

func TestActivitySourceDetailsShortcutPreservesEachView(t *testing.T) {
	for _, kind := range []dialogKind{dialogNone, dialogWorkspace, dialogJourney, dialogSearch} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			m := themeViewer(t, 70, 24, false)
			switch kind {
			case dialogJourney:
				m.openJourney()
			case dialogWorkspace, dialogSearch:
				m.openDialog(kind)
			}
			m.ui.search.SetValue("unfinished place")
			selection := m.workspace.selection
			m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
			if m.ui.dialog != dialogCredits {
				t.Fatal("advertised source details shortcut was captured by another view")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.ui.dialog != kind || m.workspace.selection != selection {
				t.Fatal("source details lost the original view or selection")
			}
			if kind == dialogSearch && m.ui.search.Value() != "unfinished place" {
				t.Fatal("source details lost search input")
			}
		})
	}
}

func TestActivityCommandMarkerKeepsCompactLabelsReadable(t *testing.T) {
	m := themeViewer(t, 30, 12, false)
	m.openDialog(dialogCommands)
	m.ui.search.SetValue("search")
	_, compact, _ := m.dialogContent(22, 2)
	if !strings.Contains(ansi.Strip(compact), "> Quick place search") {
		t.Fatal("shortcut displaced a command name in the compact picker", compact)
	}
	_, wide, _ := m.dialogContent(40, 2)
	if !strings.Contains(ansi.Strip(wide), "/") {
		t.Fatal("shortcut was hidden despite having room", wide)
	}
}
