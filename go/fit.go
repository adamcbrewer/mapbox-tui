package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/kit/hash/crc16"
	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
)

func fitTime(stamp time.Time) *string {
	if stamp.IsZero() {
		return nil
	}
	s := stamp.UTC().Format(time.RFC3339Nano)
	return &s
}

func validateFITCRC(r io.Reader) error {
	crc := crc16.New()
	for sequence := 0; ; sequence++ {
		var header [14]byte
		n, err := io.ReadFull(r, header[:12])
		if err == io.EOF && n == 0 && sequence > 0 {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read file header: %w", err)
		}
		size := header[0]
		dataSize := binary.LittleEndian.Uint32(header[4:8])
		if (size != 12 && size != 14) || string(header[8:12]) != ".FIT" || dataSize == 0 {
			return decoder.ErrNotFITFile
		}
		crc.Reset()
		crc.Write(header[:12])
		if size == 14 {
			if _, err := io.ReadFull(r, header[12:14]); err != nil {
				return fmt.Errorf("read header checksum: %w", err)
			}
			stored := binary.LittleEndian.Uint16(header[12:14])
			if stored != 0 && stored != crc.Sum16() {
				return fmt.Errorf("header checksum: %w", decoder.ErrCRCChecksumMismatch)
			}
			crc.Write(header[12:14])
		}
		if _, err := io.CopyN(crc, r, int64(dataSize)); err != nil {
			return fmt.Errorf("read file data: %w", err)
		}
		var trailer [2]byte
		if _, err := io.ReadFull(r, trailer[:]); err != nil {
			return fmt.Errorf("read file checksum: %w", err)
		}
		if stored := binary.LittleEndian.Uint16(trailer[:]); stored != crc.Sum16() {
			return fmt.Errorf("file checksum: expected %d, calculated %d: %w", stored, crc.Sum16(), decoder.ErrCRCChecksumMismatch)
		}
	}
}

func parseFIT(r io.ReadSeeker) (*Document, error) {
	if err := validateFITCRC(r); err != nil {
		return nil, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	document := &Document{Name: "Imported FIT"}
	// The SDK drops the header's CRC contribution when its optional checksum is zero.
	// Validate full-file checksums above before using its message decoder.
	for {
		var header [12]byte
		if _, err := io.ReadFull(r, header[:]); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		size := int64(header[0]) + int64(binary.LittleEndian.Uint32(header[4:8])) + 2
		if _, err := r.Seek(-int64(len(header)), io.SeekCurrent); err != nil {
			return nil, err
		}
		// The SDK can read fields past DataSize; keep it inside this sequence.
		d := decoder.New(io.LimitReader(r, size), decoder.WithIgnoreChecksum(), decoder.WithNoComponentExpansion())
		file, err := d.Decode()
		if err != nil {
			return nil, err
		}
		kind := "track"
		for _, message := range file.Messages {
			if message.Num != typedef.MesgNumFileId {
				continue
			}
			typeID := mesgdef.NewFileId(&message).Type
			if typeID == typedef.FileCourse {
				kind = "route"
			} else if typeID != typedef.FileActivity {
				return nil, fmt.Errorf("unsupported FIT file type %s; expected activity or course", typeID)
			}
		}
		itemKind := "activity"
		if kind == "route" {
			itemKind = "route"
		}
		t := document.addItem(itemKind).Track
		segment := -1
		for _, message := range file.Messages {
			switch message.Num {
			case typedef.MesgNumCourse:
				if name := mesgdef.NewCourse(&message).Name; name != "" {
					t.Name = name
					document.Name = name
				}
			case typedef.MesgNumRecord:
				r := mesgdef.NewRecord(&message)
				if r.PositionLat == basetype.Sint32Invalid || r.PositionLong == basetype.Sint32Invalid {
					segment = -1
					continue
				}
				p := Point{Lon: r.PositionLongDegrees(), Lat: r.PositionLatDegrees(), Time: fitTime(r.Timestamp)}
				if r.EnhancedAltitude != basetype.Uint32Invalid {
					e := r.EnhancedAltitudeScaled()
					p.Elevation = &e
				} else if r.Altitude != basetype.Uint16Invalid {
					e := r.AltitudeScaled()
					p.Elevation = &e
				}
				t.appendPoint(p, &segment, kind)
			case typedef.MesgNumCoursePoint:
				p := mesgdef.NewCoursePoint(&message)
				if p.PositionLat != basetype.Sint32Invalid && p.PositionLong != basetype.Sint32Invalid {
					t.Waypoints = append(t.Waypoints, Point{Lon: p.PositionLongDegrees(), Lat: p.PositionLatDegrees(), Name: p.Name, Time: fitTime(p.Timestamp)})
				}
			case typedef.MesgNumEvent:
				e := mesgdef.NewEvent(&message)
				if e.Event != typedef.EventTimer {
					continue
				}
				switch e.EventType {
				case typedef.EventTypeStart, typedef.EventTypeStop, typedef.EventTypeStopAll, typedef.EventTypeStopDisable, typedef.EventTypeStopDisableAll:
					segment = -1
				}
			case typedef.MesgNumSession:
				segment = -1
			}
		}
	}
	return document, nil
}
