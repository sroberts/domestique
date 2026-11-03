package activity

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	"github.com/tormoder/fit"
)

// ParseFIT reads a FIT activity file.
func ParseFIT(data []byte) (*Parsed, error) {
	f, err := fit.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode fit: %w", err)
	}
	act, err := f.Activity()
	if err != nil {
		return nil, fmt.Errorf("not an activity file: %w", err)
	}
	if len(act.Records) == 0 {
		return nil, errors.New("fit file has no records")
	}

	var b builder
	for _, r := range act.Records {
		x := newSample(r.Timestamp)
		if r.Power != 0xFFFF {
			x.power = int32(r.Power)
		}
		if r.HeartRate != 0xFF && r.HeartRate > 0 {
			x.hr = int32(r.HeartRate)
		}
		if r.Cadence != 0xFF {
			x.cad = int32(r.Cadence)
		}
		if r.EnhancedSpeed != 0xFFFFFFFF {
			x.speed = float64(r.EnhancedSpeed) / 1000
		} else if v := r.GetSpeedScaled(); !math.IsNaN(v) {
			x.speed = v
		}
		if v := r.GetDistanceScaled(); !math.IsNaN(v) {
			x.dist = v
		}
		if r.EnhancedAltitude != 0xFFFFFFFF {
			x.alt = float64(r.EnhancedAltitude)/5 - 500
		} else if v := r.GetAltitudeScaled(); !math.IsNaN(v) {
			x.alt = v
		}
		if !r.PositionLat.Invalid() && !r.PositionLong.Invalid() {
			x.lat, x.lng = r.PositionLat.Degrees(), r.PositionLong.Degrees()
		}
		b.add(x)
	}

	p := &Parsed{Format: "fit", Start: b.start, Stream: b.stream()}
	p.Manufacturer = manufacturer(f.FileId.Manufacturer)
	p.Device = f.FileId.ProductName

	sport, sub := fit.SportGeneric, fit.SubSportGeneric
	if len(act.Sessions) > 0 {
		s := act.Sessions[0]
		sport, sub = s.Sport, s.SubSport
		if !s.StartTime.IsZero() {
			p.Start = s.StartTime
		}
		if s.TotalElapsedTime != 0xFFFFFFFF {
			p.ElapsedS = int(s.TotalElapsedTime / 1000)
		}
		if s.TotalTimerTime != 0xFFFFFFFF {
			p.MovingS = int(s.TotalTimerTime / 1000)
		}
		if s.TotalDistance != 0xFFFFFFFF {
			p.DistanceM = float64(s.TotalDistance) / 100
		}
		// Re-base offsets if the session started before the first record.
		if d := int32(b.start.Sub(p.Start).Seconds()); d > 0 && d < 600 {
			for i := range p.Stream.T {
				p.Stream.T[i] += d
			}
		} else {
			p.Start = b.start
		}
	}
	p.Sport = fitSport(sport, sub, p.Manufacturer, p.Stream.HasGPS())
	p.finish()
	return p, nil
}

func manufacturer(m fit.Manufacturer) string {
	switch m {
	case fit.ManufacturerWahooFitness:
		return "wahoo"
	case fit.ManufacturerZwift, fit.ManufacturerZwiftByte:
		return "zwift"
	case fit.ManufacturerGarmin:
		return "garmin"
	case fit.ManufacturerStrava:
		return "strava"
	case fit.ManufacturerWhoop:
		return "whoop"
	case fit.ManufacturerDevelopment:
		return ""
	}
	return fmt.Sprintf("m%d", m)
}

func fitSport(s fit.Sport, sub fit.SubSport, mfr string, gps bool) string {
	switch s {
	case fit.SportCycling:
		if sub == fit.SubSportVirtualActivity || sub == fit.SubSportIndoorCycling || mfr == "zwift" || !gps {
			return SportVirtualRide
		}
		return SportRide
	case fit.SportRunning:
		return SportRun
	case fit.SportWalking:
		return SportWalk
	case fit.SportHiking:
		return SportHike
	case fit.SportSwimming:
		return SportSwim
	case fit.SportTraining:
		return SportStrength
	}
	if mfr == "zwift" {
		return SportVirtualRide
	}
	return SportOther
}
