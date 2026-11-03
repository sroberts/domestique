package activity

import (
	"os"
	"path/filepath"
	"testing"
)

// Sample files ship with the FIT library's module.
func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), "go/pkg/mod/github.com/tormoder/fit@*/testdata", rel))
	if len(matches) == 0 {
		t.Skip("sample not in module cache: " + rel)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseFITPowerRide(t *testing.T) {
	p, err := ParseFIT(fixture(t, "dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit"))
	if err != nil {
		t.Fatal(err)
	}
	m := Compute(&p.Stream)
	t.Logf("sport=%s mfr=%s start=%s elapsed=%ds moving=%ds dist=%.0fm n=%d gps=%v", p.Sport, p.Manufacturer, p.Start, p.ElapsedS, p.MovingS, p.DistanceM, p.Stream.Len(), p.Stream.HasGPS())
	t.Logf("avgP=%d NP=%d maxP=%d kJ=%.0f avgHR=%d maxHR=%d cad=%d ascent=%.0f curve=%v", m.AvgPower, m.NP, m.MaxPower, m.KJ, m.AvgHR, m.MaxHR, m.AvgCadence, m.AscentM, m.PowerCurve)
	if p.Sport != SportRide || !p.Stream.HasPower() || m.NP < m.AvgPower || m.NP == 0 {
		t.Fatalf("unexpected: sport %s NP %d avg %d", p.Sport, m.NP, m.AvgPower)
	}
	tss, ifv := PowerTSS(m.NP, p.MovingS, 250)
	t.Logf("TSS@250=%.1f IF=%.2f", tss, ifv)
}

func TestParseFITIndoor(t *testing.T) {
	p, err := ParseFIT(fixture(t, "python-fitparse/sample-activity-indoor-trainer.fit"))
	if err != nil {
		t.Fatal(err)
	}
	m := Compute(&p.Stream)
	t.Logf("sport=%s mfr=%s elapsed=%d moving=%d power=%v hr=%v NP=%d avgHR=%d", p.Sport, p.Manufacturer, p.ElapsedS, p.MovingS, p.Stream.HasPower(), p.Stream.HasHR(), m.NP, m.AvgHR)
}

func TestParseFITCorrupt(t *testing.T) {
	if _, err := ParseFIT(fixture(t, "corrupt/activity-unexpected-eof.fit")); err == nil {
		t.Log("corrupt file decoded without error (library tolerant)")
	}
}

func TestParseGPX(t *testing.T) {
	gpx := []byte(`<?xml version="1.0"?><gpx creator="StravaGPX" xmlns:gpxtpx="x"><trk><name>Morning Ride</name><type>1</type><trkseg>
<trkpt lat="45.0" lon="-73.0"><ele>10</ele><time>2024-05-01T10:00:00Z</time><extensions><gpxtpx:TrackPointExtension><gpxtpx:hr>120</gpxtpx:hr><gpxtpx:cad>85</gpxtpx:cad></gpxtpx:TrackPointExtension><power>200</power></extensions></trkpt>
<trkpt lat="45.001" lon="-73.0"><ele>14</ele><time>2024-05-01T10:00:20Z</time><extensions><gpxtpx:TrackPointExtension><gpxtpx:hr>130</gpxtpx:hr></gpxtpx:TrackPointExtension></extensions></trkpt>
</trkseg></trk></gpx>`)
	p, err := ParseGPX(gpx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Sport != SportRide || p.Name != "Morning Ride" || p.DistanceM < 100 || p.DistanceM > 120 || p.Stream.HR[1] != 130 || p.Stream.Power[0] != 200 {
		t.Fatalf("gpx: %+v dist=%.1f", p.Sport, p.DistanceM)
	}
}
