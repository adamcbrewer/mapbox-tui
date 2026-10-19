package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/kit/hash/crc16"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
)

func importFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const activityTCX = `<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2">
<Activities><Activity Sport="Biking"><Id>2026-10-01T10:00:00Z</Id><Lap><Track>
<Trackpoint><Time>2026-10-01T10:00:00Z</Time><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0</LongitudeDegrees></Position><AltitudeMeters>10</AltitudeMeters></Trackpoint>
<Trackpoint><Time>2026-10-01T10:00:10Z</Time><Position><LatitudeDegrees>51</LatitudeDegrees><LongitudeDegrees>0.001</LongitudeDegrees></Position></Trackpoint>
<Trackpoint><Time>2026-10-01T10:00:15Z</Time></Trackpoint>
<Trackpoint><Position><LatitudeDegrees>51.01</LatitudeDegrees><LongitudeDegrees>0.02</LongitudeDegrees></Position></Trackpoint>
</Track></Lap><Lap><Track><Trackpoint><Position><LatitudeDegrees>51.02</LatitudeDegrees><LongitudeDegrees>0.03</LongitudeDegrees></Position></Trackpoint></Track></Lap></Activity></Activities>
<Courses><Course><Name>Course &amp; activity</Name><Track>
<Trackpoint><Position><LatitudeDegrees>51.03</LatitudeDegrees><LongitudeDegrees>0.04</LongitudeDegrees></Position></Trackpoint>
<Trackpoint><Position><LatitudeDegrees>51.03</LatitudeDegrees><LongitudeDegrees>0.041</LongitudeDegrees></Position></Trackpoint>
</Track><CoursePoint><Name>Water</Name><Position><LatitudeDegrees>51.03</LatitudeDegrees><LongitudeDegrees>0.04</LongitudeDegrees></Position><AltitudeMeters>12</AltitudeMeters></CoursePoint></Course></Courses>
</TrainingCenterDatabase>`

const mapKML = `<kml xmlns="http://www.opengis.net/kml/2.2" xmlns:gx="http://www.google.com/kml/ext/2.2"><Document><name>Map export</name><Folder>
<Placemark><name>Water</name><Point><coordinates>0,51,10</coordinates></Point></Placemark>
<Placemark><name>Paths</name><MultiGeometry><LineString><coordinates>0,51,10 0.001,51</coordinates></LineString><MultiGeometry><LineString><coordinates>0.02,51.01 0.021,51.01</coordinates></LineString></MultiGeometry></MultiGeometry></Placemark>
<Placemark><name>Recorded ride</name><gx:MultiTrack><gx:Track>
<when>2026-10-01T10:00:00Z</when><when>2026-10-01T10:00:10Z</when><when>2026-10-01T10:00:15Z</when><when/>
<gx:coord>0.03 51.02 12</gx:coord><gx:coord>0.031 51.02 13</gx:coord><gx:coord/><gx:coord>0.04 51.03 14</gx:coord>
</gx:Track><gx:Track><when>2026-10-01T10:01:00Z</when><gx:coord>0.05 51.04 15</gx:coord></gx:Track></gx:MultiTrack></Placemark>
</Folder></Document></kml>`

func TestTCXPreservesActivitiesCoursesAndGPSGaps(t *testing.T) {
	track, err := loadTrack(importFile(t, "ride.TCX", []byte(activityTCX)))
	if err != nil {
		t.Fatal(err)
	}
	if track.Name != "Course & activity" || len(track.Points) != 6 || len(track.Segments) != 4 || len(track.Waypoints) != 1 || track.Segments[3].Kind != "route" {
		t.Fatalf("lost TCX geometry: %+v", track)
	}
	if track.Points[2].Distance != track.Points[1].Distance || track.Points[3].Distance != track.Points[2].Distance || track.Points[4].Distance != track.Points[3].Distance {
		t.Fatal("TCX distance includes GPS, lap or course gaps")
	}
	if track.Points[0].Elevation == nil || *track.Points[0].Elevation != 10 || track.Points[1].Elevation != nil || track.Points[2].Time != nil || track.Waypoints[0].Name != "Water" || track.Waypoints[0].Segment != -1 {
		t.Fatal("TCX invented or lost measurements or waypoints")
	}
	if newPlayback(track).Mode != "mixed time/estimate" || track.Points[0].World != project(0, 51) {
		t.Fatal("TCX was not prepared for playback and rendering")
	}
}

