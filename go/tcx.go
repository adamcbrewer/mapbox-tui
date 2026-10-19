package main

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

type tcxFloat struct{ value *float64 }

func (f *tcxFloat) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var text string
	if err := d.DecodeElement(&text, &start); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		f.value = nil
		return nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return err
	}
	f.value = &value
	return nil
}

type tcxPoint struct {
	Name      string   `xml:"Name"`
	Time      string   `xml:"Time"`
	Elevation tcxFloat `xml:"AltitudeMeters"`
	Position  *struct {
		Lat tcxFloat `xml:"LatitudeDegrees"`
		Lon tcxFloat `xml:"LongitudeDegrees"`
	} `xml:"Position"`
}

func (p tcxPoint) point() (Point, bool) {
	if p.Position == nil || p.Position.Lat.value == nil || p.Position.Lon.value == nil {
		return Point{}, false
	}
	return Point{Lon: *p.Position.Lon.value, Lat: *p.Position.Lat.value, Elevation: p.Elevation.value, Time: optionalTime(p.Time), Name: p.Name}, true
}

func parseTCX(d *xml.Decoder) (*Document, error) {
	document := &Document{Name: "Imported TCX"}
	var t *Track
	var depth int
	err := xmlTokens(d, "TrainingCenterDatabase", func(v xml.StartElement, parents []string) (bool, error) {
		if len(parents) < depth {
			t = nil
		}
		parent := parents[len(parents)-1]
		switch {
		case v.Name.Local == "Activity" && parent == "Activities", v.Name.Local == "Course" && parent == "Courses":
			kind := "activity"
			if v.Name.Local == "Course" {
				kind = "route"
			}
			t = document.addItem(kind).Track
			depth = len(parents) + 1
		case (v.Name.Local == "Name" && parent == "Course") || (v.Name.Local == "Id" && parent == "Activity"):
			var name string
			if err := d.DecodeElement(&name, &v); err != nil {
				return true, err
			}
			if name != "" && t != nil {
				t.Name = name
				document.Name = name
			}
			return true, nil
		case v.Name.Local == "Track" && (parent == "Lap" || parent == "Course"):
			if t == nil {
				return false, fmt.Errorf("track outside TCX activity or course")
			}
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
				if err := t.appendImportedPoint(p, &segment, kind); err != nil {
					return true, err
				}
			}
			return true, nil
		case v.Name.Local == "CoursePoint" && parent == "Course":
			if t == nil {
				return false, fmt.Errorf("point outside TCX course")
			}
			var raw tcxPoint
			if err := d.DecodeElement(&raw, &v); err != nil {
				return true, err
			}
			if p, ok := raw.point(); ok {
				if err := t.appendImportedWaypoint(p); err != nil {
					return true, err
				}
			}
			return true, nil
		}
		return false, nil
	})
	return document, err
}
