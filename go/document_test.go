package main

import (
	"bytes"
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
)

func TestGPXDocumentPreservesSourceAndNamedItems(t *testing.T) {
	body := []byte(`<gpx><metadata><name>Weekend collection</name></metadata>
<wpt lat="51" lon="0"><name>Meeting point</name></wpt>
<trk><name>Morning ride</name><trkseg><trkpt lat="51" lon="0"/><trkpt lat="51" lon="0.001"/></trkseg><trkseg><trkpt lat="51" lon="0.1"/></trkseg></trk>
<rte><name>Way home</name><rtept lat="51" lon="0.2"/><rtept lat="51" lon="0.201"/></rte>
<trk><name>Evening ride</name><trkseg><trkpt lat="51" lon="0.3"/></trkseg></trk></gpx>`)
	path := importFile(t, "rides.data", body)
	d, err := loadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceMetadataLost := d.Path != abs ||
		d.Filename != "rides.data" ||
		d.Format != "GPX" ||
		d.Name != "Weekend collection"
	if sourceMetadataLost {
		t.Fatalf("lost source metadata: %+v", d)
	}
	if len(d.Items) != 4 {
		t.Fatalf("expected four independent items, got %d", len(d.Items))
	}
	for i, want := range []struct{ name, kind string }{
		{name: "Meeting point", kind: "waypoint"},
		{name: "Morning ride", kind: "track"},
		{name: "Way home", kind: "route"},
		{name: "Evening ride", kind: "track"},
	} {
		item := d.Items[i]
		identityLost := item.ID != i+1 || item.Track.Name != want.name || item.Kind != want.kind
		if identityLost {
			t.Fatalf(
				"item %d lost its identity: %+v, name %q",
				i,
				item,
				item.Track.Name,
			)
		}
	}
	if len(d.Items[1].Track.Segments) != 2 ||
		d.Items[1].Track.Points[2].Distance != d.Items[1].Track.Points[1].Distance {
		t.Fatal("a disconnected part became a new item or acquired distance")
	}
	for _, item := range d.Items[1:] {
		if item.Track.Points[0].Distance != 0 || item.Track.Points[0].Segment != 0 {
			t.Fatal("item geometry uses another item's distance or indices")
		}
	}
	combined := d.track()
	geometryCountsChanged := combined.summary()["route_count"] != 1 ||
		len(combined.Points) != 6 ||
		len(combined.Segments) != 4 ||
		len(combined.Waypoints) != 1
	if geometryCountsChanged {
		t.Fatal("single-file snapshot changed geometry or entity counts")
	}
	itemBoundariesChanged := combined.Points[3].Lon != 0.2 ||
		combined.Points[5].Lon != 0.3 ||
		combined.Points[3].Distance != combined.Points[2].Distance
	if itemBoundariesChanged {
		t.Fatal("single-file snapshot reordered points or connected items")
	}
	other, err := loadDocument(importFile(t, "rides.data", body))
	sourcesIndistinguishable := err != nil || other.Filename != d.Filename || other.Path == d.Path
	if sourcesIndistinguishable {
		t.Fatal("equally named source files cannot be distinguished", err)
	}
}

const coursesWithGapTCX = `<TrainingCenterDatabase><Courses>
<Course><Name>Outward</Name><Track>
<Trackpoint><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0</LongitudeDegrees></Position></Trackpoint>
<Trackpoint><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0.001</LongitudeDegrees></Position></Trackpoint>
<Trackpoint/>
<Trackpoint><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0.1</LongitudeDegrees></Position></Trackpoint>
<Trackpoint><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0.101</LongitudeDegrees></Position></Trackpoint>
</Track><CoursePoint><Name>Water</Name><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0.1</LongitudeDegrees></Position></CoursePoint></Course>
<Course><Name>Return</Name><Track>
<Trackpoint><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0.2</LongitudeDegrees></Position></Trackpoint>
<Trackpoint><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0.201</LongitudeDegrees></Position></Trackpoint>
</Track></Course></Courses></TrainingCenterDatabase>`

