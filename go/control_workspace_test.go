package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestControlWorkspaceSaveOpenAndConflicts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "東京-café"))
	a := journeyViewer(t)
	b := journeyViewer(t)
	_, _ = a.applyControl(controlTestRequest(a, "journey.create", map[string]any{
		"mode": "offline", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}},
	}))
	saved, _ := a.applyControl(controlTestRequest(a, "workspace.save", map[string]any{"name": "Agent route"}))
	if saved.Error != nil {
		t.Fatal(saved)
	}
	path := controlTestResult(t, saved)["path"].(string)
	open := func(m *viewer) {
		t.Helper()
		response, cmd := m.applyControl(controlTestRequest(m, "workspace.open", map[string]any{"path": path, "discard": true}))
		if response.State != "pending" {
			t.Fatal(response)
		}
		m.Update(cmd())
		status, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": response.OperationID}))
		if status.Error != nil || status.State != "applied" {
			t.Fatal(status)
		}
	}
	generation := b.workspace.liveIdentity()
	open(b)
	if b.workspace.liveIdentity() == generation || b.workspace.checkpoint.manifest.Revision != 1 {
		t.Fatal("open did not install checkpoint with a fresh generation")
	}
	saved, _ = a.applyControl(controlTestRequest(a, "workspace.save", map[string]any{}))
	if saved.Error != nil {
		t.Fatal(saved)
	}
	conflict, _ := b.applyControl(controlTestRequest(b, "workspace.save", map[string]any{}))
	if conflict.Error == nil || conflict.Error.Code != "workspace_revision_conflict" || conflict.Changed {
		t.Fatal(conflict)
	}
	copy, _ := b.applyControl(controlTestRequest(b, "workspace.save", map[string]any{"save_as": true}))
	if copy.Error != nil || b.workspace.checkpoint.manifest.ID == a.workspace.checkpoint.manifest.ID {
		t.Fatal(copy)
	}
}

func TestControlWorkspaceOpenRejectsInterveningEdit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := journeyViewer(t)
	saved, _ := m.applyControl(controlTestRequest(m, "workspace.save", map[string]any{"name": "Empty"}))
	path := controlTestResult(t, saved)["path"].(string)
	request := controlTestRequest(m, "workspace.open", map[string]any{"path": path, "discard": true})
	_, cmd := m.applyControl(request)
	prepared := cmd()
	m.workspace.edited()
	generation := m.workspace.liveIdentity()
	m.Update(prepared)
	response, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": request.ID}))
	if response.State != "superseded" || m.workspace.liveIdentity() != generation {
		t.Fatal(response)
	}
}

func TestControlWorkspacePartialOpenHasNoRepairDialog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := journeyViewer(t)
	data, err := os.ReadFile("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ride.gpx")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.workspace.open(path); err != nil {
		t.Fatal(err)
	}
	saved, _ := m.applyControl(controlTestRequest(m, "workspace.save", map[string]any{"name": "Missing import"}))
	if saved.Error != nil {
		t.Fatal(saved)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	response, cmd := m.applyControl(controlTestRequest(m, "workspace.open", map[string]any{
		"path": controlTestResult(t, saved)["path"], "discard": true,
	}))
	m.Update(cmd())
	result, _ := m.applyControl(controlTestRequest(m, "operation.status", map[string]any{"operation_id": response.OperationID}))
	if result.Error != nil || len(m.workspace.unresolved) != 1 || m.ui.dialog != dialogNone {
		t.Fatal("partial restore blocked on a dialog", result, m.ui.dialog)
	}
}

func TestPendingHumanOpenPreservesAgentEdits(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := journeyViewer(t)
	saved, _ := m.applyControl(controlTestRequest(m, "workspace.save", map[string]any{"name": "Empty"}))
	path := controlTestResult(t, saved)["path"].(string)
	m.openSavedWorkspaceDialog("open")
	m.savedWork.mode = "library"
	cmd := m.beginSavedOpen(path)
	ready := cmd()
	created, _ := m.applyControl(controlTestRequest(m, "journey.create", map[string]any{"mode": "offline", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}}}))
	if created.Error != nil {
		t.Fatal(created)
	}
	m.Update(ready)
	if len(m.workspace.documents) == 0 {
		t.Fatal("pending human open discarded an intervening successful agent mutation")
	}
}

func TestControlWorkspaceOpenRequiresAbsolutePath(t *testing.T) {
	m := journeyViewer(t)
	generation, revision := m.workspace.liveIdentity(), m.workspace.liveRevision
	response, cmd := m.applyControl(controlTestRequest(m, "workspace.open", map[string]any{"path": "relative.json", "discard": true}))
	if response.Error == nil || response.Error.Code != "invalid_arguments" || cmd != nil || response.Changed || response.Generation != generation || response.Revision != revision {
		t.Fatal("relative path reached workspace loader", response)
	}
}
