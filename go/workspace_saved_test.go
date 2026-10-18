package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func saveTestWorkspace(t *testing.T, w *Workspace, store workspaceStore, saveAs bool) *workspaceCheckpoint {
	t.Helper()
	snapshot, err := w.snapshot("Weekend / café", fit(nil, 80, 24, 2), saveAs)
	if err != nil {
		t.Fatal(err)
	}
	expected := w.checkpoint
	if saveAs {
		expected = nil
	}
	checkpoint, err := store.save(snapshot, expected)
	if err != nil {
		t.Fatal(err)
	}
	w.checkpoint = checkpoint
	return checkpoint
}

func TestSavedWorkspaceRoundTrip(t *testing.T) {
	var w Workspace
	ref := openWorkspaceFile(t, &w, "../fixtures/example.gpx")
	openWorkspaceFile(t, &w, importFile(t, "courses.tcx", []byte(coursesWithGapTCX)))
	journey := w.newJourney()
	j, err := w.journey(journey)
	if err != nil {
		t.Fatal(err)
	}
	j.Stops = []journeyStop{
		{ID: 8, Location: &placeLocation{Name: "Café", Lon: 0, Lat: 51, Source: placeSourceMapboxSearch, ProviderID: "place-id", Attribution: "© Mapbox"}},
		{ID: 9},
		{ID: 11, Location: &placeLocation{Name: "End", Lon: .01, Lat: 51, Source: placeSourceCoordinates}},
	}
	j.Mode, j.Busy = journeyDriving, true
	j.Route = &journeyRoute{Distance: 999, Duration: 111}
	j.Error = "transient error"
	w.newJourney()
	w.documents[0].colors[ref.ItemID] = 5
	if err := w.setVisible(w.items()[0].Ref, false); err != nil {
		t.Fatal(err)
	}
	if err := w.selectItem(ref); err != nil {
		t.Fatal(err)
	}
	if err := w.seek(3); err != nil {
		t.Fatal(err)
	}
	w.selection.playback.Playing = true
	checkpoint := saveTestWorkspace(t, &w, workspaceStore{directory: t.TempDir()}, false)
	restored, _, err := prepareWorkspace(checkpoint.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.documents) != 4 || len(restored.unresolved) != 0 {
		t.Fatal("lost documents", restored)
	}
	if restored.selection.playback.Playing || restored.position().Index != 3 {
		t.Fatal("playback was not restored paused")
	}
	if restored.documents[0].colors[ref.ItemID] != 5 {
		t.Fatal("lost palette slot")
	}
	restoredJourney := restored.documents[2].document.Items[0].Journey
	if restoredJourney.Mode != journeyDriving || restoredJourney.Stops[1].Location != nil {
		t.Fatal("lost incomplete journey intent")
	}
	if restoredJourney.Route != nil || restoredJourney.Busy || len(restoredJourney.history) != 0 || restoredJourney.Error != "" {
		t.Fatal("transient routing state persisted")
	}
	if !reflect.DeepEqual(restoredJourney.Stops[0].Location, j.Stops[0].Location) {
		t.Fatal("lost stop provenance or attribution")
	}
	j.Stops[0].Location.Name = "changed favourite copy"
	if restoredJourney.Stops[0].Location.Name != "Café" {
		t.Fatal("stop aliased source")
	}
	data, err := os.ReadFile(checkpoint.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"transient error", "Distance", "Duration", "Busy", "requestID"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("runtime data in save", forbidden)
		}
	}
	if filepath.Base(checkpoint.path) != checkpoint.manifest.ID+".json" {
		t.Fatal("name became path")
	}
}