func TestTCXDocumentCountsCoursesRatherThanGPSParts(t *testing.T) {
	d, err := loadDocument(importFile(t, "courses.tcx", []byte(coursesWithGapTCX)))
	if err != nil {
		t.Fatal(err)
	}
	courseIdentityLost := len(d.Items) != 2 ||
		d.Items[0].Track.Name != "Outward" ||
		d.Items[1].Track.Name != "Return"
	if courseIdentityLost {
		t.Fatal("course identity was lost")
	}
	first, second := d.Items[0].Track, d.Items[1].Track
	courseCountsChanged := first.summary()["route_count"] != 1 ||
		second.summary()["route_count"] != 1 ||
		d.track().summary()["route_count"] != 2
	if courseCountsChanged {
		t.Fatal("GPS gaps were counted as additional courses")
	}
	boundaryDistancesChanged := len(first.Segments) != 2 ||
		first.Points[2].Distance != first.Points[1].Distance ||
		second.Points[0].Distance != 0
	if boundaryDistancesChanged {
		t.Fatal("GPS gaps or course boundaries acquired distance")
	}
	coursePointOwnershipLost := len(first.Waypoints) != 1 ||
		len(second.Waypoints) != 0 ||
		first.Waypoints[0].Name != "Water"
	if coursePointOwnershipLost {
		t.Fatal("course points lost their owning course")
	}
	d, err = loadDocument(importFile(t, "activity.tcx", []byte(activityTCX)))
	if err != nil {
		t.Fatal(err)
	}
	activityOwnershipChanged := len(d.Items) != 2 ||
		d.Items[0].Kind != "activity" ||
		d.Items[0].Track.Name != "2026-10-01T10:00:00Z" ||
		len(d.Items[0].Track.Segments) != 3 ||
		d.Items[1].Kind != "route"
	if activityOwnershipChanged {
		t.Fatal("activity/lap/course ownership changed")
	}
}

func TestFITDocumentKeepsSessionsWithinTheirActivityAndSeparatesSequences(t *testing.T) {
	file := proto.FIT{Messages: []proto.Message{
		mesgdef.NewFileId(nil).SetType(typedef.FileActivity).ToMesg(nil),
		mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0).ToMesg(nil),
		mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0.001).ToMesg(nil),
		mesgdef.NewSession(nil).SetSport(typedef.SportCycling).ToMesg(nil),
		mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0.1).ToMesg(nil),
		mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0.101).ToMesg(nil),
		mesgdef.NewSession(nil).SetSport(typedef.SportRunning).ToMesg(nil),
	}}
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(&file); err != nil {
		t.Fatal(err)
	}
	body := append(buf.Bytes(), syntheticFIT(t, true)...)
	d, err := loadDocument(importFile(t, "chained.fit", body))
	if err != nil {
		t.Fatal(err)
	}
	sequenceIdentityLost := len(d.Items) != 2 ||
		d.Items[0].Kind != "activity" ||
		d.Items[1].Kind != "route" ||
		d.Items[1].Track.Name != "Synthetic course"
	if sequenceIdentityLost {
		t.Fatal("FIT sequences did not retain independent activity/course identity")
	}
	activity, course := d.Items[0].Track, d.Items[1].Track
	sequenceBoundariesChanged := len(activity.Segments) != 2 ||
		activity.Points[2].Distance != activity.Points[1].Distance ||
		len(course.Segments) != 3 ||
		course.Points[0].Distance != 0
	if sequenceBoundariesChanged {
		t.Fatal("session, pause or sequence boundaries changed")
	}
	courseOwnershipLost := len(activity.Waypoints) != 0 ||
		len(course.Waypoints) != 1 ||
		d.track().summary()["route_count"] != 1
	if courseOwnershipLost {
		t.Fatal("FIT course points or route count lost entity ownership")
	}
}

