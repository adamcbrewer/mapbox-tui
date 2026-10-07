package main

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
)

func TestXMLImportsAcceptLeadingUTF8BOM(t *testing.T) {
	for name, body := range map[string]string{
		"pin.gpx":  `<gpx><wpt lat="51" lon="0"/></gpx>`,
		"ride.tcx": activityTCX,
		"map.kml":  mapKML,
	} {
		t.Run(name, func(t *testing.T) {
			original, err := loadTrack(importFile(t, name, []byte(body)))
			if err != nil {
				t.Fatal(err)
			}
			for _, prefix := range []string{"\xef\xbb\xbf", "\xef\xbb\xbf<?xml version=\"1.0\" encoding=\"utf-8\"?>\n"} {
				track, err := loadTrack(importFile(t, name, []byte(prefix+body)))
				if err != nil || !reflect.DeepEqual(track, original) {
					t.Fatal("UTF-8 BOM changed or prevented XML import", err)
				}
			}
			for _, prefix := range []string{"unexpected", " \xef\xbb\xbf", "\xef\xbb\xbf\xef\xbb\xbf", "\xef\xbb\xbfunexpected"} {
				if _, err := loadTrack(importFile(t, name, []byte(prefix+body))); err == nil {
					t.Fatal("accepted invalid leading text or a misplaced BOM")
				}
			}
		})
	}
}

