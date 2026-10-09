package main

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
)

type ItemRef struct {
	DocumentID int `json:"document_id"`
	ItemID     int `json:"item_id"`
}

type WorkspaceItem struct {
	Ref      ItemRef
	Document *Document
	Item     *DocumentItem
	Visible  bool
	Color    activityColor
}

type workspaceDocument struct {
	id       int
	document *Document
	hidden   map[int]bool
	colors   map[int]activityColor
}

type workspaceSelection struct {
	ref      ItemRef
	track    *Track
	playback *playback
}

type Workspace struct {
	documents []*workspaceDocument
	selection *workspaceSelection
	nextID    int
	nextColor int
	elevation *elevationProfile
}

func (w *Workspace) open(path string) (ItemRef, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return ItemRef{}, err
	}
	if entry := w.documentAtPath(path); entry != nil {
		return w.activateDocument(entry), nil
	}
	d, err := loadDocument(path)
	if err != nil {
		return ItemRef{}, err
	}
	return w.addDocument(d), nil
}

// addDocument takes ownership of a successfully prepared import.
func (w *Workspace) addDocument(d *Document) ItemRef {
	if entry := w.documentAtPath(d.Path); entry != nil {
		return w.activateDocument(entry)
	}
	w.nextID++
	entry := &workspaceDocument{id: w.nextID, document: d, colors: make(map[int]activityColor)}
	for _, item := range d.Items {
		entry.colors[item.ID] = activityColor(w.nextColor%activityColorCount + 1)
		w.nextColor++
	}
	w.documents = append(w.documents, entry)
	return w.activateDocument(entry)
}

func (w *Workspace) documentAtPath(path string) *workspaceDocument {
	if path == "" {
		return nil
	}
	for _, entry := range w.documents {
		if entry.document.Path == path {
			return entry
		}
	}
	return nil
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
	ref := ItemRef{DocumentID: entry.id, ItemID: item.ID}
	w.activate(ref, item.Track)
	return ref
}