func TestKMLPreservesNestedGeometryAndTimedTracks(t *testing.T) {
	track, err := loadTrack(importFile(t, "map.KML", []byte(mapKML)))
	if err != nil {
		t.Fatal(err)
	}
	if track.Name != "Map export" || len(track.Points) != 8 || len(track.Segments) != 5 || len(track.Waypoints) != 1 {
		t.Fatalf("lost KML geometry: %+v", track)
	}
	for _, index := range []int{2, 4, 6, 7} {
		if track.Points[index].Distance != track.Points[index-1].Distance {
			t.Fatal("KML connected separate geometries or missing positions")
		}
	}
	if track.Points[0].Lat != 51 || track.Points[0].Lon != 0 || *track.Points[0].Elevation != 10 || track.Points[1].Elevation != nil || track.Points[0].Time != nil || track.Points[6].Time != nil {
		t.Fatal("KML changed coordinate order or invented measurements")
	}
	if track.Segments[0].Kind != "route" || track.Segments[2].Kind != "track" || *track.Points[4].Time != "2026-10-01T10:00:00Z" || track.Waypoints[0].Name != "Water" {
		t.Fatal("KML lost track timing, kinds or placemark names")
	}
}

func TestKMLWaypointOnlyAndPolygonOutlines(t *testing.T) {
	for _, test := range []struct {
		body      string
		points    int
		segments  int
		waypoints int
	}{
		{`<kml><Placemark><name>Pin</name><Point><coordinates>-0.1,51.5</coordinates></Point></Placemark></kml>`, 0, 0, 1},
		{`<kml><Placemark><Polygon><outerBoundaryIs><LinearRing><coordinates>0,51 0.01,51 0,51</coordinates></LinearRing></outerBoundaryIs><innerBoundaryIs><LinearRing><coordinates>0.001,51 0.002,51 0.001,51</coordinates></LinearRing></innerBoundaryIs></Polygon></Placemark></kml>`, 6, 2, 0},
	} {
		track, err := loadTrack(importFile(t, "map.xml", []byte(test.body)))
		if err != nil {
			t.Fatal(err)
		}
		if len(track.Points) != test.points || len(track.Segments) != test.segments || len(track.Waypoints) != test.waypoints {
			t.Fatal("lost KML pins or polygon boundaries")
		}
		if len(track.Points) > 3 && track.Points[3].Distance != track.Points[2].Distance {
			t.Fatal("connected polygon exterior and interior")
		}
	}
}

func TestKMLAcceptsWhitespaceAroundCoordinateCommas(t *testing.T) {
	for _, test := range []struct {
		coordinates string
		elevation   bool
	}{
		{"0, 51", false}, {" 0 , 51 ", false},
		{"0,51, 10", true}, {"0 ,\n51 ,\t10", true}, {"0e0,\r\n5.1e1 ,\n1e1", true},
	} {
		t.Run(test.coordinates, func(t *testing.T) {
			body := `<kml xmlns="http://earth.google.com/kml/2.1"><Folder><Placemark><Point><coordinates>` + test.coordinates + `</coordinates></Point><LineString><coordinates>` + test.coordinates + ` 0.001 , 51 , 11</coordinates></LineString></Placemark></Folder></kml>`
			track, err := loadTrack(importFile(t, "spaced.kml", []byte(body)))
			if err != nil {
				t.Fatal(err)
			}
			if len(track.Waypoints) != 1 || len(track.Points) != 2 || len(track.Segments) != 1 || track.Distance <= 0 {
				t.Fatal("spaces split a coordinate or joined separate tuples")
			}
			for _, p := range []Point{track.Waypoints[0], track.Points[0]} {
				if p.Lon != 0 || p.Lat != 51 || (p.Elevation != nil) != test.elevation || (p.Elevation != nil && *p.Elevation != 10) {
					t.Fatal("coordinate values or missing elevation changed")
				}
			}
			if track.Points[1].Lon != 0.001 || track.Points[1].Lat != 51 || *track.Points[1].Elevation != 11 {
				t.Fatal("line string lost the next coordinate tuple")
			}
		})
	}
}

