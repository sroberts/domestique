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
		{"no GPS, labelled road", fit.SubSportRoad, "wahoo", false, SportRide},
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
// doesn't climb.
func TestClimbs(t *testing.T) {
	out, err := ParseFIT(fixture(t, "dcrainmaker/Edge810-Vector-2013-08-16-15-35-10.fit"))
	if err != nil {
		t.Fatal(err)
	}
	if !climbs(out.Stream.Altitude) {
		t.Error("hilly outdoor ride doesn't climb")
	}
	in, err := ParseFIT(fixture(t, "python-fitparse/sample-activity-indoor-trainer.fit"))
	if err != nil {
		t.Fatal(err)
	}
	if climbs(in.Stream.Altitude) {
		t.Error("trainer ride climbs")
	}

	// A few metres of drift with one wild glitch: not a climb.
	flat := make([]float32, 600)
	for i := range flat {
		flat[i] = 100 + float32(i%5)
	}
	flat[300] = 900
	if climbs(flat) {
		t.Error("one altitude glitch counted as a climb")
	}
	// A steady 40 m climb.
	hill := make([]float32, 600)
	for i := range hill {
		hill[i] = 100 + float32(i)*40/600
	}
	if !climbs(hill) {
		t.Error("40 m climb not counted")
	}
}