func TestSavedWorkspaceMissingChangedAndRepair(t *testing.T) {
	source := []byte(`<gpx><trk><name>Old</name><trkseg><trkpt lat="51" lon="0"/><trkpt lat="51.1" lon="0.1"/></trkseg></trk></gpx>`)
	path := importFile(t, "source.gpx", source)
	var w Workspace
	openWorkspaceFile(t, &w, path)
	if err := w.seek(1); err != nil {
		t.Fatal(err)
	}
	store := workspaceStore{directory: t.TempDir()}
	checkpoint := saveTestWorkspace(t, &w, store, false)
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(source), "Old", "New")), 0600); err != nil {
		t.Fatal(err)
	}
	partial, _, err := prepareWorkspace(checkpoint.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(partial.documents) != 0 || len(partial.unresolved) != 1 || partial.selection != nil {
		t.Fatal("changed file received old state")
	}
	if err := partial.locateUnresolved(partial.unresolved[0].ID, path); err == nil {
		t.Fatal("accepted changed import")
	}
	next := saveTestWorkspace(t, partial, store, false)
	if !reflect.DeepEqual(next.manifest.Imports, checkpoint.manifest.Imports) {
		t.Fatal("partial save lost reference")
	}
	if next.manifest.Selection == nil || next.manifest.Selection.Point != 1 {
		t.Fatal("partial save lost selection")
	}
	moved := importFile(t, "moved.gpx", source)
	if err := partial.locateUnresolved(partial.unresolved[0].ID, moved); err != nil {
		t.Fatal(err)
	}
	if len(partial.unresolved) != 0 || partial.position().Index != 1 {
		t.Fatal("repair failed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	missing, _, err := prepareWorkspace(checkpoint.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.unresolved) != 1 {
		t.Fatal("missing reference lost")
	}
	if err := missing.removeUnresolved(missing.unresolved[0].ID); err != nil {
		t.Fatal(err)
	}
	if missing.deferredSelection != nil {
		t.Fatal("removed reference left dangling selection")
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatal("removed original source", err)
	}
}

func TestSavedWorkspaceConflictsAndSaveAs(t *testing.T) {
	var first Workspace
	openWorkspaceFile(t, &first, "../fixtures/example.gpx")
	store := workspaceStore{directory: t.TempDir()}
	initial := saveTestWorkspace(t, &first, store, false)
	second, _, err := prepareWorkspace(initial.path)
	if err != nil {
		t.Fatal(err)
	}
	saveTestWorkspace(t, &first, store, false)
	stale, err := second.snapshot("stale", fit(nil, 80, 24, 2), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.save(stale, second.checkpoint); err == nil {
		t.Fatal("stale writer overwrote newer save")
	}
	copy := saveTestWorkspace(t, second, store, true)
	if copy.manifest.ID == initial.manifest.ID {
		t.Fatal("Save As reused identity")
	}
	if err := os.WriteFile(copy.path, []byte("newer external format"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := second.snapshot("new", fit(nil, 80, 24, 2), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.save(snapshot, second.checkpoint); err == nil {
		t.Fatal("overwrote unreadable external edit")
	}
	data, err := os.ReadFile(copy.path)
	if err != nil || string(data) != "newer external format" {
		t.Fatal("external edit lost")
	}
	if err := os.Remove(copy.path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.save(snapshot, second.checkpoint); err == nil {
		t.Fatal("resurrected deleted checkpoint")
	}
}

func TestSavedWorkspaceValidationAndAtomicFailures(t *testing.T) {
	var w Workspace
	store := workspaceStore{directory: t.TempDir()}
	checkpoint := saveTestWorkspace(t, &w, store, false)
	original, err := os.ReadFile(checkpoint.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*savedWorkspace)
	}{
		{name: "newer", change: func(v *savedWorkspace) { v.Version++ }},
		{name: "identity", change: func(v *savedWorkspace) { v.ID = "../../escape" }},
		{name: "camera", change: func(v *savedWorkspace) { v.Camera.Lat = nil }},
		{name: "selection", change: func(v *savedWorkspace) { v.Selection = &savedSelection{Item: "missing-item", Point: 0} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var v savedWorkspace
			if err := json.Unmarshal(original, &v); err != nil {
				t.Fatal(err)
			}
			test.change(&v)
			data, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "invalid.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := prepareWorkspace(path); err == nil {
				t.Fatal("accepted invalid workspace")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", workspaceFileLimit+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkspace(path); err == nil {
		t.Fatal("accepted oversized workspace")
	}
	lock, err := os.OpenFile(checkpoint.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockLocalFile(lock); err != nil {
		t.Fatal(err)
	}
	defer unlockLocalFile(lock)
	v, err := w.snapshot("blocked", fit(nil, 80, 24, 2), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.save(v, checkpoint); err == nil {
		t.Fatal("ignored lock")
	}
	after, err := os.ReadFile(checkpoint.path)
	if err != nil || string(after) != string(original) {
		t.Fatal("failed write changed checkpoint")
	}
}

func TestSavedWorkspaceSwitchRejectsOldRouteResults(t *testing.T) {
	var old Workspace
	ref := old.newJourney()
	locations := []journeyStop{
		{ID: 1, Location: &placeLocation{Lon: 0, Lat: 51, Source: placeSourceCoordinates}},
		{ID: 2, Location: &placeLocation{Lon: .01, Lat: 51, Source: placeSourceCoordinates}},
	}
	if err := old.editJourneyIntent(ref, journeyIntent{Stops: locations, Mode: journeyDriving}); err != nil {
		t.Fatal(err)
	}
	request, err := old.beginJourneyRoute(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := saveTestWorkspace(t, &old, workspaceStore{directory: t.TempDir()}, false)
	candidate, _, err := prepareWorkspace(checkpoint.path)
	if err != nil {
		t.Fatal(err)
	}
	old.replace(candidate)
	if old.documents[0].id == ref.DocumentID {
		t.Fatal("runtime identity reused across switch")
	}
	newRef := old.items()[0].Ref
	if _, err := old.beginJourneyRoute(context.Background(), newRef); err != nil {
		t.Fatal(err)
	}
	if old.acceptJourneyRoute(journeyRouteResult{ref: request.ref, id: request.id, revision: request.revision}) {
		t.Fatal("accepted stale route")
	}
	if request.ctx.Err() == nil {
		t.Fatal("old route not cancelled")
	}
}

func TestSavedWorkspaceAllFormatsAndPlainRendering(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	var w Workspace
	for name, body := range map[string][]byte{
		"ride.tcx": []byte(activityTCX), "map.kml": []byte(mapKML), "ride.fit": syntheticFIT(t, false),
	} {
		openWorkspaceFile(t, &w, importFile(t, name, body))
	}
	cp := saveTestWorkspace(t, &w, workspaceStore{directory: t.TempDir()}, false)
	restored, _, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.documents) != 3 || len(restored.unresolved) != 0 {
		t.Fatal("formats failed to restore")
	}
	service := &Service{workspace: *restored, providers: map[string]*Provider{"mapbox": newProvider("none")}}
	result, err := service.call(context.Background(), "render_map", Args{Workspace: true, Provider: "none", ASCII: true})
	if err != nil {
		t.Fatal(err)
	}
	text := result.(map[string]any)["text"].(string)
	if strings.Contains(text, "\x1b") {
		t.Fatal("saved workspace introduced ANSI into shared rendering")
	}
}

func TestSavedWorkspaceConcurrentSaveAndExternalEdits(t *testing.T) {
	var w Workspace
	store := workspaceStore{directory: t.TempDir()}
	cp := saveTestWorkspace(t, &w, store, false)
	a, err := w.snapshot("first", fit(nil, 80, 24, 2), false)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Name = "second"
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, v := range []savedWorkspace{a, b} {
		go func() { <-start; _, err := store.save(v, cp); results <- err }()
	}
	close(start)
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent writers: %d successes", successes)
	}
	current, err := readWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	// A valid external edit without a revision bump must still conflict.
	data, err := os.ReadFile(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp.path, append(data, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	a.Revision++
	if _, err := store.save(a, current); err == nil {
		t.Fatal("digest failed to catch external edit")
	}
	// A temporary file left by an interrupted write isn't used as a checkpoint.
	if err := os.WriteFile(filepath.Join(store.directory, ".workspace-interrupted.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareWorkspace(cp.path); err != nil {
		t.Fatal("temporary write affected valid checkpoint", err)
	}
}

func TestSavedWorkspaceRelativePathsAndRemovedItems(t *testing.T) {
	directory := t.TempDir()
	data, err := os.ReadFile("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "original.gpx")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	var w Workspace
	openWorkspaceFile(t, &w, source)
	removed := w.items()[0].Ref
	if err := w.removeItem(removed); err != nil {
		t.Fatal(err)
	}
	v, err := w.snapshot("Relative", fit(nil, 80, 24, 2), true)
	if err != nil {
		t.Fatal(err)
	}
	v.Imports[0].Path = "original.gpx"
	cp, err := (workspaceStore{directory: directory}).save(v, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	restored, _, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.items()) != 3 || len(restored.unresolved) != 0 {
		t.Fatal("relative import failed or removed item resurrected")
	}
	for _, item := range restored.items() {
		if item.Item.ID == removed.ItemID {
			t.Fatal("removed item restored")
		}
	}
}

func TestSavedWorkspacePreservesInterleavedDocumentOrder(t *testing.T) {
	var w Workspace
	w.newJourney()
	source := importFile(t, "middle.gpx", []byte(`<gpx><wpt lat="51" lon="0"/></gpx>`))
	openWorkspaceFile(t, &w, source)
	w.newJourney()
	store := workspaceStore{directory: t.TempDir()}
	cp := saveTestWorkspace(t, &w, store, false)
	restored, _, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range restored.documents {
		if entry.saveID != w.documents[i].saveID {
			t.Fatal("document layering changed")
		}
	}
	moved := source + ".moved"
	if err := os.Rename(source, moved); err != nil {
		t.Fatal(err)
	}
	partial, _, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	next := saveTestWorkspace(t, partial, store, false)
	if !reflect.DeepEqual(next.manifest.Order, cp.manifest.Order) {
		t.Fatal("missing reference changed order")
	}
	if err := partial.locateUnresolved(partial.unresolved[0].ID, moved); err != nil {
		t.Fatal(err)
	}
	for i, entry := range partial.documents {
		if entry.saveID != w.documents[i].saveID {
			t.Fatal("repair changed layering")
		}
	}
}

func TestCompleteImportRepairValidatesBeforeChangingWorkspace(t *testing.T) {
	var w Workspace
	source := importFile(t, "original.gpx", []byte(`<gpx><wpt lat="51" lon="0"/></gpx>`))
	openWorkspaceFile(t, &w, source)
	cp := saveTestWorkspace(t, &w, workspaceStore{directory: t.TempDir()}, false)
	moved := source + ".moved.gpx"
	if err := os.Rename(source, moved); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	saved := candidate.unresolved[0]
	document, err := loadDocument(moved)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := document.Fingerprint
	document.Fingerprint = "changed"
	if err := candidate.completeImportRepair(saved.ID, document); err == nil {
		t.Fatal("changed document was accepted")
	}
	if len(candidate.documents) != 0 || len(candidate.unresolved) != 1 || candidate.deferredSelection == nil {
		t.Fatal("failed repair changed workspace state")
	}
	document.Fingerprint = fingerprint
	if err := candidate.completeImportRepair(saved.ID, document); err != nil {
		t.Fatal(err)
	}
	if len(candidate.unresolved) != 0 || candidate.selection == nil || candidate.documents[0].saveID != saved.ID {
		t.Fatal("repair did not restore saved identity and selection")
	}
	if candidate.documents[0].document.Path != moved {
		t.Fatal("repair lost replacement path")
	}
	if err := candidate.completeImportRepair(saved.ID, document); err == nil || len(candidate.documents) != 1 {
		t.Fatal("stale completion installed a duplicate import")
	}
}

func TestSavedWorkspaceRejectsReopenedUnresolvedPath(t *testing.T) {
	var w Workspace
	source := importFile(t, "original.gpx", []byte(`<gpx><wpt lat="51" lon="0"/></gpx>`))
	openWorkspaceFile(t, &w, source)
	store := workspaceStore{directory: t.TempDir()}
	cp := saveTestWorkspace(t, &w, store, false)
	moved := source + ".moved"
	if err := os.Rename(source, moved); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, source); err != nil {
		t.Fatal(err)
	}
	openWorkspaceFile(t, candidate, source)
	for _, saveAs := range []bool{false, true} {
		if _, err := candidate.snapshot("Duplicate", fit(nil, 80, 24, 2), saveAs); err == nil {
			t.Fatal("save accepted loaded and unresolved imports with the same path")
		}
	}
	unchanged, err := readWorkspace(cp.path)
	if err != nil || unchanged.digest != cp.digest || len(candidate.unresolved) != 1 || len(candidate.documents) != 1 {
		t.Fatal("rejected save changed the checkpoint or working copy")
	}
	if err := candidate.removeUnresolved(candidate.unresolved[0].ID); err != nil {
		t.Fatal(err)
	}
	saveTestWorkspace(t, candidate, store, false)
}

func TestCompleteImportRepairPreservesActivePlayback(t *testing.T) {
	var w Workspace
	source := importFile(t, "missing.gpx", []byte(`<gpx><wpt lat="51" lon="0"/></gpx>`))
	openWorkspaceFile(t, &w, source)
	openWorkspaceFile(t, &w, "../fixtures/example.gpx")
	cp := saveTestWorkspace(t, &w, workspaceStore{directory: t.TempDir()}, false)
	moved := source + ".moved.gpx"
	if err := os.Rename(source, moved); err != nil {
		t.Fatal(err)
	}
	candidate, _, err := prepareWorkspace(cp.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.seek(3); err != nil {
		t.Fatal(err)
	}
	candidate.selection.playback.toggle()
	selection := candidate.selection
	before := *selection.playback
	if err := candidate.locateUnresolved(candidate.unresolved[0].ID, moved); err != nil {
		t.Fatal(err)
	}
	if candidate.selection != selection || !reflect.DeepEqual(*selection.playback, before) {
		t.Fatal("repair changed the active selection or playback")
	}
}
