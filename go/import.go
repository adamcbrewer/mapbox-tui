package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func loadTrack(path string) (*Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open track file: %w", err)
	}
	defer f.Close()
	var header [12]byte
	n, _ := f.ReadAt(header[:], 0)
	format := "FIT"
	var t *Track
	if (n == len(header) && string(header[8:]) == ".FIT") || strings.EqualFold(filepath.Ext(path), ".fit") {
		t, err = parseFIT(f)
	} else {
		if n >= 3 && string(header[:3]) == "\xef\xbb\xbf" {
			if _, err := f.Seek(3, io.SeekStart); err != nil {
				return nil, err
			}
		}
		d := xml.NewDecoder(f)
		for {
			var tok xml.Token
			tok, err = d.Token()
			if err != nil {
				return nil, fmt.Errorf("cannot read track XML: %w", err)
			}
			root, ok := tok.(xml.StartElement)
			if !ok {
				if text, ok := tok.(xml.CharData); ok && strings.TrimSpace(string(text)) != "" {
					return nil, fmt.Errorf("expected GPX, TCX, KML or FIT file")
				}
				continue
			}
			switch root.Name.Local {
			case "gpx":
				format = "GPX"
				t, err = parseGPX(d)
			case "TrainingCenterDatabase":
				format = "TCX"
				t, err = parseTCX(d)
			case "kml":
				format = "KML"
				t, err = parseKML(d)
			default:
				return nil, fmt.Errorf("unsupported track XML root %q; expected GPX, TCX or KML", root.Name.Local)
			}
			break
		}
	}
	if err == nil {
		err = prepareTrack(t)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", format, err)
	}
	return t, nil
}

func prepareTrack(t *Track) error {
	if len(t.Points)+len(t.Waypoints) == 0 {
		return fmt.Errorf("contains no supported geometry")
	}
	t.Name = clean(t.Name)
	t.Bounds = [4]float64{180, 90, -180, -90}
	for _, points := range [][]Point{t.Points, t.Waypoints} {
		for i := range points {
			p := &points[i]
			if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) || math.Abs(p.Lat) > 90 || math.Abs(p.Lon) > 180 {
				return fmt.Errorf("invalid coordinates")
			}
			if p.Elevation != nil && (math.IsNaN(*p.Elevation) || math.IsInf(*p.Elevation, 0)) {
				return fmt.Errorf("invalid elevation")
			}
			if p.Time != nil {
				if _, err := time.Parse(time.RFC3339Nano, *p.Time); err != nil {
					return fmt.Errorf("invalid timestamp: %w", err)
				}
			}
			p.Name = clean(p.Name)
			p.World = project(p.Lon, p.Lat)
			t.Bounds[0] = math.Min(t.Bounds[0], p.Lon)
			t.Bounds[1] = math.Min(t.Bounds[1], p.Lat)
			t.Bounds[2] = math.Max(t.Bounds[2], p.Lon)
			t.Bounds[3] = math.Max(t.Bounds[3], p.Lat)
		}
	}
	for i, s := range t.Segments {
		for j := s.Start; j < s.End; j++ {
			p := &t.Points[j]
			p.Segment = i
			if j > s.Start {
				prev := t.Points[j-1]
				if math.Abs(prev.Lon-p.Lon) > 180 {
					return fmt.Errorf("antimeridian segments are not supported")
				}
				t.Distance += haversine(prev, *p)
			}
			p.Distance = t.Distance
		}
	}
	for i := range t.Waypoints {
		t.Waypoints[i].Segment = -1
	}
	if t.Bounds[2]-t.Bounds[0] > 180 {
		return fmt.Errorf("antimeridian bounds are not supported")
	}
	return nil
}

func (t *Track) appendPoint(p Point, segment *int, kind string) {
	if *segment < 0 {
		*segment = len(t.Segments)
		t.Segments = append(t.Segments, Segment{kind, len(t.Points), len(t.Points)})
	}
	t.Points = append(t.Points, p)
	t.Segments[*segment].End = len(t.Points)
}

func optionalTime(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func xmlTokens(d *xml.Decoder, root string, visit func(xml.StartElement, []string) (bool, error)) error {
	stack := []string{root}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				return fmt.Errorf("expected one %s root", root)
			}
			consumed, err := visit(v, stack)
			if err != nil {
				return err
			}
			if !consumed {
				stack = append(stack, v.Name.Local)
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(v)) != "" {
				return fmt.Errorf("unexpected text after %s root", root)
			}
		}
	}
}