func TestKMLDocumentPreservesPlacemarksAndLateContainerNames(t *testing.T) {
	body := []byte(`<kml><Document><Folder><Folder>
<Placemark><name>Paths</name><MultiGeometry>
<LineString><coordinates>0,51 0.001,51</coordinates></LineString>
<MultiGeometry><LineString><coordinates>0.1,51 0.101,51</coordinates></LineString></MultiGeometry>
</MultiGeometry></Placemark><name>Inner</name></Folder><name>Outer</name></Folder>
<Folder><name>Other</name><Placemark><name>Pin</name><Point><coordinates>0.2,51</coordinates></Point></Placemark></Folder>
<Placemark><name>Root pin</name><Point><coordinates>0.3,51</coordinates></Point></Placemark>
<name>Collection</name></Document></kml>`)
	d, err := loadDocument(importFile(t, "map.kml", body))
	if err != nil {
		t.Fatal(err)
	}
	placemarkIdentityLost := d.Name != "Collection" ||
		len(d.Items) != 3 ||
		d.Items[0].Track.Name != "Paths" ||
		d.Items[1].Track.Name != "Pin"
	if placemarkIdentityLost {
		t.Fatal("document or placemark identity was lost")
	}
	for i, want := range [][]string{{"Inner", "Outer", "Collection"}, {"Other", "Collection"}, {"Collection"}} {
		var names []string
		for group := d.Items[i].Group; group != nil; group = group.Parent {
			names = append(names, group.Name)
		}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf(
				"item %d container path: got %v, want %v",
				i,
				names,
				want,
			)
		}
	}
	multiGeometryOwnershipLost := len(d.Items[0].Track.Segments) != 2 ||
		d.track().summary()["route_count"] != 1 ||
		len(d.Items[1].Track.Waypoints) != 1
	if multiGeometryOwnershipLost {
		t.Fatal("multi-geometry parts became separate placemarks or routes")
	}
	d, err = loadDocument(importFile(t, "timed.kml", []byte(mapKML)))
	if err != nil {
		t.Fatal(err)
	}
	timedPartsChanged := len(d.Items) != 3 ||
		d.Items[2].Track.Name != "Recorded ride" ||
		len(d.Items[2].Track.Segments) != 3 ||
		d.Items[2].Track.Points[0].Time == nil ||
		d.Items[2].Track.Points[2].Time != nil
	if timedPartsChanged {
		t.Fatal("timed multi-track placemark lost parts or missing measurements")
	}
}

func TestDocumentRecalculationAndSnapshotsPreserveIndependentGeometry(t *testing.T) {
	d, err := loadDocument(importFile(t, "courses.tcx", []byte(coursesWithGapTCX)))
	if err != nil {
		t.Fatal(err)
	}
	original := d.track()
	if err := d.prepare(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, d.track()) {
		t.Fatal("recalculating unchanged geometry changed its measurements")
	}
	first, second := d.Items[0].Track, d.Items[1].Track
	secondSummary := second.summary()
	first.Points[1].Lon = 0.002
	if err := d.prepare(); err != nil {
		t.Fatal(err)
	}
	wantDistance := haversine(first.Points[0], first.Points[1]) + haversine(first.Points[2], first.Points[3])
	editedGeometryStale := math.Abs(first.Distance-wantDistance) > 1e-9 ||
		first.Points[1].World != project(0.002, 51) ||
		first.Points[2].Distance != first.Points[1].Distance
	if editedGeometryStale {
		t.Fatal("edited geometry accumulated old distance or retained stale projection")
	}
	unrelatedItemChanged := !reflect.DeepEqual(second.summary(), secondSummary) ||
		d.Items[0].ID != 1 ||
		d.Items[1].ID != 2
	if unrelatedItemChanged {
		t.Fatal("recalculation changed another item or reassigned identity")
	}
	combined := d.track()
	if combined.RouteCount != 2 || math.Abs(combined.Distance-first.Distance-second.Distance) > 1e-9 {
		t.Fatal("combined snapshot has stale measurements")
	}
	combined.Points[0].Lon = 10
	combined.Segments[0].Start = 1
	ownedGeometryChanged := first.Points[0].Lon != 0 ||
		first.Segments[0].Start != 0 ||
		second.Points[0].Distance != 0
	if ownedGeometryChanged {
		t.Fatal("combining geometry changed the owned items")
	}
}

func TestDocumentSkipsEmptyItemsAndRetainsWaypointOnlyGeometry(t *testing.T) {
	d, err := loadDocument(importFile(
		t,
		"pins.gpx",
		[]byte(`<gpx><trk><name>Empty</name></trk><wpt lat="51" lon="0"/></gpx>`),
	))
	if err != nil {
		t.Fatal(err)
	}
	waypointGeometryChanged := len(d.Items) != 1 ||
		d.Items[0].ID != 2 ||
		d.Items[0].Kind != "waypoint" ||
		d.Items[0].Track.Name == "" ||
		len(d.Items[0].Track.Waypoints) != 1 ||
		d.track().Distance != 0
	if waypointGeometryChanged {
		t.Fatal("empty items survived or a waypoint became a route")
	}
	if err := d.prepare(); err != nil || d.Items[0].ID != 2 {
		t.Fatal("preparation reassigned a surviving item's identity", err)
	}
}

func TestGPXDocumentRejectsNestedItemOwnership(t *testing.T) {
	for _, nested := range []string{"trk", "rte"} {
		body := `<gpx><trk><trkseg><` + nested + `></` + nested + `><trkpt lat="51" lon="0"/></trkseg></trk></gpx>`
		if _, err := loadDocument(importFile(t, "nested.gpx", []byte(body))); err == nil {
			t.Fatal("accepted overlapping GPX item ownership")
		}
	}
}