func syntheticFIT(t *testing.T, course bool, options ...encoder.Option) []byte {
	t.Helper()
	stamp := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	kind := typedef.FileActivity
	if course {
		kind = typedef.FileCourse
	}
	file := proto.FIT{Messages: []proto.Message{
		mesgdef.NewFileId(nil).SetType(kind).SetManufacturer(typedef.ManufacturerDevelopment).SetTimeCreated(stamp).ToMesg(nil),
	}}
	if course {
		file.Messages = append(file.Messages, mesgdef.NewCourse(nil).SetName("Synthetic course").ToMesg(nil))
	}
	file.Messages = append(file.Messages,
		mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0).SetAltitudeScaled(10).SetTimestamp(stamp).ToMesg(nil),
		mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0.001).SetEnhancedAltitudeScaled(12).SetTimestamp(stamp.Add(10*time.Second)).ToMesg(nil),
		mesgdef.NewEvent(nil).SetEvent(typedef.EventTimer).SetEventType(typedef.EventTypeStopAll).SetTimestamp(stamp.Add(11*time.Second)).ToMesg(nil),
		mesgdef.NewEvent(nil).SetEvent(typedef.EventTimer).SetEventType(typedef.EventTypeStart).SetTimestamp(stamp.Add(20*time.Second)).ToMesg(nil),
		mesgdef.NewRecord(nil).SetPositionLatDegrees(51.01).SetPositionLongDegrees(0.02).SetTimestamp(stamp.Add(20*time.Second)).ToMesg(nil),
		mesgdef.NewRecord(nil).SetTimestamp(stamp.Add(21*time.Second)).ToMesg(nil),
		mesgdef.NewRecord(nil).SetPositionLatDegrees(51.02).SetPositionLongDegrees(0.03).ToMesg(nil),
		mesgdef.NewCoursePoint(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0).SetName("Water").SetTimestamp(stamp).ToMesg(nil),
	)
	var buf bytes.Buffer
	if err := encoder.New(&buf, options...).Encode(&file); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fitHeaderVariant(body []byte, size byte, omitCRC bool) []byte {
	data := bytes.Clone(body)
	if size == 12 {
		data = append(bytes.Clone(data[:12]), data[14:]...)
	}
	data[0] = size
	crc := crc16.New()
	if size == 14 {
		crc.Write(data[:12])
		binary.LittleEndian.PutUint16(data[12:14], crc.Sum16())
		if omitCRC {
			clear(data[12:14])
		}
	}
	crc.Reset()
	crc.Write(data[:len(data)-2])
	binary.LittleEndian.PutUint16(data[len(data)-2:], crc.Sum16())
	return data
}

func TestFITAcceptsOptionalHeaderCRCVariants(t *testing.T) {
	for _, options := range [][]encoder.Option{nil, {encoder.WithBigEndian(), encoder.WithProtocolVersion(proto.V2), encoder.WithHeaderOption(encoder.HeaderOptionCompressedTimestamp, 3)}} {
		body := syntheticFIT(t, false, options...)
		original, err := loadTrack(importFile(t, "original.fit", body))
		if err != nil {
			t.Fatal(err)
		}
		var chained []byte
		for _, variant := range []struct {
			name string
			size byte
			omit bool
		}{{"12-byte header", 12, false}, {"14-byte header", 14, false}, {"omitted header CRC", 14, true}} {
			t.Run(variant.name, func(t *testing.T) {
				data := fitHeaderVariant(body, variant.size, variant.omit)
				track, err := loadTrack(importFile(t, "header.fit", data))
				if err != nil || !reflect.DeepEqual(track.summary(), original.summary()) {
					t.Fatal("valid header variant failed or changed imported data", err)
				}
				chained = append(chained, data...)
			})
		}
		track, err := loadTrack(importFile(t, "mixed-headers.fit", chained))
		if err != nil || len(track.Points) != 12 || len(track.Segments) != 9 {
			t.Fatal("chained files with different header variants failed", err)
		}
		for _, i := range []int{4, 8} {
			if track.Points[i].Distance != track.Points[i-1].Distance {
				t.Fatal("chained header variants acquired a connecting route")
			}
		}
	}
}

