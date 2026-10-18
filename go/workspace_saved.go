package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

func (w *Workspace) snapshot(name string, camera Camera, saveAs bool) (savedWorkspace, error) {
	v := newSavedWorkspace(name)
	if w.checkpoint != nil && !saveAs {
		previous := w.checkpoint.manifest
		v.ID, v.Created, v.Revision = previous.ID, previous.Created, previous.Revision+1
		v.Modified = time.Now().UTC()
	}
	centre := unproject(camera.Center)
	v.Camera = workspaceCamera{Lon: &centre.X, Lat: &centre.Y, Zoom: &camera.Zoom, Bearing: &camera.Bearing}
	v.Imports = append(v.Imports, w.unresolved...)
	for i := range v.Imports {
		v.Imports[i].problem = ""
		v.Order = append(v.Order, v.Imports[i].ID)
	}
	v.Selection = w.deferredSelection
	for _, entry := range w.documents {
		v.Order = append(v.Order, entry.saveID)
		d := entry.document
		imported := savedImport{ID: entry.saveID, Path: d.Path, Fingerprint: d.Fingerprint, Items: []savedItem{}}
		for _, item := range d.Items {
			state := savedItem{ID: entry.itemIDs[item.ID], Key: item.ID,
				Visible: !entry.hidden[item.ID], Color: entry.colors[item.ID]}
			if w.selection != nil && w.selection.ref == (ItemRef{DocumentID: entry.id, ItemID: item.ID}) {
				point := -1
				if w.selection.playback != nil && len(item.Track.Points) > 0 {
					point = w.selection.playback.nearest()
				}
				v.Selection = &savedSelection{Item: state.ID, Point: point}
			}
			if item.Journey == nil {
				imported.Items = append(imported.Items, state)
				continue
			}
			journey := savedJourney{ID: entry.saveID, Item: state, Name: item.Track.Name,
				Mode: item.Journey.Mode, Stops: make([]*savedLocation, len(item.Journey.Stops))}
			for i, stop := range item.Journey.Stops {
				if stop.Location == nil {
					continue
				}
				location := *stop.Location
				journey.Stops[i] = &savedLocation{Name: location.Name, Lon: &location.Lon, Lat: &location.Lat,
					Source: location.Source, ProviderID: location.ProviderID, Attribution: location.Attribution}
			}
			v.Journeys = append(v.Journeys, journey)
		}
		if len(imported.Items) > 0 {
			if slices.ContainsFunc(v.Imports, func(saved savedImport) bool {
				return filepath.Clean(saved.Path) == filepath.Clean(imported.Path)
			}) {
				return savedWorkspace{}, fmt.Errorf("import path is also an unresolved reference; " +
					"remove that reference or remove the loaded file and repair it before saving")
			}
			v.Imports = append(v.Imports, imported)
		}
	}
	if w.checkpoint != nil {
		// Keep unresolved references in their original positions, then append new work.
		order := make([]string, 0, len(v.Order))
		for _, id := range w.checkpoint.manifest.Order {
			if slices.Contains(v.Order, id) {
				order = append(order, id)
			}
		}
		for _, id := range v.Order {
			if !slices.Contains(order, id) {
				order = append(order, id)
			}
		}
		v.Order = order
	}
	return v, v.validate()
}

// prepareWorkspace never mutates an active workspace and never calls a provider.
// Unavailable imports retain their complete saved state for later repair/save.
func prepareWorkspace(path string) (*Workspace, Camera, error) {
	return prepareWorkspaceContext(context.Background(), path)
}

