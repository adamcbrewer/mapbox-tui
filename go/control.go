package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

const operationRetention = 15 * time.Minute
const operationCapacity = 256
const pendingCapacity = 16
const recoveryCapacity = pendingCapacity*2 + 1

type controlOperation struct {
	request       controlRequest
	fingerprint   [32]byte
	response      controlResponse
	created       time.Time
	ref           ItemRef
	routeID       uint64
	routeRevision uint64
	cancel        context.CancelFunc
	transition    string
	activitySeen  bool
	saveReply     chan<- controlResponse
}

type viewerControl struct {
	server     *sessionServer
	operations map[string]*controlOperation
	activity   controlActivity
}

type controlLoaded struct {
	id       string
	document *Document
	err      error
}

func (m *viewer) waitControl() tea.Cmd {
	if m.control == nil || m.control.server == nil {
		return nil
	}
	server := m.control.server
	return func() tea.Msg {
		select {
		case msg := <-server.inbox:
			return msg
		case <-server.ctx.Done():
			return nil
		}
	}
}

func (m *viewer) response(request controlRequest) controlResponse {
	id := ""
	if m.control != nil && m.control.server != nil {
		id = m.control.server.info.ID
	}
	return controlResponse{OutcomeKnown: true, Version: controlVersion, RequestID: request.ID, SessionID: id,
		Generation: m.workspace.liveIdentity(), Revision: m.workspace.liveRevision, State: "applied", Message: "completed"}
}

func (m *viewer) fail(request controlRequest, code, message, recovery string) controlResponse {
	r := m.response(request)
	r.State, r.Message = "failed", message
	r.Error = &controlError{Code: code, Message: message, Recovery: recovery}
	return r
}

func controlRead(action string) bool {
	switch action {
	case "status", "capabilities", "operation.status", "journey.inspect", "theme.list":
		return true
	}
	return false
}

func (m *viewer) handleControl(msg controlMessage) tea.Cmd {
	if msg.ctx != nil && msg.ctx.Err() != nil {
		return nil
	}
	response, cmd := m.applyControl(msg.request)
	m.receiveControlActivity(msg.request, response)
	if msg.request.Action == "workspace.save" && response.State == "pending" && cmd != nil {
		m.control.operations[msg.request.ID].saveReply = msg.reply
		m.rebuild()
		return cmd
	}
	msg.reply <- response
	return cmd
}