func TestFITOptionalHeaderCRCKeepsIntegrityAndStructureChecks(t *testing.T) {
	body := fitHeaderVariant(syntheticFIT(t, false), 14, true)
	badFooter := bytes.Clone(body)
	badFooter[len(badFooter)-1] ^= 1
	badPayload := bytes.Clone(body)
	badPayload[20] ^= 1
	badHeader := fitHeaderVariant(body, 14, false)
	badHeader[12] ^= 1
	crc := crc16.New()
	crc.Write(badHeader[:len(badHeader)-2])
	binary.LittleEndian.PutUint16(badHeader[len(badHeader)-2:], crc.Sum16())
	badChained := append(bytes.Clone(body), badFooter...)
	for _, data := range [][]byte{badFooter, badPayload, badHeader, badChained} {
		if _, err := loadTrack(importFile(t, "bad-crc.fit", data)); !errors.Is(err, decoder.ErrCRCChecksumMismatch) {
			t.Fatal("corrupt checksum was accepted or misreported", err)
		}
	}
	wrongSize := bytes.Clone(body)
	binary.LittleEndian.PutUint32(wrongSize[4:8], binary.LittleEndian.Uint32(body[4:8])+1)
	for _, data := range [][]byte{nil, body[:5], body[:11], body[:13], body[:len(body)-3], body[:len(body)-2], body[:len(body)-1], append(bytes.Clone(body), 1), wrongSize} {
		if _, err := loadTrack(importFile(t, "truncated.fit", data)); err == nil {
			t.Fatal("omitted header CRC allowed truncated data, trailing bytes or an invalid size")
		}
	}
	undefinedMessage := append(bytes.Clone(body[:14]), 0, 1, 0, 0)
	binary.LittleEndian.PutUint32(undefinedMessage[4:8], 2)
	undefinedMessage = fitHeaderVariant(undefinedMessage, 14, true)
	if _, err := loadTrack(importFile(t, "missing-definition.fit", undefinedMessage)); !errors.Is(err, decoder.ErrMesgDefMissing) {
		t.Fatal("valid CRC bypassed message structure checks", err)
	}
}

func TestFITActivitiesCoursesAndEncodingVariants(t *testing.T) {
	for _, course := range []bool{false, true} {
		for _, options := range [][]encoder.Option{nil, {encoder.WithBigEndian(), encoder.WithProtocolVersion(proto.V2), encoder.WithHeaderOption(encoder.HeaderOptionCompressedTimestamp, 3)}} {
			track, err := loadTrack(importFile(t, "ride.FIT", syntheticFIT(t, course, options...)))
			if err != nil {
				t.Fatal(err)
			}
			if len(track.Points) != 4 || len(track.Segments) != 3 || len(track.Waypoints) != 1 {
				t.Fatal("lost FIT records or pause/GPS boundaries")
			}
			kind := "track"
			if course {
				kind = "route"
				if track.Name != "Synthetic course" {
					t.Fatal("lost FIT course name")
				}
			}
			if track.Segments[0].Kind != kind || math.Abs(track.Points[0].Lat-51) > 1e-6 || math.Abs(track.Points[1].Lon-0.001) > 1e-6 {
				t.Fatal("incorrect FIT kind or semicircle conversion")
			}
			if *track.Points[0].Elevation != 10 || *track.Points[1].Elevation != 12 || track.Points[2].Elevation != nil || track.Points[3].Time != nil || *track.Points[0].Time != "2026-10-01T10:00:00Z" {
				t.Fatal("incorrect FIT scaling, invalid sentinels or epoch")
			}
			if track.Points[2].Distance != track.Points[1].Distance || track.Points[3].Distance != track.Points[2].Distance || track.Waypoints[0].Name != "Water" {
				t.Fatal("FIT connected gaps or lost course points")
			}
		}
	}
}

