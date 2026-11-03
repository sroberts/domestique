package export

import (
	"bytes"
	"encoding/csv"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/tormoder/fit"

	"domestique/internal/activity"
)

func ride(n int) *activity.Stream {
	s := &activity.Stream{}
	for i := 0; i < n; i++ {
		t := int32(i)
		if i > n/2 {
			t += 30 // a café stop
		}
		s.T = append(s.T, t)
		p := int32(150 + i%100)
		if i == 7 {
			p = activity.Missing
		}
		s.Power = append(s.Power, p)
		s.HR = append(s.HR, int32(120+i%40))
		s.Cadence = append(s.Cadence, int32(85+i%10))
		s.Speed = append(s.Speed, 8.5)
		s.Distance = append(s.Distance, float32(i)*8.5)
		s.Altitude = append(s.Altitude, 100+float32(i%50))
		s.Lat = append(s.Lat, 51.5+float64(i)*1e-5)
		s.Lng = append(s.Lng, -0.12+float64(i)*1e-5)
	}
	return s
}

func TestFITRoundTrip(t *testing.T) {
	start := time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC)
	s := ride(3600)
	var buf bytes.Buffer
	err := WriteFIT(&buf, FITActivity{
		Start: start, Sport: activity.SportRide, Stream: s, ElapsedS: 3630, MovingS: 3600,
		DistanceM: 30600, NP: 210, TSS: 72.4, IF: 0.85, FTP: 250, KJ: 700,
		Laps: []Lap{{0, 1200}, {1200, 2400}, {2400, 3630}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := fit.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if f.Type() != fit.FileTypeActivity {
		t.Fatalf("type %v", f.Type())
	}
	act, err := f.Activity()
	if err != nil {
		t.Fatal(err)
	}
	if len(act.Records) != s.Len() {
		t.Fatalf("records %d, want %d", len(act.Records), s.Len())
	}
	if !act.Records[0].Timestamp.Equal(start) {
		t.Fatalf("first record at %v, want %v", act.Records[0].Timestamp, start)
	}
	for _, i := range []int{0, 7, 100, 1801, 3599} {
		r := act.Records[i]
		want := start.Add(time.Duration(s.T[i]) * time.Second)
		if !r.Timestamp.Equal(want) {
			t.Fatalf("record %d at %v, want %v", i, r.Timestamp, want)
		}
		if s.Power[i] < 0 {
			if r.Power != 0xFFFF {
				t.Fatalf("record %d: missing power encoded as %d", i, r.Power)
			}
		} else if int32(r.Power) != s.Power[i] {
			t.Fatalf("record %d power %d, want %d", i, r.Power, s.Power[i])
		}
		if int32(r.HeartRate) != s.HR[i] || int32(r.Cadence) != s.Cadence[i] {
			t.Fatalf("record %d hr/cad %d/%d, want %d/%d", i, r.HeartRate, r.Cadence, s.HR[i], s.Cadence[i])
		}
		if math.Abs(r.PositionLat.Degrees()-s.Lat[i]) > 1e-6 || math.Abs(r.GetDistanceScaled()-float64(s.Distance[i])) > 0.01 {
			t.Fatalf("record %d position/distance off: %v %v", i, r.PositionLat.Degrees(), r.GetDistanceScaled())
		}
		if math.Abs(r.GetAltitudeScaled()-float64(s.Altitude[i])) > 0.2 {
			t.Fatalf("record %d altitude %v, want %v", i, r.GetAltitudeScaled(), s.Altitude[i])
		}
	}
	if len(act.Sessions) != 1 || len(act.Laps) != 3 || act.Activity == nil {
		t.Fatalf("sessions %d laps %d activity %v", len(act.Sessions), len(act.Laps), act.Activity != nil)
	}
	ss := act.Sessions[0]
	if ss.Sport != fit.SportCycling || !ss.StartTime.Equal(start) || ss.TotalTimerTime != 3600*1000 ||
		ss.TotalDistance != 30600*100 || ss.NormalizedPower != 210 || ss.NumLaps != 3 || ss.ThresholdPower != 250 {
		t.Fatalf("session %+v", ss)
	}
	if ss.TotalElapsedTime != 3630*1000 {
		t.Fatalf("elapsed %d", ss.TotalElapsedTime)
	}
	if act.Laps[1].StartTime.Sub(start) != 1200*time.Second {
		t.Fatalf("lap 2 starts at +%v", act.Laps[1].StartTime.Sub(start))
	}
	if act.Activity.NumSessions != 1 {
		t.Fatalf("activity %+v", act.Activity)
	}
	// And Domestique's own parser reads it back as the same ride.
	p, err := activity.ParseFIT(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if p.Stream.Len() != s.Len() || p.Sport != activity.SportRide || !p.Start.Equal(start) {
		t.Fatalf("reparsed: %d samples, %s, %v", p.Stream.Len(), p.Sport, p.Start)
	}
}

func TestFITHeartRateOnly(t *testing.T) {
	start := time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)
	s := &activity.Stream{}
	for i := 0; i < 1800; i += 2 {
		s.T = append(s.T, int32(i))
		s.HR = append(s.HR, int32(90+i%60))
	}
	var buf bytes.Buffer
	if err := WriteFIT(&buf, FITActivity{Start: start, Sport: activity.SportStrength, Stream: s, ElapsedS: 1800}); err != nil {
		t.Fatal(err)
	}
	f, err := fit.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	act, _ := f.Activity()
	if len(act.Records) != s.Len() || act.Sessions[0].Sport != fit.SportTraining || act.Sessions[0].SubSport != fit.SubSportStrengthTraining {
		t.Fatalf("records %d sport %v/%v", len(act.Records), act.Sessions[0].Sport, act.Sessions[0].SubSport)
	}
	if act.Records[10].HeartRate != uint8(s.HR[10]) || act.Records[10].Power != 0xFFFF {
		t.Fatalf("record 10: %+v", act.Records[10])
	}
}

func TestStreamCSV(t *testing.T) {
	start := time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC)
	var buf bytes.Buffer
	if err := StreamCSV(&buf, start, ride(10)); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 11 || rows[0][2] != "power_w" || rows[1][0] != "2026-11-02T07:30:00Z" || rows[8][2] != "" {
		t.Fatalf("csv: %v", rows[:3])
	}
}

func TestFileName(t *testing.T) {
	d := time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"Morning Ride":            "2026-11-02-morning-ride.fit",
		"../../etc/passwd":        "2026-11-02-etc-passwd.fit",
		"Côte de la Redoute 🚴‍♂️": "2026-11-02-cote-de-la-redoute.fit",
		"   ":                     "2026-11-02-ride.fit",
		`a"b\r\nc;d`:              "2026-11-02-a-b-r-nc-d.fit",
	} {
		if got := FileName(d, in, "Ride", "fit"); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if got := FileName(d, strings.Repeat("x", 300), "", "csv"); len(got) > 80 {
		t.Errorf("long name not bounded: %d", len(got))
	}
}
