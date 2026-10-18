package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestStartCleanDetachesCheckpointAndRejectsLateWork(t *testing.T) {
	m := themeViewer(t, 70, 24, true)
	m.openJourney()
	oldRef := m.planner.ref
	routeCtx, cancel := context.WithCancel(t.Context())
	j, _ := m.workspace.journey(oldRef)
	j.cancel = cancel
	m.openSavedWorkspaceDialog("save")
	m.ui.search.SetValue("Original")
	m.saveWorkspaceFromDialog()
	cp := m.workspace.checkpoint
	before, err := os.ReadFile(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	m.openSavedWorkspaceDialog("open")
	lateOpen := m.beginSavedOpen(cp.path)()
	oldLoad, oldSearch := m.loadID, m.planner.searchID
	m.args.Journey = &startupJourney{}
	m.searchName, m.searchCredit = "Old search", "Old credit"
	m.runCommand("clear")
	if routeCtx.Err() == nil {
		t.Fatal("old route was not cancelled")
	}
	if m.workspace.checkpoint != nil || len(m.workspace.items()) != 0 || m.planner.ref != (ItemRef{}) {
		t.Fatal("Start clean retained workspace or checkpoint")
	}
	if m.args.Journey != nil || m.searchName != "" || m.searchCredit != "" || len(m.ui.parents) != 0 {
		t.Fatal("Start clean retained transient state")
	}
	m.Update(loaded{id: oldLoad, document: &Document{}})
	m.Update(lateOpen)
	m.Update(placeSearchResult{id: oldSearch, ref: oldRef})
	m.Update(journeyRouteResult{ref: oldRef})
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	if len(m.workspace.items()) != 0 || m.savedWork.candidate != nil {
		t.Fatal("late work recreated content")
	}
	m.openSavedWorkspaceDialog("save")
	if m.ui.search.Value() != "" {
		t.Fatal("new save reused checkpoint name")
	}
	m.ui.search.SetValue("New workspace")
	m.saveWorkspaceFromDialog()
	if m.workspace.checkpoint == nil || m.workspace.checkpoint.path == cp.path {
		t.Fatal("new save targeted old checkpoint")
	}
	after, _ := os.ReadFile(cp.path)
	if !bytes.Equal(before, after) {
		t.Fatal("Start clean modified named save")
	}
}

func TestClearJourneyPreservesActivityOtherJourneyAndManualSave(t *testing.T) {
	for _, action := range []string{"panel", "palette"} {
		t.Run(action, func(t *testing.T) {
			m := themeViewer(t, 70, 24, false)
			activity := m.workspace.selection
			m.openJourney()
			target := m.planner.ref
			other := m.workspace.newJourney()
			m.workspace.selection = activity
			m.openSavedWorkspaceDialog("save")
			m.ui.search.SetValue("Two journeys")
			m.saveWorkspaceFromDialog()
			cp := m.workspace.checkpoint
			count := len(m.workspace.items())
			before, _ := os.ReadFile(cp.path)
			m.openJourney()
			oldSearch, oldLoad := m.planner.searchID, m.loadID
			m.following = true
			activity.playback.Playing = true
			if action == "palette" {
				m.runCommand("clear-journey")
			} else {
				j, _ := m.workspace.journey(target)
				for i, entry := range journeyActions {
					if entry.action == "clear-journey" {
						m.planner.row = len(j.Stops) + i
					}
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			if _, err := m.workspace.journey(target); err == nil {
				t.Fatal("target journey remains")
			}
			if _, err := m.workspace.journey(other); err != nil {
				t.Fatal("other journey removed")
			}
			if m.workspace.selection != activity || m.loadID != oldLoad {
				t.Fatal("unrelated activity or import changed")
			}
			if !m.following || !activity.playback.Playing {
				t.Fatal("clearing another journey interrupted activity playback or following")
			}
			m.Update(placeSearchResult{id: oldSearch, ref: target})
			m.Update(journeyRouteResult{ref: target})
			if len(m.workspace.items()) != count-1 {
				t.Fatal("late result recreated journey")
			}
			after, _ := os.ReadFile(cp.path)
			if !bytes.Equal(before, after) {
				t.Fatal("clear automatically saved")
			}
			restored, _, err := prepareWorkspace(cp.path)
			if err != nil || len(restored.items()) != count {
				t.Fatal("unchanged checkpoint lost journey", err)
			}
			m.openSavedWorkspaceDialog("save")
			m.saveWorkspaceFromDialog()
			restored, _, err = prepareWorkspace(cp.path)
			if err != nil || len(restored.items()) != count-1 {
				t.Fatal("explicit save did not retain clear", err)
			}
		})
	}
}