func prepareWorkspaceContext(ctx context.Context, path string) (*Workspace, Camera, error) {
	checkpoint, err := readWorkspaceContext(ctx, path)
	if err != nil {
		return nil, Camera{}, err
	}
	v := checkpoint.manifest
	candidate := &Workspace{checkpoint: checkpoint}
	for _, imported := range v.Imports {
		if err := ctx.Err(); err != nil {
			return nil, Camera{}, err
		}
		if !filepath.IsAbs(imported.Path) {
			imported.Path = filepath.Join(filepath.Dir(checkpoint.path), imported.Path)
		}
		if err := candidate.restoreImportContext(ctx, imported); err != nil {
			imported.problem = err.Error()
			candidate.unresolved = append(candidate.unresolved, imported)
		}
	}
	for _, saved := range v.Journeys {
		if err := ctx.Err(); err != nil {
			return nil, Camera{}, err
		}
		ref := candidate.newJourney()
		entry, index, err := candidate.lookup(ref)
		if err != nil {
			return nil, Camera{}, err
		}
		item := entry.document.Items[index]
		j := item.Journey
		j.Stops = make([]journeyStop, len(saved.Stops))
		for i, location := range saved.Stops {
			stop := journeyStop{ID: i + 1}
			if location != nil {
				stop.Location = &placeLocation{Name: location.Name, Lon: *location.Lon, Lat: *location.Lat,
					Source: location.Source, ProviderID: location.ProviderID, Attribution: location.Attribution}
			}
			j.Stops[i] = stop
		}
		j.nextID, j.Mode, j.modeChosen = len(j.Stops), saved.Mode, true
		j.automaticRoutePending = j.Mode != journeyManual && j.hasCompleteLocations()
		j.automaticRoutePreserveView = true
		track, err := journeyTrack(j.Stops)
		if err != nil {
			return nil, Camera{}, fmt.Errorf("restore journey: %w", err)
		}
		track.Name = saved.Name
		candidate.replaceJourneyTrack(ref, track)
		entry.saveID, entry.itemIDs[item.ID] = saved.ID, saved.Item.ID
		entry.document.Name = saved.Name
		entry.hidden = map[int]bool{item.ID: !saved.Item.Visible}
		entry.colors[item.ID] = saved.Item.Color
	}
	candidate.orderSavedDocuments()
	candidate.clearSelection()
	if err := candidate.restoreSelection(v.Selection); err != nil {
		return nil, Camera{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, Camera{}, err
	}
	camera, err := v.Camera.camera()
	return candidate, camera, err
}

func (w *Workspace) restoreImport(saved savedImport) error {
	return w.restoreImportContext(context.Background(), saved)
}

func (w *Workspace) restoreImportContext(ctx context.Context, saved savedImport) error {
	// A repeated resolved path would alias two saved identities through addDocument.
	if w.documentAtPath(saved.Path) != nil {
		return fmt.Errorf("duplicate import path")
	}
	d, err := loadSavedImportContext(ctx, saved)
	if err != nil {
		return err
	}
	return w.attachSavedImport(saved, d)
}

func loadSavedImport(saved savedImport) (*Document, error) {
	return loadSavedImportContext(context.Background(), saved)
}

func loadSavedImportContext(ctx context.Context, saved savedImport) (*Document, error) {
	d, err := loadDocumentContext(ctx, saved.Path)
	if err != nil {
		return nil, err
	}
	if d.Fingerprint != saved.Fingerprint {
		return nil, fmt.Errorf("file changed; locate the original or remove this reference and import the replacement")
	}
	return d, nil
}

func (w *Workspace) attachSavedImport(saved savedImport, d *Document) error {
	if w.documentAtPath(d.Path) != nil {
		return fmt.Errorf("file is already loaded; remove the unresolved reference deliberately")
	}
	items := make([]*DocumentItem, 0, len(saved.Items))
	for _, state := range saved.Items {
		i := slices.IndexFunc(d.Items, func(item *DocumentItem) bool { return item.ID == state.Key })
		if i < 0 {
			return fmt.Errorf("saved item is unavailable in this import")
		}
		items = append(items, d.Items[i])
	}
	if selection := w.deferredSelection; selection != nil {
		for i, state := range saved.Items {
			if state.ID == selection.Item && selection.Point >= len(items[i].Track.Points) {
				return fmt.Errorf("saved playback position is outside the imported item")
			}
		}
	}
	d.Items = items
	entry := w.appendDocument(d)
	entry.saveID = saved.ID
	entry.hidden = make(map[int]bool)
	for _, state := range saved.Items {
		entry.itemIDs[state.Key] = state.ID
		entry.hidden[state.Key] = !state.Visible
		entry.colors[state.Key] = state.Color
	}
	return nil
}

func (w *Workspace) restoreSelection(selection *savedSelection) error {
	if selection == nil {
		return nil
	}
	for _, entry := range w.documents {
		for _, item := range entry.document.Items {
			if entry.itemIDs[item.ID] != selection.Item {
				continue
			}
			if selection.Point >= len(item.Track.Points) {
				return fmt.Errorf("saved playback position is outside the imported item")
			}
			ref := ItemRef{DocumentID: entry.id, ItemID: item.ID}
			if err := w.selectItem(ref); err != nil {
				return err
			}
			if selection.Point >= 0 {
				if err := w.seek(selection.Point); err != nil {
					return err
				}
			}
			w.deferredSelection = nil
			return nil
		}
	}
	copy := *selection
	w.deferredSelection = &copy
	return nil
}

// replace keeps runtime document IDs monotonic across workspace switches. Late
// route results from the old workspace cannot target an item in the new one.
func (w *Workspace) replace(candidate *Workspace) {
	revision := w.liveRevision + 1
	w.cancelJourneyRoutes()
	offset := w.nextID
	for _, entry := range candidate.documents {
		entry.id += offset
	}
	if candidate.selection != nil {
		candidate.selection.ref.DocumentID += offset
	}
	candidate.nextID += offset
	*w = *candidate
	w.liveGeneration, w.liveRevision = newSessionID(), revision
}

func (w *Workspace) removeUnresolved(id string) error {
	index := slices.IndexFunc(w.unresolved, func(entry savedImport) bool { return entry.ID == id })
	if index < 0 {
		return fmt.Errorf("unresolved import no longer exists")
	}
	if selection := w.deferredSelection; selection != nil {
		for _, item := range w.unresolved[index].Items {
			if item.ID == selection.Item {
				w.deferredSelection = nil
			}
		}
	}
	w.edited()
	w.unresolved = slices.Delete(w.unresolved, index, index+1)
	return nil
}

// locateUnresolved is intended for a candidate workspace, away from the update
// loop. Failed repair leaves the unresolved reference intact.
func (w *Workspace) locateUnresolved(id, path string) error {
	index := slices.IndexFunc(w.unresolved, func(entry savedImport) bool { return entry.ID == id })
	if index < 0 {
		return fmt.Errorf("unresolved import no longer exists")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve replacement path: %w", err)
	}
	saved := w.unresolved[index]
	saved.Path = absolute
	document, err := loadSavedImport(saved)
	if err != nil {
		return err
	}
	return w.completeImportRepair(id, document)
}

// Both synchronous callers and the viewer commit repairs through this operation.
// Validate against the current unresolved reference before changing the workspace.
func (w *Workspace) completeImportRepair(id string, document *Document) error {
	index := slices.IndexFunc(w.unresolved, func(entry savedImport) bool { return entry.ID == id })
	if index < 0 {
		return fmt.Errorf("unresolved import no longer exists")
	}
	saved := w.unresolved[index]
	if document == nil || document.Fingerprint != saved.Fingerprint {
		return fmt.Errorf("file changed; locate the original or remove this reference and import the replacement")
	}
	saved.Path = document.Path
	if err := w.attachSavedImport(saved, document); err != nil {
		return err
	}
	w.edited()
	w.unresolved = slices.Delete(w.unresolved, index, index+1)
	w.orderSavedDocuments()
	if w.selection == nil {
		return w.restoreSelection(w.deferredSelection)
	}
	return nil
}

func (w *Workspace) savedJourney(id string) bool {
	if w.checkpoint == nil {
		return false
	}
	return slices.ContainsFunc(w.checkpoint.manifest.Journeys, func(j savedJourney) bool { return j.ID == id })
}

// Existing document order also controls scene layering; restoration must not
// regroup imported activities and journeys or move a repaired import to the end.
func (w *Workspace) orderSavedDocuments() {
	if w.checkpoint == nil {
		return
	}
	positions := make(map[string]int)
	for i, id := range w.checkpoint.manifest.Order {
		positions[id] = i
	}
	position := func(id string) int {
		if i, ok := positions[id]; ok {
			return i
		}
		return len(positions)
	}
	slices.SortStableFunc(w.documents, func(a, b *workspaceDocument) int {
		return position(a.saveID) - position(b.saveID)
	})
}

// Older saves may have an imported activity selected while a journey was being
// edited. Prefer a populated saved journey over creating another empty draft.
func (w *Workspace) restoredPlannerRef() ItemRef {
	var fallback, populated ItemRef
	for _, item := range w.items() {
		j := item.Item.Journey
		if j == nil {
			continue
		}
		if w.checkpoint != nil {
			entry, _, err := w.lookup(item.Ref)
			if err == nil && entry.itemIDs[item.Ref.ItemID] == w.checkpoint.manifest.Planner {
				return item.Ref
			}
		}
		if fallback == (ItemRef{}) {
			fallback = item.Ref
		}
		if populated == (ItemRef{}) {
			for _, stop := range j.Stops {
				if stop.Location != nil {
					populated = item.Ref
					break
				}
			}
		}
	}
	if w.selection != nil {
		if _, err := w.journey(w.selection.ref); err == nil {
			return w.selection.ref
		}
	}
	if populated != (ItemRef{}) {
		return populated
	}
	return fallback
}