func (m *viewer) applyControl(request controlRequest) (controlResponse, tea.Cmd) {
	if m.control == nil {
		m.control = &viewerControl{}
	}
	if m.control.operations == nil {
		m.control.operations = make(map[string]*controlOperation)
	}
	m.reconcileOperations()
	if _, ok := controlSpecs[request.Action]; !ok {
		return m.fail(
			request,
			"unknown_action",
			"unknown action: "+request.Action,
			"read capabilities",
		), nil
	}
	args, err := decodeControlArgs(request.Action, request.Args)
	if err != nil {
		return m.fail(
			request,
			"invalid_arguments",
			err.Error(),
			"use the action's advertised schema",
		), nil
	}
	if controlRead(request.Action) {
		r := m.response(request)
		switch request.Action {
		case "status":
			r.Result = controlJSON(m.controlStatus(args))
		case "theme.list":
			r.Result = controlJSON(m.controlThemes())
		case "capabilities":
			r.Result = controlJSON(controlCapabilities())
		case "operation.status":
			op := m.control.operations[args.OperationID]
			if op == nil {
				return m.fail(
					request,
					"operation_not_found",
					"operation not found or expired",
					"inspect state before issuing a new mutation",
				), nil
			}
			r = op.response
			r.RequestID = request.ID
		case "journey.inspect":
			j, err := m.workspace.journey(args.Ref)
			if err != nil {
				return m.fail(
					request,
					"item_not_found",
					err.Error(),
					"read status for current item IDs",
				), nil
			}
			r.Result = controlJSON(m.journeyStatus(args.Ref, j))
		}
		return r, nil
	}
	// Canonical arguments make retries insensitive to JSON whitespace/key ordering.
	fingerprint := sha256.Sum256(controlJSON(struct {
		Action, Generation string
		Revision           *uint64
		Args               controlArgs
	}{
		Action: request.Action, Generation: request.Generation, Revision: request.Revision, Args: args}))
	if op := m.control.operations[request.ID]; op != nil {
		if fingerprint != op.fingerprint {
			return m.fail(
				request,
				"request_id_conflict",
				"request_id was already used with different arguments",
				"use the original request unchanged or a new request ID",
			), nil
		}
		return op.response, nil
	}
	if request.Generation == "" || request.Revision == nil {
		return m.fail(
			request,
			"precondition_required",
			"generation and expected_revision are required for mutations",
			"read status and supply its generation and revision",
		), nil
	}
	if request.Generation != m.workspace.liveIdentity() {
		return m.fail(
			request,
			"workspace_changed",
			"workspace changed; no changes applied",
			"read session status before retrying",
		), nil
	}
	if *request.Revision != m.workspace.liveRevision {
		r := m.fail(
			request,
			"revision_conflict",
			"workspace revision changed; no changes applied",
			"read current state before retrying",
		)
		r.Result = controlJSON(map[string]any{"expected_revision": *request.Revision, "current_revision": m.workspace.liveRevision})
		return r, nil
	}
	pending, ordinary, recovery := 0, 0, 0
	for id, op := range m.control.operations {
		if op.response.State != "pending" && time.Since(op.created) > operationRetention {
			delete(m.control.operations, id)
			continue
		}
		if recoveryControl(op.request.Action) {
			recovery++
		} else {
			ordinary++
		}
		if op.response.State == "pending" && op.transition == "" &&
			op.request.Action != "workspace.save" {
			pending++
		}
	}
	full := ordinary >= operationCapacity || (m.startsControlWork(request.Action, args) && pending >= pendingCapacity)
	if recoveryControl(request.Action) {
		full = recovery >= recoveryCapacity
	}
	if full {
		return m.fail(
			request,
			"busy",
			"operation capacity reached; no changes applied",
			"wait for pending operations or retained results to expire",
		), nil
	}
	op := &controlOperation{request: request, fingerprint: fingerprint, created: time.Now()}
	m.control.operations[request.ID] = op
	before := m.workspace.liveRevision
	r, cmd := m.executeControl(request, args, op)
	r.Changed = r.Changed || m.workspace.liveRevision != before
	r.Generation, r.Revision = m.workspace.liveIdentity(), m.workspace.liveRevision
	r.OperationID = request.ID
	if cameraControl(request.Action) && r.Error == nil {
		op.transition = m.motion.ID
		r.TransitionID = op.transition
		if m.motion.Active {
			r.State, r.Message = "pending", "camera destination accepted; transition in progress"
		} else {
			r.Message = "camera transition settled"
		}
	}
	op.response = r
	return r, cmd
}

func (m *viewer) reconcileOperations() {
	if m.control == nil {
		return
	}
	for _, op := range m.control.operations {
		if op.response.State != "pending" {
			continue
		}
		state, message := "", ""
		if op.request.Generation != m.workspace.liveIdentity() {
			state, message = "superseded", "workspace was replaced or cleared"
		}
		if op.transition != "" && state == "" {
			switch {
			case op.transition != m.motion.ID:
				state, message = "superseded", "camera transition was replaced"
			case !m.motion.Active:
				target := m.motion.Target
				settled := m.camera.Center == target.Center && m.camera.Zoom == target.Zoom && m.camera.Bearing == target.Bearing
				if settled {
					op.response.Generation, op.response.Revision = m.workspace.liveIdentity(), m.workspace.liveRevision
					op.response.State, op.response.Message = "applied", "camera transition settled"
					continue
				}
				state, message = "superseded", "camera transition was interrupted"
			}
		}
		if op.ref != (ItemRef{}) {
			j, err := m.workspace.journey(op.ref)
			if err != nil || j.revision != op.routeRevision || j.requestID != op.routeID {
				state, message = "superseded", "journey was edited, removed or recalculated"
			}
		}
		if state != "" {
			if op.cancel != nil {
				op.cancel()
			}
			op.response.Generation, op.response.Revision = m.workspace.liveIdentity(), m.workspace.liveRevision
			op.response.State, op.response.Message = state, message
			op.response.Error = &controlError{
				Code:     state,
				Message:  message,
				Recovery: "inspect current workspace before retrying",
			}
		}
	}
}

func (m *viewer) acceptControlLoad(result controlLoaded) tea.Cmd {
	m.reconcileOperations()
	op := m.control.operations[result.id]
	if op == nil || op.response.State != "pending" {
		return nil
	}
	r := m.response(op.request)
	r.OperationID = result.id
	if result.err != nil {
		r = m.fail(
			op.request,
			"import_failed",
			result.err.Error(),
			"check the file and retry with a new request ID",
		)
		r.OperationID = result.id
	} else {
		ref := m.workspace.addDocument(result.document)
		m.selectionChanged()
		r.Changed = true
		r.Revision = m.workspace.liveRevision
		r.Message = "file loaded and selected"
		r.Result = controlJSON(map[string]any{"ref": ref})
	}
	op.response = r
	return nil
}

