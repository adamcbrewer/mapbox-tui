package main

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var kmlCommaWhitespace = regexp.MustCompile(`\s*,\s*`)

func kmlTuples(text string) []string {
	return strings.Fields(kmlCommaWhitespace.ReplaceAllString(text, ","))
}

type kmlCoordinates struct {
	Coordinates string `xml:"coordinates"`
}

type kmlBoundary struct {
	Ring kmlCoordinates `xml:"LinearRing"`
}

type kmlGeometry struct {
	Points []kmlCoordinates `xml:"Point"`
	Lines  []kmlCoordinates `xml:"LineString"`
	Tracks []struct {
		Times  []string `xml:"when"`
		Coords []string `xml:"coord"`
	} `xml:"http://www.google.com/kml/ext/2.2 Track"`
	Polygons []struct {
		Outer kmlBoundary   `xml:"outerBoundaryIs"`
		Inner []kmlBoundary `xml:"innerBoundaryIs"`
	} `xml:"Polygon"`
	Multi      []kmlGeometry `xml:"MultiGeometry"`
	MultiTrack []kmlGeometry `xml:"http://www.google.com/kml/ext/2.2 MultiTrack"`
}

func parseKML(d *xml.Decoder) (*Document, error) {
	document := &Document{Name: "Imported KML"}
	var documentNamed, placemarkNamed bool
	var folderNameDepth int
	groups := []*ImportGroup{nil}
	err := xmlTokens(d, "kml", func(v xml.StartElement, parents []string) (bool, error) {
		groups = groups[:len(parents)]
		group := groups[len(groups)-1]
		parent := parents[len(parents)-1]
		if v.Name.Local == "Document" || v.Name.Local == "Folder" {
			groups = append(groups, &ImportGroup{Parent: group})
			return false, nil
		}
		namesContainer := parent == "Document" || parent == "kml" || parent == "Folder"
		if v.Name.Local == "name" && namesContainer {
			var name string
			if err := d.DecodeElement(&name, &v); err != nil {
				return true, err
			}
			if name != "" {
				if group != nil {
					group.Name = clean(name)
				}
				if parent != "Folder" {
					document.Name = name
					documentNamed = true
				} else if !documentNamed && (folderNameDepth == 0 || len(parents) < folderNameDepth) {
					document.Name = name
					folderNameDepth = len(parents)
				}
			}
			return true, nil
		}
		if v.Name.Local != "Placemark" {
			groups = append(groups, group)
			return false, nil
		}
		var placemark struct {
			Name string `xml:"name"`
			kmlGeometry
		}
		if err := d.DecodeElement(&placemark, &v); err != nil {
			return true, err
		}
		unnamedDocument := !documentNamed && folderNameDepth == 0
		firstNamedPlacemark := !placemarkNamed && placemark.Name != ""
		if unnamedDocument && firstNamedPlacemark {
			document.Name = placemark.Name
			placemarkNamed = true
		}
		item := document.addItem("placemark")
		item.Group = group
		item.Track.Name = placemark.Name
		return true, placemark.kmlGeometry.appendTo(item.Track, placemark.Name)
	})
	return document, err
}

func kmlPoint(text string, gx bool, name string) (Point, error) {
	fields := strings.Split(text, ",")
	if gx {
		fields = strings.Fields(text)
	}
	if len(fields) < 2 || len(fields) > 3 {
		return Point{}, fmt.Errorf("invalid KML coordinate tuple %q", text)
	}
	values := make([]float64, len(fields))
	for i, field := range fields {
		x, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil {
			return Point{}, fmt.Errorf("invalid KML coordinate: %w", err)
		}
		values[i] = x
	}
	p := Point{Lon: values[0], Lat: values[1], Name: name}
	if len(values) == 3 {
		p.Elevation = &values[2]
	}
	return p, nil
}

func (g kmlGeometry) appendTo(t *Track, name string) error {
	for _, raw := range g.Points {
		coords := kmlTuples(raw.Coordinates)
		if len(coords) != 1 {
			return fmt.Errorf("KML Point requires one coordinate tuple")
		}
		p, err := kmlPoint(coords[0], false, name)
		if err != nil {
			return err
		}
		t.Waypoints = append(t.Waypoints, p)
	}
	lines := g.Lines
	for _, polygon := range g.Polygons {
		lines = append(lines, polygon.Outer.Ring)
		for _, inner := range polygon.Inner {
			lines = append(lines, inner.Ring)
		}
	}
	for _, line := range lines {
		segment := -1
		for _, coord := range kmlTuples(line.Coordinates) {
			p, err := kmlPoint(coord, false, name)
			if err != nil {
				return err
			}
			t.appendPoint(p, &segment, "route")
		}
	}
	for _, track := range g.Tracks {
		if len(track.Times) != 0 && len(track.Times) != len(track.Coords) {
			return fmt.Errorf("KML track timestamps and coordinates differ in count")
		}
		segment := -1
		for i, coord := range track.Coords {
			if strings.TrimSpace(coord) == "" {
				segment = -1
				continue
			}
			p, err := kmlPoint(coord, true, name)
			if err != nil {
				return err
			}
			if len(track.Times) != 0 {
				p.Time = optionalTime(track.Times[i])
			}
			t.appendPoint(p, &segment, "track")
		}
	}
	for _, multi := range append(g.Multi, g.MultiTrack...) {
		if err := multi.appendTo(t, name); err != nil {
			return err
		}
	}
	return nil
}