func TestFITRejectsMessagesCrossingSequenceBoundaries(t *testing.T) {
	encode := func(lon float64) []byte {
		file := proto.FIT{Messages: []proto.Message{
			mesgdef.NewFileId(nil).SetType(typedef.FileActivity).ToMesg(nil),
			mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(lon).ToMesg(nil),
		}}
		var buf bytes.Buffer
		if err := encoder.New(&buf).Encode(&file); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	first, second := encode(0), encode(0.001)
	valid := append(bytes.Clone(first), second...)
	track, err := loadTrack(importFile(t, "valid-chain.fit", valid))
	if err != nil || len(track.Points) != 2 || len(track.Segments) != 2 || track.Distance != 0 {
		t.Fatal("valid chained activities were lost or connected", err)
	}
	if len(second) > 255 {
		t.Fatal("synthetic field length exceeds FIT's one-byte size")
	}
	incomplete := []byte{0x42, 0, 0, 0xf4, 1, 1, 0, byte(len(second)), 0x0d, 0x02}
	malformed := append(bytes.Clone(first[:len(first)-2]), incomplete...)
	malformed = append(malformed, 0, 0)
	binary.LittleEndian.PutUint32(malformed[4:8], binary.LittleEndian.Uint32(first[4:8])+uint32(len(incomplete)))
	malformed = fitHeaderVariant(malformed, 14, false)
	joined := append(malformed, second...)
	if err := validateFITCRC(bytes.NewReader(joined)); err != nil {
		t.Fatal("structural regression fixture must have valid checksums", err)
	}
	if _, err := loadTrack(importFile(t, "crossing-chain.fit", joined)); err == nil {
		t.Fatal("incomplete field consumed the next activity despite valid checksums")
	}
}

func TestFITPreservesNativeEnhancedAltitude(t *testing.T) {
	for _, test := range []struct {
		name     string
		legacy   uint16
		enhanced uint32
		want     float64
		present  bool
	}{
		{"both fields", 2550, 2560, 12, true},
		{"enhanced beyond legacy range", 4464, 70000, 13500, true},
		{"enhanced only", basetype.Uint16Invalid, 70000, 13500, true},
		{"legacy only", 2550, basetype.Uint32Invalid, 10, true},
		{"literal zero elevation", 2500, basetype.Uint32Invalid, 0, true},
		{"enhanced raw zero", basetype.Uint16Invalid, 0, -500, true},
		{"missing elevation", basetype.Uint16Invalid, basetype.Uint32Invalid, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := proto.FIT{Messages: []proto.Message{
				mesgdef.NewFileId(nil).SetType(typedef.FileActivity).ToMesg(nil),
				mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0).SetAltitude(test.legacy).SetEnhancedAltitude(test.enhanced).ToMesg(nil),
			}}
			var buf bytes.Buffer
			if err := encoder.New(&buf).Encode(&file); err != nil {
				t.Fatal(err)
			}
			track, err := loadTrack(importFile(t, "altitude.fit", buf.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			p := track.Points[0]
			if (p.Elevation != nil) != test.present || (p.Elevation != nil && *p.Elevation != test.want) {
				t.Fatalf("native elevation changed: got %v, want %v (present %v)", p.Elevation, test.want, test.present)
			}
		})
	}
}

func TestTCXEmptyCoordinatesPreserveGPSGaps(t *testing.T) {
	for _, position := range []string{
		``, `<Position/>`,
		`<Position><LatitudeDegrees>51</LatitudeDegrees></Position>`,
		`<Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees/></Position>`,
		"<Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees> \n\t </LongitudeDegrees></Position>",
		`<Position><LongitudeDegrees>0.001</LongitudeDegrees></Position>`,
		`<Position><LatitudeDegrees/><LongitudeDegrees>0.001</LongitudeDegrees></Position>`,
		"<Position><LatitudeDegrees> \n\t </LatitudeDegrees><LongitudeDegrees>0.001</LongitudeDegrees></Position>",
	} {
		body := `<TrainingCenterDatabase><Activities><Activity><Lap><Track>
<Trackpoint><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0</LongitudeDegrees></Position></Trackpoint>
<Trackpoint>` + position + `</Trackpoint>
<Trackpoint><Position><LatitudeDegrees>51.01</LatitudeDegrees><LongitudeDegrees>0.02</LongitudeDegrees></Position></Trackpoint>
</Track></Lap></Activity></Activities></TrainingCenterDatabase>`
		track, err := loadTrack(importFile(t, "empty-coordinates.tcx", []byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		if len(track.Points) != 2 || len(track.Segments) != 2 || track.Distance != 0 {
			t.Fatalf("empty coordinates invented positions or a connecting path: %s", position)
		}
	}
}

func TestTCXEmptyElevationStaysMissingAndZeroRemainsValid(t *testing.T) {
	for _, test := range []struct {
		elevation string
		present   bool
	}{
		{"", false}, {"<AltitudeMeters/>", false},
		{"<AltitudeMeters> \n\t </AltitudeMeters>", false},
		{"<AltitudeMeters>0</AltitudeMeters>", true},
	} {
		position := `<Position><LatitudeDegrees>0</LatitudeDegrees><LongitudeDegrees>0</LongitudeDegrees></Position>`
		body := `<TrainingCenterDatabase><Activities><Activity><Lap><Track><Trackpoint>` + position + test.elevation + `</Trackpoint></Track></Lap></Activity></Activities><Courses><Course><CoursePoint>` + position + test.elevation + `</CoursePoint></Course></Courses></TrainingCenterDatabase>`
		track, err := loadTrack(importFile(t, "empty-elevation.tcx", []byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		if len(track.Points) != 1 || len(track.Waypoints) != 1 {
			t.Fatal("literal zero coordinates were treated as missing")
		}
		for _, p := range []Point{track.Points[0], track.Waypoints[0]} {
			if p.Lat != 0 || p.Lon != 0 || (p.Elevation != nil) != test.present || (p.Elevation != nil && *p.Elevation != 0) {
				t.Fatal("empty elevation became zero or a recorded zero was lost")
			}
		}
	}
}

func TestKMLContainerTitlesPreserveFolderNames(t *testing.T) {
	placemark := `<Placemark><name>Start</name><Point><coordinates>0,51</coordinates></Point></Placemark>`
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"folder only", `<Folder><name>My ride</name>` + placemark + `</Folder>`, "My ride"},
		{"nested folder", `<Folder><name>Outer</name><Folder><name>Inner</name>` + placemark + `</Folder></Folder>`, "Outer"},
		{"outer name after inner", `<Folder><Folder><name>Inner</name>` + placemark + `</Folder><name>Outer</name></Folder>`, "Outer"},
		{"unnamed document", `<Document><Folder><name>Outer</name><Folder><name>Inner</name>` + placemark + `</Folder></Folder></Document>`, "Outer"},
		{"named document", `<Document><name>Document</name><Folder><name>Folder</name>` + placemark + `</Folder></Document>`, "Document"},
		{"document name after folder", `<Document><Folder><name>Folder</name>` + placemark + `</Folder><name>Document</name></Document>`, "Document"},
		{"title matches fallback", `<Folder><name>Imported KML</name>` + placemark + `</Folder>`, "Imported KML"},
		{"placemark fallback", `<Folder>` + placemark + `</Folder>`, "Start"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `<kml xmlns="http://earth.google.com/kml/2.1">` + test.body + `</kml>`
			track, err := loadTrack(importFile(t, "folder.kml", []byte(body)))
			if err != nil {
				t.Fatal(err)
			}
			if track.Name != test.want || len(track.Waypoints) != 1 || track.Waypoints[0].Name != "Start" {
				t.Fatal("container or placemark name was lost", track.Name)
			}
		})
	}
}
