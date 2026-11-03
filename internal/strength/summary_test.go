package strength

import (
	"testing"

	"domestique/internal/store"
)

func TestZones(t *testing.T) {
	z := ZonesFor(190)
	if z != (Zones{95, 114, 133, 152, 171}) {
		t.Fatalf("zones = %v", z)
	}
	for bpm, want := range map[int]int{80: 0, 95: 1, 133: 3, 170: 4, 171: 5, 200: 5} {
		if got := z.Of(bpm); got != want {
			t.Errorf("Of(%d) = %d, want %d", bpm, got, want)
		}
	}
}

// One set from t=60s to t=90s; heart rate peaks 5s after the set (lag), then
// falls; the next set starts at t=180s.
func TestBuildSlicesSetAndRecovery(t *testing.T) {
	const s = 1000
	fin := int64(240 * s)
	sess := store.Session{ID: "x", StartedAt: 0, FinishedAt: &fin}
	sets := []store.Set{
		{ID: "a", Reps: 5, WeightKg: 100, StartedAt: 60 * s, EndedAt: 90 * s},
		{ID: "b", Reps: 5, WeightKg: 100, StartedAt: 180 * s, EndedAt: 200 * s},
	}
	var hr []store.HRSample
	for t := int64(0); t <= 240; t++ {
		bpm := 100
		switch {
		case t >= 60 && t <= 95:
			bpm = 120 + int(t-60) // climbs to 155 at t=95
		case t > 95 && t <= 150:
			bpm = 155 - int(t-95) // falls 1 bpm/s
		}
		hr = append(hr, store.HRSample{T: t * s, BPM: bpm})
	}

	sum := Build(sess, sets, hr, 190)
	if sum.HRStatus != "ready" {
		t.Fatalf("status %s", sum.HRStatus)
	}
	if sum.VolumeKg != 1000 || sum.TotalReps != 10 || sum.DurationS != 240 {
		t.Fatalf("totals: %+v", sum)
	}
	a := sum.SetStats[0]
	if a.PeakBPM != 155 {
		t.Errorf("peak = %d, want 155 (lagged peak counted)", a.PeakBPM)
	}
	if a.Zone != 4 {
		t.Errorf("zone = %d, want 4", a.Zone)
	}
	// Recovery window: 60s after the set ends at 90s -> t=150, bpm 100.
	if a.RecoveryDrop != 55 || a.RecoveryWindowS != 60 {
		t.Errorf("recovery = -%d in %ds, want -55 in 60s", a.RecoveryDrop, a.RecoveryWindowS)
	}
	if a.RestS != 90 {
		t.Errorf("rest = %d, want 90", a.RestS)
	}
	if sum.SetStats[1].RestS != 0 {
		t.Errorf("last set rest should be 0")
	}
}

func TestBuildPendingWithoutHR(t *testing.T) {
	sess := store.Session{ID: "x", StartedAt: 0}
	sets := []store.Set{{ID: "a", Reps: 5, WeightKg: 50, StartedAt: 1000, EndedAt: 2000}}
	sum := Build(sess, sets, nil, 190)
	if sum.HRStatus != "pending" || sum.SetStats[0].HasHR {
		t.Fatalf("want pending without HR: %+v", sum)
	}
}
