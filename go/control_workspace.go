package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
)

type controlWorkspaceReady struct {
	id        string
	candidate *Workspace
	camera    Camera
	err       error
}

type controlWorkspaceSaveStart struct {
	id   string
	args controlArgs
}

func (m *viewer) controlCheckpoint() any {
	checkpoint := m.workspace.checkpoint
	if checkpoint == nil {
		return nil
	}
	v := checkpoint.manifest
	return map[string]any{"id": v.ID, "name": controlText(v.Name, 256), "revision": v.Revision,
		"path": controlText(checkpoint.path, 4096), "created": v.Created, "modified": v.Modified,
		"unresolved_imports": len(m.workspace.unresolved)}
}

func (m *viewer) beginControlWorkspaceOpen(r controlRequest, a controlArgs, op *controlOperation) (controlResponse, tea.Cmd) {
	if !filepath.IsAbs(a.Path) {
		return m.fail(r, "invalid_arguments", "path must be absolute", "resolve the workspace path before sending"), nil
	}
	if !a.Discard {
		return m.fail(r, "unsaved_changes", "opening requires explicit discard",
			"save the working copy first, or supply discard=true"), nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	op.cancel = cancel
	response := m.response(r)
	response.State, response.Message = "pending", "preparing saved workspace"
	return response, func() tea.Msg {
		defer cancel()
		candidate, camera, err := prepareWorkspaceContext(ctx, a.Path)
		return controlWorkspaceReady{id: r.ID, candidate: candidate, camera: camera, err: err}
	}
}

func (m *viewer) acceptControlWorkspace(result controlWorkspaceReady) tea.Cmd {
	m.reconcileOperations()
	op := m.control.operations[result.id]
	if op == nil || op.response.State != "pending" {
		return nil
	}
	response := m.response(op.request)
	var cmd tea.Cmd
	switch {
	case result.err != nil:
		response = m.fail(op.request, "workspace_open_failed", result.err.Error(), "check the saved workspace path")
	case *op.request.Revision != m.workspace.liveRevision:
		response = m.fail(op.request, "superseded", "working copy changed while preparing the workspace",
			"inspect current state before opening again")
		response.State = "superseded"
	default:
		cmd = m.installSavedWorkspace(result.candidate, result.camera)
		response = m.response(op.request)
		response.Changed = true
		response.Result = controlJSON(map[string]any{"checkpoint": m.controlCheckpoint(),
			"unresolved_imports": len(m.workspace.unresolved)})
		response.Message = "saved workspace opened"
		if len(m.workspace.unresolved) > 0 {
			response.Message = "saved workspace opened with unresolved imports; use the viewer repair screen"
		}
	}
	response.OperationID = result.id
	op.response = response
	return cmd
}

func (m *viewer) beginControlWorkspaceSave(r controlRequest, a controlArgs) (controlResponse, tea.Cmd) {
	response := m.response(r)
	response.State, response.Message = "pending", "preparing workspace save"
	return response, func() tea.Msg { return controlWorkspaceSaveStart{id: r.ID, args: a} }
}

func (m *viewer) startControlWorkspaceSave(start controlWorkspaceSaveStart) tea.Cmd {
	m.reconcileOperations()
	op := m.control.operations[start.id]
	if op == nil {
		return nil
	}
	if op.response.State == "pending" {
		if *op.request.Revision != m.workspace.liveRevision {
			op.response = m.fail(op.request, "superseded", "working copy changed before saving",
				"inspect current state before saving again")
			op.response.State = "superseded"
		} else {
			op.response, _ = m.controlWorkspaceSave(op.request, start.args)
		}
		op.response.OperationID = start.id
	}
	m.updateControlActivity(time.Now())
	m.rebuild()
	if op.saveReply != nil {
		op.saveReply <- op.response
		op.saveReply = nil
	}
	return nil
}

func (m *viewer) controlWorkspaceSave(r controlRequest, a controlArgs) (controlResponse, tea.Cmd) {
	name := a.Name
	if name == "" && m.workspace.checkpoint != nil {
		name = m.workspace.checkpoint.manifest.Name
	}
	_, err := m.saveWorkspaceCheckpoint(name, a.SaveAs)
	if err != nil {
		code, recovery := "workspace_save_failed", "correct the name or storage error"
		if errors.Is(err, errWorkspaceConflict) {
			code, recovery = "workspace_revision_conflict", "reload the checkpoint or save_as=true; your working copy is unchanged"
		}
		return m.fail(r, code, err.Error(), recovery), nil
	}
	response := m.response(r)
	response.Changed = true
	response.Result = controlJSON(m.controlCheckpoint())
	response.Message = "workspace saved"
	return response, nil
}