func (w *Workspace) items() []WorkspaceItem {
	var items []WorkspaceItem
	for _, entry := range w.documents {
		for _, item := range entry.document.Items {
			items = append(items, WorkspaceItem{
				Ref:      ItemRef{DocumentID: entry.id, ItemID: item.ID},
				Document: entry.document,
				Item:     item,
				Visible:  !entry.hidden[item.ID],
				Color:    entry.colors[item.ID],
			})
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
	w.selection = &workspaceSelection{ref: ref, track: track}
	if entry, i, err := w.lookup(ref); err == nil && entry.document.Items[i].Journey != nil {
		return
	}
	w.selection.playback = newPlayback(track)
}

func (w *Workspace) clearSelection() {
	if w.selection != nil {
		if w.selection.playback != nil {
			w.selection.playback.Playing = false
		}
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

func (w *Workspace) setDocumentVisible(id int, visible bool) error {
	for _, entry := range w.documents {
		if entry.id == id {
			for _, item := range entry.document.Items {
				_ = w.setVisible(ItemRef{DocumentID: id, ItemID: item.ID}, visible)
			}
			return nil
		}
	}
	return fmt.Errorf("unknown workspace document %d", id)
}

func (w *Workspace) removeItem(ref ItemRef) error {
	entry, i, err := w.lookup(ref)
	if err != nil {
		return err
	}
	if len(entry.document.Items) == 1 {
		return w.removeDocument(entry.id)
	}
	if j := entry.document.Items[i].Journey; j != nil {
		j.cancelRoute()
	}
	if w.selection != nil && w.selection.ref == ref {
		w.clearSelection()
	}
	entry.document.Items = slices.Delete(entry.document.Items, i, i+1)
	delete(entry.hidden, ref.ItemID)
	delete(entry.colors, ref.ItemID)
	entry.document.Bounds = entry.document.Items[0].Track.Bounds
	for _, item := range entry.document.Items[1:] {
		extendBounds(&entry.document.Bounds, item.Track.Bounds)
	}
	return nil
}

func (w *Workspace) removeDocument(id int) error {
	for i, entry := range w.documents {
		if entry.id == id {
			for _, item := range entry.document.Items {
				if item.Journey != nil {
					item.Journey.cancelRoute()
				}
			}
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
	w.cancelJourneyRoutes()
	w.clearSelection()
	w.documents = nil
	w.elevation = nil
}

func (w *Workspace) cancelJourneyRoutes() {
	for _, item := range w.items() {
		if item.Item.Journey != nil {
			item.Item.Journey.cancelRoute()
		}
	}
}

func (w *Workspace) position() *ridePosition {
	if w.selection == nil || w.selection.playback == nil {
		return nil
	}
	return w.selection.playback.position(w.selection.track)
}

func (w *Workspace) seek(index int) error {
	outsideActiveItem := w.selection == nil || index < 0 || index >= len(w.selection.track.Points)
	if outsideActiveItem {
		return fmt.Errorf("point_index outside active item")
	}
	if w.selection.playback == nil {
		return fmt.Errorf("planned journeys have no recorded playback")
	}
	w.selection.playback.seek(index)
	return nil
}

func (w *Workspace) selectDocumentPoint(documentID, index int) error {
	if index >= 0 {
		for _, item := range w.items() {
			if item.Ref.DocumentID != documentID {
				continue
			}
			if index < len(item.Item.Track.Points) {
				if err := w.selectItem(item.Ref); err != nil {
					return err
				}
				return w.seek(index)
			}
			index -= len(item.Item.Track.Points)
		}
	}
	return fmt.Errorf("point_index outside track")
}

func (w *Workspace) profile(width, height int) *elevationProfile {
	items := w.profileItems()
	invalidSize := width <= 0 || height <= 0
	if len(items) == 0 || invalidSize {
		w.elevation = nil
		return nil
	}
	p := w.elevation
	needsGrid := p == nil || p.Grid.Width != width || p.Grid.Height != height
	if needsGrid || !slices.Equal(p.Items, items) {
		p = newProfile(items, width, height)
		w.elevation = p
	}
	return p
}

func (w *Workspace) profileItems() []sceneItem {
	items := w.sceneItems()
	return slices.DeleteFunc(items, func(item sceneItem) bool { return len(item.Track.Points) == 0 || item.Planned })
}

func (w *Workspace) visibleBounds() ([4]float64, bool) {
	var bounds [4]float64
	var found bool
	for _, entry := range w.documents {
		for _, item := range entry.document.Items {
			if entry.hidden[item.ID] || len(item.Track.Points)+len(item.Track.Waypoints) == 0 {
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
		return fit(
			nil,
			width,
			height,
			aspect,
		)
	}
	return fitBounds(
		bounds,
		width,
		height,
		aspect,
	)
}

func (w *Workspace) sceneItems() []sceneItem {
	var items []sceneItem
	for _, item := range w.items() {
		if item.Visible {
			scene := sceneItem{
				Ref:     item.Ref,
				Track:   item.Item.Track,
				Active:  w.selection != nil && w.selection.ref == item.Ref,
				Color:   item.Color,
				Planned: item.Item.Journey != nil,
			}
			if j := item.Item.Journey; j != nil {
				if location := j.Stops[0].Location; location != nil {
					start := project(location.Lon, location.Lat)
					scene.Start = &start
				}
				if location := j.Stops[len(j.Stops)-1].Location; location != nil {
					end := project(location.Lon, location.Lat)
					scene.End = &end
				}
			}
			items = append(items, scene)
		}
	}
	return items
}

func (w *Workspace) pick(c Camera, point XY) (ItemRef, int) {
	items := w.sceneItems()
	var cursor *XY
	if position := w.position(); position != nil {
		cursor = &position.World
	}
	cell := renderSceneCell(
		c,
		items,
		cursor,
		point,
	)
	if cell.owner != (ItemRef{}) {
		if cell.Text == 'W' {
			return cell.owner, -1
		}
		for _, item := range items {
			if item.Ref == cell.owner {
				index, _ := pickDistance(item.Track, c, point)
				return item.Ref, index
			}
		}
	}
	var ref ItemRef
	index, best, active := -1, 6.0, false
	for _, item := range items {
		i, distance := pickDistance(item.Track, c, point)
		for _, waypoint := range item.Track.Waypoints {
			p := c.screen(waypoint.World)
			d := math.Hypot(p.X-point.X, (p.Y-point.Y)*c.Aspect/2)
			if d < distance {
				i, distance = -1, d
			}
		}
		closer := distance < best
		tied := ref != (ItemRef{}) && distance == best
		preferredOnTie := item.Active || !active
		if closer || (tied && preferredOnTie) {
			ref, index, best, active = item.Ref, i, distance, item.Active
		}
	}
	return ref, index
}

func extendBounds(bounds *[4]float64, other [4]float64) {
	bounds[0] = math.Min(bounds[0], other[0])
	bounds[1] = math.Min(bounds[1], other[1])
	bounds[2] = math.Max(bounds[2], other[2])
	bounds[3] = math.Max(bounds[3], other[3])
}
