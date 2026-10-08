package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func openWorkspaceFile(t *testing.T, w *Workspace, path string) ItemRef {
	t.Helper()
	ref, err := w.open(path)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestWorkspaceAddsFilesWithActivePlaybackAndSharedProfiles(t *testing.T) {
	var w Workspace
	first := openWorkspaceFile(t, &w, "../fixtures/example.gpx")
	firstSelectionWrong := len(w.items()) != 4 ||
		w.selection.ref != first ||
		w.selection.track.Name != "Riverside exploration" ||
		len(w.selection.track.Points) == 0
	if firstSelectionWrong {
		t.Fatal("opening a file did not prefer its first track over standalone waypoints")
	}
	if err := w.seek(3); err != nil {
		t.Fatal(err)
	}
	w.selection.playback.toggle()
	oldSelection := w.selection
	oldProfile := w.profile(80, 5)
	firstSummary := w.selection.track.summary()
	second := openWorkspaceFile(t, &w, importFile(t, "courses.tcx", []byte(coursesWithGapTCX)))
	secondSelectionWrong := first.DocumentID == second.DocumentID ||
		len(w.items()) != 6 ||
		w.selection.ref != second ||
		w.selection.track.Name != "Outward"
	if secondSelectionWrong {
		t.Fatal("adding a file replaced existing data or failed to select the new item")
	}
	playbackStateRetained := oldSelection.playback.Playing ||
		w.selection.playback.Playing ||
		w.position().Index != 0
	if playbackStateRetained {
		t.Fatal("switching items retained playback state from the old item")
	}
	newProfile := w.profile(80, 5)
	profileIncomplete := newProfile == oldProfile || len(newProfile.Items) != 4 || !newProfile.Available
	if profileIncomplete {
		t.Fatal("shared profile omitted visible items or their elevation")
	}
	if w.profile(80, 5) != newProfile || w.profile(40, 3) == newProfile {
		t.Fatal("profile cache did not track its dimensions")
	}
	if err := w.selectItem(first); err != nil {
		t.Fatal(err)
	}
	firstItemChanged := w.position().Index != 0 ||
		w.selection.playback.Playing ||
		!reflect.DeepEqual(w.selection.track.summary(), firstSummary) ||
		!w.profile(80, 5).Available
	if firstItemChanged {
		t.Fatal("returning to an item changed its data or reused another item's state")
	}
}

func TestWorkspaceReopeningReusesLoadedPathAndRestoresVisibility(t *testing.T) {
	var w Workspace
	path := importFile(t, "courses.tcx", []byte(coursesWithGapTCX))
	ref := openWorkspaceFile(t, &w, path)
	original := w.selection.track
	duplicate, err := loadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range w.items() {
		if err := w.setVisible(item.Ref, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte("changed and no longer parseable"), 0600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	reopened := openWorkspaceFile(t, &w, relative)
	loadedFileChanged := reopened != ref || w.selection.track != original || len(w.items()) != 2
	if loadedFileChanged {
		t.Fatal("reopening reread or duplicated the loaded file")
	}
	for _, item := range w.items() {
		if !item.Visible {
			t.Fatal("reopening did not restore the document's visibility")
		}
	}
	if err := w.seek(1); err != nil {
		t.Fatal(err)
	}
	selection := w.selection
	duplicateReplacedState := w.addDocument(duplicate) != ref ||
		w.selection != selection ||
		w.position().Index != 1 ||
		len(w.items()) != 2
	if duplicateReplacedState {
		t.Fatal("a separately completed duplicate import replaced existing state")
	}
}

func TestWorkspaceFailedImportsAndUnknownReferencesLeaveStateUntouched(t *testing.T) {
	var w Workspace
	ref := openWorkspaceFile(t, &w, "../fixtures/example.gpx")
	w.profile(80, 5)
	if err := w.seek(3); err != nil {
		t.Fatal(err)
	}
	w.selection.playback.toggle()
	selection, profile := w.selection, w.profile(80, 5)
	items := w.items()
	bounds, _ := w.visibleBounds()
	for _, path := range []string{"../fixtures/invalid.gpx", filepath.Join(t.TempDir(), "missing.gpx")} {
		if _, err := w.open(path); err == nil {
			t.Fatal("invalid import succeeded")
		}
	}
	for _, unknown := range []ItemRef{
		{DocumentID: ref.DocumentID + 1, ItemID: ref.ItemID},
		{DocumentID: ref.DocumentID, ItemID: 999},
	} {
		for _, err := range []error{w.selectItem(unknown), w.setVisible(unknown, false), w.removeItem(unknown)} {
			if err == nil {
				t.Fatal("unknown item reference succeeded")
			}
		}
	}
	if w.removeDocument(ref.DocumentID+1) == nil || w.seek(999) == nil {
		t.Fatal("invalid document or point selection succeeded")
	}
	after, _ := w.visibleBounds()
	workspaceChanged := w.selection != selection ||
		w.profile(80, 5) != profile ||
		!w.selection.playback.Playing ||
		w.position().Index != 3 ||
		!reflect.DeepEqual(w.items(), items) ||
		after != bounds
	if workspaceChanged {
		t.Fatal("failed operation changed workspace data or active state")
	}
}

func TestWorkspaceClearsActiveStateWhenHiddenRemovedOrCleared(t *testing.T) {
	for _, action := range []string{"hide", "remove item", "remove document", "clear"} {
		t.Run(action, func(t *testing.T) {
			var w Workspace
			other := openWorkspaceFile(t, &w, "../fixtures/waypoints.gpx")
			ref := openWorkspaceFile(t, &w, importFile(t, "courses.tcx", []byte(coursesWithGapTCX)))
			w.profile(80, 5)
			old := w.selection
			old.playback.toggle()
			var err error
			switch action {
			case "hide":
				err = w.setVisible(ref, false)
			case "remove item":
				err = w.removeItem(ref)
			case "remove document":
				err = w.removeDocument(ref.DocumentID)
			case "clear":
				w.clear()
			}
			if err != nil {
				t.Fatal(err)
			}
			activeStateRetained := w.selection != nil || w.position() != nil || old.playback.Playing
			if activeStateRetained {
				t.Fatal("active state survived or another item was selected automatically")
			}
			profile := w.profile(80, 5)
			if action == "hide" || action == "remove item" {
				remainingProfileWrong := profile == nil ||
					len(profile.Items) != 1 ||
					profile.Items[0].Ref == ref ||
					profile.Items[0].Active
				if remainingProfileWrong {
					t.Fatal("remaining visible elevation was cleared or retained the old selection")
				}
			} else if profile != nil {
				t.Fatal("removed geometry retained its profile")
			}
			if action == "clear" {
				if len(w.items()) != 0 {
					t.Fatal("clear retained loaded items")
				}
			} else if err := w.selectItem(other); err != nil {
				t.Fatal("unrelated document was removed", err)
			}
			if action == "hide" {
				err := w.selectItem(ref)
				selectionNotRestored := err != nil || w.selection.ref != ref || w.selection.playback.Playing
				if selectionNotRestored {
					t.Fatal("selecting a hidden item did not restore a fresh paused selection", err)
				}
				for _, item := range w.items() {
					if item.Ref == ref && !item.Visible {
						t.Fatal("selected item is still hidden")
					}
				}
			}
		})
	}
}

func TestWorkspaceItemRemovalUpdatesBoundsAndNeverReusesReferences(t *testing.T) {
	var w Workspace
	path := importFile(t, "courses.tcx", []byte(coursesWithGapTCX))
	active := openWorkspaceFile(t, &w, path)
	other := w.items()[1].Ref
	selection := w.selection
	if err := w.setVisible(other, false); err != nil {
		t.Fatal(err)
	}
	profile := w.profile(80, 5)
	if err := w.removeItem(other); err != nil {
		t.Fatal(err)
	}
	items := w.items()
	inactiveRemovalChangedState := len(items) != 1 ||
		items[0].Document.Bounds != selection.track.Bounds ||
		w.selection != selection ||
		w.profile(80, 5) != profile
	if inactiveRemovalChangedState {
		t.Fatal("removing an inactive item left stale document bounds or reset active state")
	}
	if err := w.removeItem(active); err != nil {
		t.Fatal(err)
	}
	if len(w.documents) != 0 || w.selection != nil {
		t.Fatal("removing the last item retained its document")
	}
	second := openWorkspaceFile(t, &w, path)
	w.clear()
	third := openWorkspaceFile(t, &w, path)
	if active.DocumentID == second.DocumentID || second.DocumentID == third.DocumentID {
		t.Fatal("document IDs were reused after removal or clearing")
	}
	for _, stale := range []ItemRef{active, other, second} {
		if w.removeItem(stale) == nil || w.selection.ref != third {
			t.Fatal("a stale item reference affected a later import")
		}
	}
}

func TestWorkspaceSameFilenameAndLocalIDRemainIndependentAcrossDocuments(t *testing.T) {
	var w Workspace
	body := []byte(`<gpx><rte><name>Route</name><rtept lat="51" lon="0"/><rtept lat="51" lon="0.001"/></rte></gpx>`)
	first := openWorkspaceFile(t, &w, importFile(t, "ride.gpx", body))
	second := openWorkspaceFile(t, &w, importFile(t, "ride.gpx", body))
	documentsCollapsed := first.ItemID != second.ItemID ||
		first.DocumentID == second.DocumentID ||
		len(w.items()) != 2
	if documentsCollapsed {
		t.Fatal("document-local IDs or duplicate filenames collapsed separate files")
	}
	firstColor, secondColor := w.items()[0].Color, w.items()[1].Color
	coloursIndistinguishable := firstColor == secondColor || firstColor == 0 || secondColor == 0
	if coloursIndistinguishable {
		t.Fatal("independent activities share a colour")
	}
	if err := w.selectItem(first); err != nil {
		t.Fatal(err)
	}
	if w.items()[0].Color != firstColor || w.items()[1].Color != secondColor {
		t.Fatal("selecting an activity changed its colour")
	}
	if err := w.selectItem(second); err != nil {
		t.Fatal(err)
	}
	if err := w.setVisible(first, false); err != nil {
		t.Fatal(err)
	}
	selection := w.selection
	visibilityCrossedOwnership := w.items()[0].Visible || !w.items()[1].Visible || w.selection.ref != second
	if visibilityCrossedOwnership {
		t.Fatal("visibility crossed document ownership")
	}
	err := w.removeDocument(first.DocumentID)
	unrelatedRemovalChangedState := err != nil || len(w.items()) != 1 || w.selection != selection
	if unrelatedRemovalChangedState {
		t.Fatal("removing an unrelated file changed the active import", err)
	}
	if w.items()[0].Color != secondColor {
		t.Fatal("removing another activity reassigned the remaining colour")
	}
}

func TestWorkspaceFitsAllVisibleGeometryWithoutMergingDistantFiles(t *testing.T) {
	var w Workspace
	for _, lon := range []float64{-170, 170} {
		body := fmt.Sprintf(`<gpx><rte><rtept lat="51" lon="%f"/><rtept lat="51" lon="%f"/></rte></gpx>`, lon, lon+0.001)
		openWorkspaceFile(t, &w, importFile(t, "route.gpx", []byte(body)))
	}
	bounds, ok := w.visibleBounds()
	if !ok || bounds != [4]float64{-170, 51, 170.001, 51} {
		t.Fatal("workspace rejected or lost distant independent items", bounds)
	}
	selection := w.selection
	c := w.fitVisible(160, 50, 2)
	for _, item := range w.items() {
		if item.Item.Track.Distance > 1000 || item.Item.Track.Points[0].Distance != 0 {
			t.Fatal("workspace invented a connecting path between files")
		}
		for _, p := range item.Item.Track.Points {
			s := c.screen(p.World)
			outsideViewport := s.X < 0 || s.X >= 320 || s.Y < 0 || s.Y >= 200
			if outsideViewport {
				t.Fatal("fit omitted visible geometry", s)
			}
		}
	}
	narrow := w.fitVisible(80, 24, 2)
	if narrow.Zoom >= 0 {
		t.Fatal("wide bounds did not zoom out far enough for a narrow viewport")
	}
	for _, item := range w.items() {
		for _, p := range item.Item.Track.Points {
			s := narrow.screen(p.World)
			outsideViewport := s.X < 0 || s.X >= 160 || s.Y < 0 || s.Y >= 96
			if outsideViewport {
				t.Fatal("narrow fit omitted visible geometry")
			}
		}
	}
	first := w.items()[0].Ref
	if err := w.setVisible(first, false); err != nil {
		t.Fatal(err)
	}
	visible, _ := w.visibleBounds()
	hiddenGeometryAffectsFit := visible != selection.track.Bounds ||
		w.fitVisible(120, 40, 2).Zoom <= c.Zoom ||
		w.selection != selection
	if hiddenGeometryAffectsFit {
		t.Fatal("hidden geometry affected fit or fitting changed selection")
	}
	if err := w.setVisible(selection.ref, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.visibleBounds(); ok || w.fitVisible(120, 40, 2) != fit(
		nil,
		120,
		40,
		2,
	) {
		t.Fatal("empty visible workspace did not return the world view")
	}
	if err := w.setVisible(first, true); err != nil || w.selection != nil {
		t.Fatal("showing an item automatically selected it", err)
	}
}

func TestWorkspaceWaypointOnlyItemHasBoundsWithoutPlaybackOrProfile(t *testing.T) {
	var w Workspace
	ref := openWorkspaceFile(t, &w, importFile(t, "pin.gpx", []byte(`<gpx><wpt lat="0" lon="0"/></gpx>`)))
	bounds, ok := w.visibleBounds()
	w.selection.playback.toggle()
	waypointTreatedAsRoute := !ok ||
		bounds != [4]float64{} ||
		w.selection.ref != ref ||
		w.position() != nil ||
		w.profile(80, 5) != nil ||
		w.selection.playback.Playing ||
		w.seek(0) == nil
	if waypointTreatedAsRoute {
		t.Fatal("waypoint-only data was treated as an empty workspace or playable route")
	}
	if w.fitVisible(120, 40, 2).Zoom == 0 {
		t.Fatal("zero-valued bounds were mistaken for no visible geometry")
	}
}