func TestFITChecksIntegrityAndChainedFiles(t *testing.T) {
	body := syntheticFIT(t, false)
	joined := append(bytes.Clone(body), body...)
	track, err := loadTrack(importFile(t, "chained.bin", joined))
	if err != nil || len(track.Points) != 8 || len(track.Segments) != 6 || track.Points[4].Distance != track.Points[3].Distance {
		t.Fatal("FIT chained files failed or were connected", err)
	}
	corrupt := bytes.Clone(body)
	corrupt[len(corrupt)-1] ^= 1
	for _, data := range [][]byte{body[:10], body[:len(body)-1], corrupt, append(bytes.Clone(body), 1, 2, 3)} {
		if _, err := loadTrack(importFile(t, "bad.fit", data)); err == nil {
			t.Fatal("accepted truncated, corrupt or trailing FIT data")
		}
	}
	for _, file := range []proto.FIT{
		{Messages: []proto.Message{mesgdef.NewFileId(nil).SetType(typedef.FileWorkout).ToMesg(nil)}},
		{Messages: []proto.Message{mesgdef.NewFileId(nil).SetType(typedef.FileActivity).ToMesg(nil), mesgdef.NewRecord(nil).SetTimestamp(time.Now()).ToMesg(nil)}},
	} {
		var buf bytes.Buffer
		if err := encoder.New(&buf).Encode(&file); err != nil {
			t.Fatal(err)
		}
		if _, err := loadTrack(importFile(t, "no-gps.fit", buf.Bytes())); err == nil {
			t.Fatal("accepted unsupported FIT type or file without GPS")
		}
	}
}

func TestXMLImportsRejectInvalidGeometryAndDocuments(t *testing.T) {
	for _, body := range []string{
		`<other/>`, `<TrainingCenterDatabase/>`, `<kml/>`, `<kml><Placemark>`,
		`<kml><Placemark><Point><coordinates>NaN,51</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><Point><coordinates>0,91</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><Point><coordinates>0,51,Inf</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><Point><coordinates>0,51 1,52</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><Point><coordinates>0, 51 1, 52</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><Point><coordinates>0,51,</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><Point><coordinates>0,,10</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><Point><coordinates>0,51,,10</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><Point><coordinates>0,5 1</coordinates></Point></Placemark></kml>`,
		`<kml><Placemark><LineString><coordinates>179,0 -179,0</coordinates></LineString></Placemark></kml>`,
		`<kml xmlns:gx="http://www.google.com/kml/ext/2.2"><Placemark><gx:Track><when>bad</when><gx:coord>0 51 0</gx:coord></gx:Track></Placemark></kml>`,
		`<kml xmlns:gx="http://www.google.com/kml/ext/2.2"><Placemark><gx:Track><when>2026-10-01T10:00:00Z</when><gx:coord>0 51 0</gx:coord><gx:coord>1 51 0</gx:coord></gx:Track></Placemark></kml>`,
		mapKML + `<kml/>`, activityTCX + `<TrainingCenterDatabase/>`, mapKML + `unexpected`,
		strings.Replace(activityTCX, `<LongitudeDegrees>0</LongitudeDegrees>`, `<LongitudeDegrees>NaN</LongitudeDegrees>`, 1),
		strings.Replace(activityTCX, `<AltitudeMeters>10</AltitudeMeters>`, `<AltitudeMeters>Inf</AltitudeMeters>`, 1),
		strings.Replace(activityTCX, `<Time>2026-10-01T10:00:00Z</Time>`, `<Time>invalid</Time>`, 1),
	} {
		if _, err := loadTrack(importFile(t, "bad.xml", []byte(body))); err == nil {
			t.Fatalf("accepted invalid import: %s", body)
		}
	}
}