func (m *viewer) acceptControlRoute(result journeyRouteResult, accepted bool) {
	if m.control == nil {
		return
	}
	for _, op := range m.control.operations {
		if op.response.State != "pending" || op.ref != result.ref || op.routeID != result.id || op.routeRevision != result.revision {
			continue
		}
		if !accepted {
			continue
		}
		r := m.response(op.request)
		r.OperationID = op.request.ID
		r.Changed = op.response.Changed || result.err == nil
		r.Message = "route calculated"
		if result.err != nil {
			r.State = "failed"
			r.Message = "journey retained; route calculation failed: " + result.err.Error()
			r.Error = &controlError{
				Code:     "routing_failed",
				Message:  r.Message,
				Recovery: "inspect journey and retry calculation when available",
			}
		}
		if j, err := m.workspace.journey(result.ref); err == nil {
			r.Result = controlJSON(m.journeyStatus(result.ref, j))
		}
		op.response = r
	}
	m.reconcileOperations()
}

const controlPageSize = 20

func controlText(value string, limit int) string {
	value = cleanPlaceText(value)
	if len(value) > limit {
		for limit > 0 && !utf8.RuneStart(value[limit]) {
			limit--
		}
		return value[:limit] + "..."
	}
	return value
}

func (m *viewer) controlStatus(options ...controlArgs) map[string]any {
	a := controlArgs{}
	if len(options) > 0 {
		a = options[0]
	}
	limit := controlPageSize
	if a.Limit != nil {
		limit = *a.Limit
	}
	tracks, journeys := []any{}, []any{}
	start := min(a.Offset, len(m.workspace.documents))
	end := min(start+limit, len(m.workspace.documents))
	if !a.Summary {
		for _, entry := range m.workspace.documents[start:end] {
			items := []any{}
			itemStart := min(a.ItemOffset, len(entry.document.Items))
			itemEnd := min(itemStart+controlPageSize, len(entry.document.Items))
			for _, item := range entry.document.Items[itemStart:itemEnd] {
				ref := ItemRef{DocumentID: entry.id, ItemID: item.ID}
				items = append(items, map[string]any{"item_id": item.ID, "ref": ref,
					"name": controlText(item.Track.Name, 256), "kind": item.Kind,
					"visible": !entry.hidden[item.ID], "point_count": len(item.Track.Points)})
				if item.Journey != nil {
					journeys = append(journeys, m.journeySummary(ref, item.Journey))
				}
			}
			var nextItem any
			if itemEnd < len(entry.document.Items) {
				nextItem = itemEnd
			}
			tracks = append(tracks, map[string]any{
				"document_id": entry.id, "name": controlText(entry.document.Name, 256),
				"filename": controlText(entry.document.Filename, 256), "path": controlText(entry.document.Path, 4096),
				"items": items, "item_count": len(entry.document.Items), "next_item_offset": nextItem,
			})
		}
	}
	var next any
	if !a.Summary && end < len(m.workspace.documents) {
		next = end
	}
	var active *ItemRef
	if m.workspace.selection != nil {
		ref := m.workspace.selection.ref
		active = &ref
	}
	var playback any
	if p := m.activePlayback(); p != nil {
		playback = map[string]any{"playing": p.Playing, "point_index": p.nearest(), "elapsed_s": p.Elapsed,
			"duration_s": p.duration(), "mode": p.Mode, "speed": p.multiplier(), "available_speeds": playbackSpeeds}
	}
	var session any
	if m.control != nil && m.control.server != nil {
		session = m.control.server.info
	}
	return map[string]any{"session": session, "generation": m.workspace.liveIdentity(), "revision": m.workspace.liveRevision,
		"persistence_dirty_available": false, "checkpoint": m.controlCheckpoint(), "tracks": tracks, "journeys": journeys, "document_count": len(m.workspace.documents), "next_offset": next, "active_item": active,
		"camera": controlCamera(m.destination()), "camera_current": controlCamera(m.camera),
		"camera_transition_id": m.motion.ID, "camera_animating": m.motion.Active, "camera_animation": !m.settings.NoAnimation,
		"theme": m.controlThemeID(), "playback": playback}
}

func controlCamera(c Camera) map[string]any {
	centre := unproject(c.Center)
	return map[string]any{"lat": centre.Y, "lon": centre.X, "zoom": c.Zoom, "bearing": c.Bearing}
}

