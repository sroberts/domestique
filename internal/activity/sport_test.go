package activity

import (
	"testing"

	"github.com/tormoder/fit"
)

func TestFitSportRide(t *testing.T) {
	for _, c := range []struct {
		name    string
		sub     fit.SubSport
		mfr     string
		outside bool
		want    string
	}{
		{"GPS track", fit.SubSportGeneric, "wahoo", true, SportRide},
		{"no GPS, no climb", fit.SubSportGeneric, "wahoo", false, SportVirtualRide},
		{"no GPS, labelled road (a default profile)", fit.SubSportRoad, "garmin", false, SportVirtualRide},
		{"no GPS, labelled road, climbs", fit.SubSportRoad, "garmin", true, SportRide},
		{"no GPS, labelled gravel", fit.SubSportGravelCycling, "wahoo", false, SportRide},
		{"labelled indoor, GPS on", fit.SubSportIndoorCycling, "wahoo", true, SportVirtualRide},
		{"labelled virtual", fit.SubSportVirtualActivity, "wahoo", true, SportVirtualRide},
		{"labelled spin", fit.SubSportSpin, "garmin", false, SportVirtualRide},
		{"Zwift", fit.SubSportRoad, "zwift", true, SportVirtualRide},
	} {
		if got := fitSport(fit.SportCycling, c.sub, c.mfr, c.outside); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A head unit whose GPS never locked still records the hills; a trainer ride
// doesn't climb, however long it drifts.
func TestClimbs(t *testing.T) {
	out, err := ParseFIT(fixture(t, "dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit"))
	if err != nil {
		t.Fatal(err)
	}
	if !climbs(out.Stream.T, out.Stream.Altitude) {
		t.Error("hilly outdoor ride doesn't climb")
	}
	in, err := ParseFIT(fixture(t, "python-fitparse/sample-activity-indoor-trainer.fit"))
	if err != nil {
		t.Fatal(err)
	}
	if climbs(in.Stream.T, in.Stream.Altitude) {
		t.Error("trainer ride climbs")
	}

	trace := func(secs int, f func(i int) float32) ([]int32, []float32) {
		ts, alt := make([]int32, secs), make([]float32, secs)
		for i := range ts {
			ts[i], alt[i] = int32(i), f(i)
		}
		return ts, alt
	}
	for _, c := range []struct {
		name string
		secs int
		f    func(i int) float32
		want bool
	}{
		{"flat with sensor noise", 3600, func(i int) float32 { return 100 + float32(i%5) }, false},
		{"flat with one wild glitch", 3600, func(i int) float32 {
			if i == 1800 {
				return 900
			}
			return 100
		}, false},
		// A weather front over a three-hour trainer session: 40 m of drift.
		{"slow drift", 3 * 3600, func(i int) float32 { return 100 + float32(i)*40/(3*3600) }, false},
		{"25 m climb in 8 minutes", 3600, func(i int) float32 { return 100 + float32(min(i, 480))*25/480 }, true},
		{"25 m descent in 8 minutes", 3600, func(i int) float32 { return 200 - float32(min(i, 480))*25/480 }, true},
		{"under a minute, too short to judge", 30, func(i int) float32 { return float32(i) }, false},
	} {
		ts, alt := trace(c.secs, c.f)
		if got := climbs(ts, alt); got != c.want {
			t.Errorf("%s: climbs %v, want %v", c.name, got, c.want)
		}
	}
	if climbs(nil, nil) {
		t.Error("no altitude climbs")
	}
}