func TestImportedFormatsWorkThroughMCPAndViewer(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	for name, body := range map[string][]byte{"ride.tcx": []byte(activityTCX), "map.kml": []byte(mapKML), "ride.fit": syntheticFIT(t, false)} {
		t.Run(name, func(t *testing.T) {
			path := importFile(t, name, body)
			track, err := loadTrack(path)
			if err != nil {
				t.Fatal(err)
			}
			s := &Service{providers: map[string]*Provider{"none": newProvider("none"), "mapbox": newProvider("mapbox")}}
			result, err := s.call(context.Background(), "load_track", Args{Path: path})
			if err != nil {
				t.Fatal(err)
			}
			loadedTrack := result.(map[string]any)
			id := loadedTrack["track_id"].(string)
			summary, err := s.call(context.Background(), "summarize_track", Args{TrackID: id})
			if err != nil || !reflect.DeepEqual(summary, track.summary()) {
				t.Fatal("MCP import differs from shared loader", err)
			}
			index := 0
			if _, err := s.call(context.Background(), "inspect_track", Args{TrackID: id, PointIndex: &index}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.call(context.Background(), "render_map", Args{TrackID: id, Provider: "none"}); err != nil {
				t.Fatal(err)
			}
			m := &viewer{
				provider: newProvider("none"),
				ctx:      context.Background(),
				width:    120,
				height:   40,
			}
			m.initUI()
			batch := m.load(path)().(tea.BatchMsg)
			for _, cmd := range batch {
				if msg, ok := cmd().(loaded); ok {
					m.Update(msg)
				}
			}
			importNotInitialised := m.activeTrack() == nil ||
				m.path != path ||
				m.activePlayback() == nil ||
				m.visibleProfile() == nil ||
				m.fatal != nil ||
				m.loading
			if importNotInitialised {
				t.Fatal("viewer failed to initialise imported file")
			}
			m.openDialog(dialogFiles)
			if !strings.Contains(strings.Join(m.ui.picker.AllowedTypes, " "), filepath.Ext(name)) {
				t.Fatal("viewer picker excludes imported format")
			}
			m.Update(loaded{id: m.loadID, err: os.ErrInvalid})
			failedImportLostRoute := m.activeTrack() == nil ||
				m.path != path ||
				!strings.Contains(m.ui.notice, "Could not open track file")
			if failedImportLostRoute {
				t.Fatal("failed import discarded the existing route")
			}
		})
	}
}

func importPointLimitFixture(t *testing.T, format string, counts []int) []byte {
	t.Helper()
	var body strings.Builder
	switch format {
	case "gpx", "gpx-route":
		body.WriteString("<gpx>")
		for _, count := range counts {
			if format == "gpx-route" {
				body.WriteString("<rte>" + strings.Repeat(`<rtept lat="51" lon="0"/>`, count) + "</rte>")
				continue
			}
			body.WriteString("<trk><trkseg>" + strings.Repeat(`<trkpt lat="51" lon="0"/>`, count/2))
			body.WriteString("</trkseg><trkseg>" + strings.Repeat(`<trkpt lat="51" lon="0"/>`, count-count/2))
			body.WriteString("</trkseg></trk>")
		}
		body.WriteString("</gpx>")
	case "tcx":
		body.WriteString("<TrainingCenterDatabase><Courses>")
		point := "<Trackpoint><Position><LatitudeDegrees>51</LatitudeDegrees>" +
			"<LongitudeDegrees>0</LongitudeDegrees></Position></Trackpoint>"
		for _, count := range counts {
			body.WriteString("<Course><Track>" + strings.Repeat(point, count/2) + "</Track><Track>")
			body.WriteString(strings.Repeat(point, count-count/2) + "</Track></Course>")
		}
		body.WriteString("</Courses></TrainingCenterDatabase>")
	case "kml-track", "kml-waypoints":
		body.WriteString(`<kml xmlns:gx="http://www.google.com/kml/ext/2.2">`)
		for _, count := range counts {
			body.WriteString("<Placemark>")
			if format == "kml-track" {
				body.WriteString("<gx:Track>" + strings.Repeat("<gx:coord>0 51</gx:coord>", count) + "</gx:Track>")
			} else {
				body.WriteString("<MultiGeometry>" + strings.Repeat("<Point><coordinates>0,51</coordinates></Point>", count))
				body.WriteString("</MultiGeometry>")
			}
			body.WriteString("</Placemark>")
		}
		body.WriteString("</kml>")
	case "kml":
		body.WriteString("<kml>")
		for _, count := range counts {
			body.WriteString("<Placemark><MultiGeometry><LineString><coordinates>")
			body.WriteString(strings.Repeat("0,51 ", count/2))
			body.WriteString("</coordinates></LineString><LineString><coordinates>")
			body.WriteString(strings.Repeat("0,51 ", count-count/2))
			body.WriteString("</coordinates></LineString></MultiGeometry></Placemark>")
		}
		body.WriteString("</kml>")
	case "fit", "fit-waypoints":
		var out bytes.Buffer
		record := mesgdef.NewRecord(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0).ToMesg(nil)
		if format == "fit-waypoints" {
			record = mesgdef.NewCoursePoint(nil).SetPositionLatDegrees(51).SetPositionLongDegrees(0).ToMesg(nil)
		}
		for _, count := range counts {
			file := proto.FIT{Messages: []proto.Message{
				mesgdef.NewFileId(nil).SetType(typedef.FileActivity).ToMesg(nil),
			}}
			for range count {
				file.Messages = append(file.Messages, record)
			}
			if err := encoder.New(&out).Encode(&file); err != nil {
				t.Fatal(err)
			}
		}
		return out.Bytes()
	default:
		t.Fatalf("unknown fixture format %q", format)
	}
	return []byte(body.String())
}

func TestImportedRecordingPointLimit(t *testing.T) {
	for _, format := range []string{"gpx", "gpx-route", "tcx", "kml", "kml-track", "kml-waypoints", "fit", "fit-waypoints"} {
		t.Run(format, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				counts []int
				reject bool
			}{
				{name: "at limit", counts: []int{100000}},
				{name: "over limit", counts: []int{100001}, reject: true},
				{name: "independent recordings", counts: []int{60000, 60000}},
				{name: "reject entire document", counts: []int{1, 100001}, reject: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					path := importFile(t, "limit."+format, importPointLimitFixture(t, format, tc.counts))
					d, err := loadDocument(path)
					if tc.reject {
						if err == nil || d != nil || !strings.Contains(err.Error(), "100000") {
							t.Fatal("oversized recording was accepted or lost its limit error", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if len(d.Items) != len(tc.counts) {
						t.Fatal("import merged independent recordings")
					}
					for i, count := range tc.counts {
						if len(d.Items[i].Track.Points)+len(d.Items[i].Track.Waypoints) != count {
							t.Fatal("import dropped recorded points")
						}
					}
				})
			}
		})
	}
}

func TestImportedRecordingLimitIncludesWaypoints(t *testing.T) {
	for _, format := range []string{"tcx", "kml"} {
		t.Run(format, func(t *testing.T) {
			body := string(importPointLimitFixture(t, format, []int{99999}))
			waypoint := "<CoursePoint><Position><LatitudeDegrees>51</LatitudeDegrees>" +
				"<LongitudeDegrees>0</LongitudeDegrees></Position></CoursePoint>"
			end := "</Course>"
			if format == "kml" {
				waypoint = "<Point><coordinates>0,51</coordinates></Point>"
				end = "</MultiGeometry>"
			}
			for _, count := range []int{1, 2} {
				t.Run(strconv.Itoa(count)+" waypoints", func(t *testing.T) {
					data := strings.Replace(body, end, strings.Repeat(waypoint, count)+end, 1)
					d, err := loadDocument(importFile(t, "limit."+format, []byte(data)))
					if count == 2 {
						if err == nil || d != nil || !strings.Contains(err.Error(), "100000") {
							t.Fatal("waypoints bypassed the recording limit", err)
						}
						return
					}
					if err != nil || len(d.Items[0].Track.Waypoints) != 1 || len(d.Items[0].Track.Points) != 99999 {
						t.Fatal("recording at the combined limit lost geometry", err)
					}
				})
			}
		})
	}
}