func (m *viewer) executeControl(r controlRequest, a controlArgs, op *controlOperation) (controlResponse, tea.Cmd) {
	response := m.response(r)
	var err error
	var cmd tea.Cmd
	switch r.Action {
	case "workspace.open":
		return m.beginControlWorkspaceOpen(r, a, op)
	case "workspace.save":
		return m.beginControlWorkspaceSave(r, a)
	case "load":
		if !filepath.IsAbs(a.Path) {
			err = fmt.Errorf("path must be absolute")
			break
		}
		if entry := m.workspace.documentAtPath(a.Path); entry != nil {
			ref := m.workspace.activateDocument(entry)
			m.selectionChanged()
			response.Result = controlJSON(map[string]any{"ref": ref})
			response.Message = "existing import selected and visibility restored"
			break
		}
		response.State, response.Message = "pending", "import queued; query operation status for completion"
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		op.cancel = cancel
		id, path := r.ID, a.Path
		cmd = func() tea.Msg {
			defer cancel()
			info, err := os.Stat(path)
			if err == nil && info.Size() > 128<<20 {
				err = fmt.Errorf("remote imports are limited to 128 MiB")
			}
			if err == nil && !info.Mode().IsRegular() {
				err = fmt.Errorf("import requires a regular file")
			}
			var document *Document
			if err == nil {
				document, err = loadDocumentContext(ctx, path)
			}
			return controlLoaded{id: id, document: document, err: err}
		}
	case "operation.cancel":
		target := m.control.operations[a.OperationID]
		if target == nil {
			return m.fail(
				r,
				"operation_not_found",
				"operation not found",
				"inspect current state",
			), nil
		}
		if target.response.State == "pending" {
			if target.cancel != nil {
				target.cancel()
			}
			if target.transition != "" && target.transition == m.motion.ID {
				before := m.destination()
				m.motion.Active = false
				m.recordViewChange(before)
			}
			if target.ref != (ItemRef{}) {
				if j, e := m.workspace.journey(target.ref); e == nil {
					j.cancelRoute()
					m.workspace.edited()
				}
			}
			target.response.Generation = m.workspace.liveIdentity()
			target.response.Revision = m.workspace.liveRevision
			target.response.State = "cancelled"
			target.response.Message = "operation cancelled; previously applied edits remain"
			target.response.Error = &controlError{
				Code:     "cancelled",
				Message:  target.response.Message,
				Recovery: "inspect current state",
			}
			response.Message = target.response.Message
		}
		response.Result = controlJSON(target.response)
	case "select":
		err = m.workspace.selectItem(a.Ref)
		if err == nil {
			m.selectionChanged()
		}
	case "visibility":
		hadSelection := m.workspace.selection != nil
		if a.Visible == nil {
			err = fmt.Errorf("visible is required")
			break
		}
		if a.Ref.ItemID == 0 {
			err = m.workspace.setDocumentVisible(a.Ref.DocumentID, *a.Visible)
		} else {
			err = m.workspace.setVisible(a.Ref, *a.Visible)
		}
		if err == nil && hadSelection && m.workspace.selection == nil {
			m.selectionChanged()
		}
	case "remove":
		hadSelection := m.workspace.selection != nil
		if a.Ref.ItemID == 0 {
			err = m.workspace.removeDocument(a.Ref.DocumentID)
		} else {
			err = m.workspace.removeItem(a.Ref)
		}
		if err == nil && hadSelection && m.workspace.selection == nil {
			m.selectionChanged()
		}
	case "workspace.clear":
		if !a.Discard {
			return m.fail(
				r,
				"unsaved_changes",
				"clearing requires explicit discard",
				"supply discard=true to clear the live working copy",
			), nil
		}
		cmd = m.clearWorkspace()
	case "theme.set":
		index := -1
		for i, theme := range m.themes {
			if theme.ID == a.Theme {
				index = i
				break
			}
		}
		if index < 0 {
			return m.fail(r, "invalid_arguments", "theme is not available in this viewer", "use theme.list to choose an ID"), nil
		}
		m.applyTheme(index)
		if m.ui.dialog == dialogThemes {
			m.ui.themeBefore = index
		}
		response.Result = controlJSON(map[string]any{"theme": a.Theme})
	case "camera.animation":
		m.setCameraAnimation(*a.Enabled)
		response.Result = controlJSON(map[string]any{"enabled": *a.Enabled})
	case "camera.set", "camera.pan", "camera.fit":
		before := m.destination()
		target := before
		switch r.Action {
		case "camera.set":
			var checked Camera
			checked, err = viewport(nil, Args{Lon: a.Lon, Lat: a.Lat, Zoom: a.Zoom, Bearing: a.Bearing})
			if err != nil {
				break
			}
			if a.Lon != nil {
				target.Center = checked.Center
			}
			if a.Zoom != nil {
				target.Zoom = checked.Zoom
			}
			if a.Bearing != nil {
				target.Bearing = checked.Bearing
			}
		case "camera.pan":
			target.pan(a.DX*2, a.DY*4)
		case "camera.fit":
			target = m.workspace.fitVisible(before.Width, before.Height, before.Aspect)
		}
		if err == nil {
			m.controlMoveCamera(target, a.Animate)
			m.setFollowing(false)
			m.recordViewChange(before)
			cmd = m.fetch()
			response.Result = controlJSON(controlCamera(target))
		}
	case "playback.play", "playback.pause", "playback.seek", "playback.speed":
		if a.Ref == (ItemRef{}) {
			err = fmt.Errorf("explicit ref required")
			break
		}
		entry, index, lookupErr := m.workspace.lookup(a.Ref)
		if lookupErr != nil {
			err = lookupErr
			break
		}
		track := entry.document.Items[index]
		candidate := m.activePlayback()
		selectionChanged := m.workspace.selection == nil || m.workspace.selection.ref != a.Ref
		if selectionChanged {
			candidate = nil
		}
		if candidate == nil && track.Journey == nil {
			candidate = newPlayback(track.Track)
		}
		if candidate == nil || candidate.duration() == 0 {
			return m.fail(
				r,
				"playback_unavailable",
				"item has no playable recording",
				"select an imported recording with playable geometry",
			), nil
		}
		if r.Action == "playback.seek" && (a.PointIndex == nil || *a.PointIndex < 0 || *a.PointIndex >= len(track.Track.Points)) {
			err = fmt.Errorf("point_index outside item")
			break
		}
		if err = m.workspace.selectItem(a.Ref); err != nil {
			break
		}
		if selectionChanged {
			m.selectionChanged()
		}
		p := m.activePlayback()
		switch r.Action {
		case "playback.play":
			m.workspace.setPlaybackPlaying(true)
		case "playback.pause":
			m.workspace.setPlaybackPlaying(false)
		case "playback.seek":
			err = m.workspace.seek(*a.PointIndex)
		case "playback.speed":
			m.advancePlayback(time.Now())
			for index, speed := range playbackSpeeds {
				if speed == a.Speed {
					err = m.workspace.setPlaybackSpeed(index)
				}
			}
			response.Result = controlJSON(map[string]any{"speed": p.multiplier(), "playing": p.Playing})
		}
		m.lastTick = time.Now()
	default:
		return m.executeJourneyControl(r, a, op)
	}
	if err != nil {
		return m.fail(
			r,
			"invalid_arguments",
			err.Error(),
			"inspect current state and correct the arguments",
		), nil
	}
	m.layout()
	m.dirty = true
	return response, cmd
}

