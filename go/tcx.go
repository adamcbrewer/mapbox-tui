package main

import "encoding/xml"

type tcxPoint struct {
	Name      string   `xml:"Name"`
	Time      string   `xml:"Time"`
	Elevation *float64 `xml:"AltitudeMeters"`
	Position  *struct {
		Lat *float64 `xml:"LatitudeDegrees"`
		Lon *float64 `xml:"LongitudeDegrees"`
	} `xml:"Position"`
}

func (p tcxPoint) point() (Point, bool) {
	if p.Position == nil || p.Position.Lat == nil || p.Position.Lon == nil {
		return Point{}, false
	}
	return Point{Lon: *p.Position.Lon, Lat: *p.Position.Lat, Elevation: p.Elevation, Time: optionalTime(p.Time), Name: p.Name}, true
}

func parseTCX(d *xml.Decoder) (*Track, error) {
	t := &Track{Name: "Imported TCX"}
	err := xmlTokens(d, "TrainingCenterDatabase", func(v xml.StartElement, parent string) (bool, error) {
		switch {
		case (v.Name.Local == "Name" && parent == "Course") || (v.Name.Local == "Id" && parent == "Activity"):
			var name string
			if err := d.DecodeElement(&name, &v); err != nil {
				return true, err
			}
			if name != "" {
				t.Name = name
			}
			return true, nil
		case v.Name.Local == "Track" && (parent == "Lap" || parent == "Course"):
			var track struct {
				Points []tcxPoint `xml:"Trackpoint"`
			}
			if err := d.DecodeElement(&track, &v); err != nil {
				return true, err
			}
			kind := "track"
			if parent == "Course" {
				kind = "route"
			}
			segment := -1
			for _, raw := range track.Points {
				p, ok := raw.point()
				if !ok {
					segment = -1
					continue
				}
				t.appendPoint(p, &segment, kind)
			}
			return true, nil
		case v.Name.Local == "CoursePoint" && parent == "Course":
			var raw tcxPoint
			if err := d.DecodeElement(&raw, &v); err != nil {
				return true, err
			}
			if p, ok := raw.point(); ok {
				t.Waypoints = append(t.Waypoints, p)
			}
			return true, nil
		}
		return false, nil
	})
	return t, err
}
