package activity

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseGPX reads a GPX track, including Garmin TrackPointExtension (hr, cad)
// and the common <power> extension.
func ParseGPX(data []byte) (*Parsed, error) {
	var doc struct {
		Creator string `xml:"creator,attr"`
		Trk     []struct {
			Name string `xml:"name"`
			Type string `xml:"type"`
			Seg  []struct {
				Pt []struct {
					Lat  float64  `xml:"lat,attr"`
					Lon  float64  `xml:"lon,attr"`
					Ele  *float64 `xml:"ele"`
					Time string   `xml:"time"`
					Ext  struct {
						Inner []byte `xml:",innerxml"`
					} `xml:"extensions"`
				} `xml:"trkpt"`
			} `xml:"trkseg"`
		} `xml:"trk"`
	}
	if err := xml.Unmarshal(bytes.TrimSpace(data), &doc); err != nil {
		return nil, fmt.Errorf("decode gpx: %w", err)
	}
	if len(doc.Trk) == 0 {
		return nil, errors.New("gpx has no track")
	}
	var b builder
	for _, trk := range doc.Trk {
		for _, seg := range trk.Seg {
			for _, pt := range seg.Pt {
				t, err := parseTime(pt.Time)
				if err != nil {
					continue
				}
				x := newSample(t)
				x.lat, x.lng = pt.Lat, pt.Lon
				if pt.Ele != nil {
					x.alt = *pt.Ele
				}
				if len(pt.Ext.Inner) > 0 {
					ext := string(pt.Ext.Inner)
					x.hr = extInt(ext, "hr")
					x.cad = extInt(ext, "cad")
					x.power = extInt(ext, "power")
				}
				b.add(x)
			}
		}
	}
	if !b.hasPoint {
		return nil, errors.New("gpx has no timed points")
	}
	p := &Parsed{Format: "gpx", Start: b.start, Stream: b.stream(), Name: doc.Trk[0].Name}
	p.Sport = textSport(doc.Trk[0].Type, p.Stream.HasGPS())
	p.Manufacturer = creatorMfr(doc.Creator)
	p.Device = doc.Creator
	p.finish()
	return p, nil
}

// extInt finds <…:name>123</…:name> in an extensions blob, namespace-agnostic.
func extInt(ext, name string) int32 {
	if v, ok := extFloat(ext, name); ok {
		return int32(v)
	}
	return Missing
}

// extFloat is extInt without truncation, for fractional values such as speed.
func extFloat(ext, name string) (float64, bool) {
	for _, open := range []string{":" + name + ">", "<" + name + ">"} {
		i := strings.Index(ext, open)
		if i < 0 {
			continue
		}
		rest := ext[i+len(open):]
		j := strings.IndexByte(rest, '<')
		if j < 0 {
			continue
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(rest[:j]), 64); err == nil && v >= 0 {
			return v, true
		}
	}
	return 0, false
}

// ParseTCX reads a Garmin Training Center file.
func ParseTCX(data []byte) (*Parsed, error) {
	type tp struct {
		Time    string   `xml:"Time"`
		Lat     *float64 `xml:"Position>LatitudeDegrees"`
		Lng     *float64 `xml:"Position>LongitudeDegrees"`
		Alt     *float64 `xml:"AltitudeMeters"`
		Dist    *float64 `xml:"DistanceMeters"`
		HR      *float64 `xml:"HeartRateBpm>Value"`
		Cadence *float64 `xml:"Cadence"`
		Ext     struct {
			Inner []byte `xml:",innerxml"`
		} `xml:"Extensions"`
	}
	var doc struct {
		Activities []struct {
			Sport string `xml:"Sport,attr"`
			Laps  []struct {
				Track []tp `xml:"Track>Trackpoint"`
			} `xml:"Lap"`
			Creator string `xml:"Creator>Name"`
		} `xml:"Activities>Activity"`
	}
	// Strava's TCX exports sometimes lead with whitespace before the XML declaration.
	if err := xml.Unmarshal(bytes.TrimSpace(data), &doc); err != nil {
		return nil, fmt.Errorf("decode tcx: %w", err)
	}
	if len(doc.Activities) == 0 {
		return nil, errors.New("tcx has no activity")
	}
	a := doc.Activities[0]
	var b builder
	for _, lap := range a.Laps {
		for _, pt := range lap.Track {
			t, err := parseTime(pt.Time)
			if err != nil {
				continue
			}
			x := newSample(t)
			if pt.Lat != nil && pt.Lng != nil {
				x.lat, x.lng = *pt.Lat, *pt.Lng
			}
			if pt.Alt != nil {
				x.alt = *pt.Alt
			}
			if pt.Dist != nil {
				x.dist = *pt.Dist
			}
			if pt.HR != nil {
				x.hr = int32(*pt.HR)
			}
			if pt.Cadence != nil {
				x.cad = int32(*pt.Cadence)
			}
			if len(pt.Ext.Inner) > 0 {
				ext := string(pt.Ext.Inner)
				x.power = extInt(ext, "Watts")
				if v, ok := extFloat(ext, "Speed"); ok {
					x.speed = v // m/s, fractional
				}
			}
			b.add(x)
		}
	}
	if !b.hasPoint {
		return nil, errors.New("tcx has no timed points")
	}
	p := &Parsed{Format: "tcx", Start: b.start, Stream: b.stream(), Device: a.Creator}
	p.Sport = textSport(a.Sport, p.Stream.HasGPS())
	p.Manufacturer = creatorMfr(a.Creator)
	p.finish()
	return p, nil
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", s)
}

// textSport maps free-text sport names (GPX <type>, TCX Sport, Strava's
// "Activity Type") onto Domestique sports.
func textSport(s string, gps bool) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "")) {
	case "biking", "cycling", "ride", "1", "roadbike", "mountainbikeride", "gravelride", "ebikeride", "mountainbiking", "gravel":
		if !gps {
			return SportVirtualRide
		}
		return SportRide
	case "virtualride", "indoorcycling", "velomobile":
		return SportVirtualRide
	case "running", "run", "9", "trailrun", "virtualrun":
		return SportRun
	case "walk", "walking", "10":
		return SportWalk
	case "hike", "hiking":
		return SportHike
	case "swim", "swimming":
		return SportSwim
	case "weighttraining", "workout", "crossfit", "strengthtraining", "functionalstrengthtraining", "hiit":
		return SportStrength
	}
	return SportOther
}

func creatorMfr(c string) string {
	l := strings.ToLower(c)
	switch {
	case strings.Contains(l, "wahoo"), strings.Contains(l, "elemnt"):
		return "wahoo"
	case strings.Contains(l, "zwift"):
		return "zwift"
	case strings.Contains(l, "garmin"), strings.Contains(l, "edge"), strings.Contains(l, "forerunner"):
		return "garmin"
	case strings.Contains(l, "strava"):
		return "strava"
	}
	return ""
}

// TextSport is exported for importers that only have a sport name.
func TextSport(s string, gps bool) string { return textSport(s, gps) }