func (m *viewer) isControlRoute(result journeyRouteResult) bool {
	if m.control == nil {
		return false
	}
	for _, op := range m.control.operations {
		if op.ref == result.ref && op.routeID == result.id && op.routeRevision == result.revision {
			return true
		}
	}
	return false
}

func (m *viewer) cancelControlOperations() {
	if m.control == nil {
		return
	}
	for _, op := range m.control.operations {
		if op.cancel != nil {
			op.cancel()
		}
	}
}

// controlMoveCamera applies a per-command override without changing the session preference.
func (m *viewer) controlMoveCamera(target Camera, override *bool) {
	animate := !m.settings.NoAnimation
	if override != nil {
		animate = *override
	}
	m.moveCameraAnimated(target, animate)
}

func (m *viewer) controlThemeID() string {
	if len(m.themes) == 0 {
		return ""
	}
	return m.theme().ID
}

func (m *viewer) controlThemes() any {
	themes := make([]map[string]string, 0, len(m.themes))
	for _, theme := range m.themes {
		themes = append(themes, map[string]string{"id": theme.ID, "name": theme.Name})
	}
	return map[string]any{"themes": themes, "selected": m.controlThemeID()}
}

func recoveryControl(action string) bool {
	return action == "operation.cancel" || action == "workspace.clear"
}

// Camera transitions share a single viewer animation, not the worker budget.
func (m *viewer) startsControlWork(action string, args controlArgs) bool {
	if action == "load" || action == "workspace.open" {
		return true
	}
	if !strings.HasPrefix(action, "journey.") || action == "journey.show" {
		return false
	}
	if action == "journey.create" || action == "journey.mode" {
		return args.Mode != "offline"
	}
	j, err := m.workspace.journey(args.Ref)
	return err == nil && j.Mode != journeyManual
}

func cameraControl(action string) bool {
	switch action {
	case "camera.set", "camera.pan", "camera.fit", "journey.show":
		return true
	}
	return false
}
