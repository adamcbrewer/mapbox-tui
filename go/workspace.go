package main

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
)

type ItemRef struct {
	DocumentID int
	ItemID     int
}

type WorkspaceItem struct {
	Ref      ItemRef
	Document *Document
	Item     *DocumentItem
	Visible  bool
}

type workspaceDocument struct {
	id       int
	document *Document
	hidden   map[int]bool
}

type workspaceSelection struct {
	ref      ItemRef
	track    *Track
	playback *playback
	profile  *elevationProfile
}

type Workspace struct {
	documents []*workspaceDocument
	selection *workspaceSelection
	nextID    int
}

func (w *Workspace) open(path string) (ItemRef, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return ItemRef{}, err
	}
	for _, entry := range w.documents {
		if entry.document.Path == path {
			return w.activateDocument(entry), nil
		}
	}
	d, err := loadDocument(path)
	if err != nil {
		return ItemRef{}, err
	}
	return w.addDocument(d), nil
}

// addDocument takes ownership of a successfully prepared import.
func (w *Workspace) addDocument(d *Document) ItemRef {
	for _, entry := range w.documents {
		if entry.document.Path == d.Path {
			return w.activateDocument(entry)
		}
	}
	w.nextID++
	entry := &workspaceDocument{id: w.nextID, document: d}
	w.documents = append(w.documents, entry)
	return w.activateDocument(entry)
}

func (w *Workspace) activateDocument(entry *workspaceDocument) ItemRef {
	entry.hidden = nil
	if w.selection != nil && w.selection.ref.DocumentID == entry.id {
		return w.selection.ref
	}
	item := entry.document.Items[0]
	for _, candidate := range entry.document.Items {
		if len(candidate.Track.Points) > 0 {
			item = candidate
			break
		}
	}
	ref := ItemRef{entry.id, item.ID}
	w.activate(ref, item.Track)
	return ref
}

func (w *Workspace) items() []WorkspaceItem {
	var items []WorkspaceItem
	for _, entry := range w.documents {
		for _, item := range entry.document.Items {
			items = append(items, WorkspaceItem{ItemRef{entry.id, item.ID}, entry.document, item, !entry.hidden[item.ID]})
		}
	}
	return items
}

func (w *Workspace) lookup(ref ItemRef) (*workspaceDocument, int, error) {
	for _, entry := range w.documents {
		if entry.id == ref.DocumentID {
			for i, item := range entry.document.Items {
				if item.ID == ref.ItemID {
					return entry, i, nil
				}
			}
			break
		}
	}
	return nil, -1, fmt.Errorf("unknown workspace item %d:%d", ref.DocumentID, ref.ItemID)
}

func (w *Workspace) selectItem(ref ItemRef) error {
	entry, i, err := w.lookup(ref)
	if err != nil {
		return err
	}
	delete(entry.hidden, ref.ItemID)
	if w.selection == nil || w.selection.ref != ref {
		w.activate(ref, entry.document.Items[i].Track)
	}
	return nil
}

func (w *Workspace) activate(ref ItemRef, track *Track) {
	w.clearSelection()
	w.selection = &workspaceSelection{ref: ref, track: track, playback: newPlayback(track)}
}

func (w *Workspace) clearSelection() {
	if w.selection != nil {
		w.selection.playback.Playing = false
		w.selection.profile = nil
		w.selection = nil
	}
}

func (w *Workspace) setVisible(ref ItemRef, visible bool) error {
	entry, _, err := w.lookup(ref)
	if err != nil {
		return err
	}
	if visible {
		delete(entry.hidden, ref.ItemID)
		return nil
	}
	if entry.hidden == nil {
		entry.hidden = make(map[int]bool)
	}
	entry.hidden[ref.ItemID] = true
	if w.selection != nil && w.selection.ref == ref {
		w.clearSelection()
	}
	return nil
}

func (w *Workspace) removeItem(ref ItemRef) error {
	entry, i, err := w.lookup(ref)
	if err != nil {
		return err
	}
	if len(entry.document.Items) == 1 {
		return w.removeDocument(entry.id)
	}
	if w.selection != nil && w.selection.ref == ref {
		w.clearSelection()
	}
	entry.document.Items = slices.Delete(entry.document.Items, i, i+1)
	delete(entry.hidden, ref.ItemID)
	entry.document.Bounds = entry.document.Items[0].Track.Bounds
	for _, item := range entry.document.Items[1:] {
		extendBounds(&entry.document.Bounds, item.Track.Bounds)
	}
	return nil
}

func (w *Workspace) removeDocument(id int) error {
	for i, entry := range w.documents {
		if entry.id == id {
			if w.selection != nil && w.selection.ref.DocumentID == id {
				w.clearSelection()
			}
			w.documents = slices.Delete(w.documents, i, i+1)
			return nil
		}
	}
	return fmt.Errorf("unknown workspace document %d", id)
}

func (w *Workspace) clear() {
	w.clearSelection()
	w.documents = nil
}

func (w *Workspace) position() *ridePosition {
	if w.selection == nil {
		return nil
	}
	return w.selection.playback.position(w.selection.track)
}

func (w *Workspace) seek(index int) error {
	if w.selection == nil || index < 0 || index >= len(w.selection.track.Points) {
		return fmt.Errorf("point_index outside active item")
	}
	w.selection.playback.seek(index)
	return nil
}

func (w *Workspace) profile(width, height int) *elevationProfile {
	if w.selection == nil || len(w.selection.track.Points) == 0 || width <= 0 || height <= 0 {
		return nil
	}
	p := w.selection.profile
	if p == nil || p.Grid.Width != width || p.Grid.Height != height {
		p = newProfile(w.selection.track, width, height)
		w.selection.profile = p
	}
	return p
}

func (w *Workspace) visibleBounds() ([4]float64, bool) {
	var bounds [4]float64
	found := false
	for _, entry := range w.documents {
		for _, item := range entry.document.Items {
			if entry.hidden[item.ID] {
				continue
			}
			if !found {
				bounds = item.Track.Bounds
				found = true
			} else {
				extendBounds(&bounds, item.Track.Bounds)
			}
		}
	}
	return bounds, found
}

func (w *Workspace) fitVisible(width, height int, aspect float64) Camera {
	bounds, ok := w.visibleBounds()
	if !ok {
		return fit(nil, width, height, aspect)
	}
	return fitBounds(bounds, width, height, aspect)
}

func extendBounds(bounds *[4]float64, other [4]float64) {
	bounds[0] = math.Min(bounds[0], other[0])
	bounds[1] = math.Min(bounds[1], other[1])
	bounds[2] = math.Max(bounds[2], other[2])
	bounds[3] = math.Max(bounds[3], other[3])
}
