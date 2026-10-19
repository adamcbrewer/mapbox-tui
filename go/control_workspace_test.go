package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func controlTestSave(t *testing.T, m *viewer, args map[string]any) controlResponse {
	t.Helper()
	request := controlTestRequest(m, "workspace.save", args)
	response, cmd := m.applyControl(request)
	if cmd != nil {
		m.Update(cmd())
		response = m.control.operations[request.ID].response
	}
	return response
}

func TestControlWorkspaceSaveOpenAndConflicts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "東京-café"))
	a := journeyViewer(t)
	b := journeyViewer(t)
	_, _ = a.applyControl(controlTestRequest(a, "journey.create", map[string]any{
		"mode": "offline", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}},
	}))
	saved := controlTestSave(t, a, map[string]any{"name": "Agent route"})
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
	saved = controlTestSave(t, a, map[string]any{})
	if saved.Error != nil {
		t.Fatal(saved)
	}
	conflict := controlTestSave(t, b, map[string]any{})
	if conflict.Error == nil || conflict.Error.Code != "workspace_revision_conflict" || conflict.Changed {
		t.Fatal(conflict)
	}
	copy := controlTestSave(t, b, map[string]any{"save_as": true})
	if copy.Error != nil || b.workspace.checkpoint.manifest.ID == a.workspace.checkpoint.manifest.ID {
		t.Fatal(copy)
	}
}

func TestControlWorkspaceOpenRejectsInterveningEdit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := journeyViewer(t)
	saved := controlTestSave(t, m, map[string]any{"name": "Empty"})
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
	saved := controlTestSave(t, m, map[string]any{"name": "Missing import"})
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
	saved := controlTestSave(t, m, map[string]any{"name": "Empty"})
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

func TestControlWorkspaceSavePreservesWorkerAllowance(t *testing.T) {
	for _, pending := range []int{pendingCapacity - 1, pendingCapacity} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			m := journeyViewer(t)
			t.Cleanup(m.cancelControlOperations)
			path, err := filepath.Abs("../fixtures/example.gpx")
			if err != nil {
				t.Fatal(err)
			}
			load := func() controlResponse {
				response, _ := m.applyControl(controlTestRequest(m, "load", map[string]any{"path": path}))
				return response
			}
			for range pending {
				if response := load(); response.State != "pending" {
					t.Fatal("could not fill worker allowance", response)
				}
			}
			reply := make(chan controlResponse, 1)
			request := controlTestRequest(m, "workspace.save", map[string]any{"name": "Save while busy"})
			cmd := m.handleControl(controlMessage{request: request, reply: reply})
			if cmd == nil {
				t.Fatal("background worker limit blocked saving")
			}
			if pending < pendingCapacity {
				if response := load(); response.State != "pending" {
					t.Fatal("queued save consumed background worker capacity", response)
				}
			}
			if response := load(); response.Error == nil || response.Error.Code != "busy" {
				t.Fatal("worker allowance did not reject excess background work", response)
			}
			m.Update(cmd())
			if response := <-reply; response.State != "applied" || m.workspace.checkpoint == nil {
				t.Fatal("save did not finish with busy background workers", response)
			}
		})
	}
}
