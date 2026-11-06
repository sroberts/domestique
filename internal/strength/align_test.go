package strength

import (
	"math"
	"math/rand"
	"testing"

	"domestique/internal/store"
)

// session builds a 12-set session in device time and the heart rate a strap
// would record for it, with the strap's clock running lag ms behind.
func session(lag int64, seed int64) ([]store.Set, []store.HRSample) {
	r := rand.New(rand.NewSource(seed))
	var sets []store.Set
	t := int64(1_700_000_000_000)
	for i := 0; i < 12; i++ {
		dur := int64(25_000 + r.Intn(20_000))
		sets = append(sets, store.Set{ID: string(rune('a' + i)), Reps: 6, StartedAt: t, EndedAt: t + dur})
		t += dur + int64(90_000+r.Intn(60_000))
	}
	// Physiology in true time: climbs during a set, peaks just after, decays.
	var hr []store.HRSample
	bpm := 90.0
	for ts := sets[0].StartedAt - 180_000; ts < t+180_000; ts += 1000 {
		inSet, after := false, int64(-1)
		for _, s := range sets {
			if ts >= s.StartedAt && ts < s.EndedAt+4_000 {
				inSet = true
			}
			if ts >= s.EndedAt {
				after = ts - s.EndedAt
			}
		}
		if inSet {
			bpm += (165 - bpm) * 0.06
		} else {
			bpm += (92 - bpm) * 0.025
		}
		_ = after
		if (ts-sets[0].StartedAt)%5000 == 0 { // a sample every 5 s, like Apple Health
			hr = append(hr, store.HRSample{T: ts + lag, BPM: int(math.Round(bpm + r.NormFloat64()*1.5))})
		}
	}
	return sets, hr
}

func TestEstimateShiftFindsLag(t *testing.T) {
	for _, lag := range []int64{0, 37_000, -52_000, 95_000} {
		for seed := int64(1); seed <= 3; seed++ {
			sets, hr := session(lag, seed)
			got, z, ok := EstimateShift(sets, hr)
			if !ok {
				t.Fatalf("lag %d seed %d: no estimate (z=%.1f)", lag, seed, z)
			}
			if d := got - lag; d < -4_000 || d > 4_000 {
				t.Errorf("lag %d seed %d: estimated %d (z=%.1f)", lag, seed, got, z)
			}
		}
	}
}

func TestEstimateShiftNeedsStructure(t *testing.T) {
	sets, _ := session(0, 1)
	r := rand.New(rand.NewSource(9))
	var flat []store.HRSample
	for ts := sets[0].StartedAt - 180_000; ts < sets[len(sets)-1].EndedAt+180_000; ts += 5000 {
		flat = append(flat, store.HRSample{T: ts, BPM: 100 + r.Intn(5)})
	}
	if s, z, ok := EstimateShift(sets, flat); ok && s != 0 {
		t.Fatalf("noise gave a shift of %d (z=%.1f)", s, z)
	}
	if _, _, ok := EstimateShift(sets[:2], flat); ok {
		t.Fatal("two sets should not be enough")
	}
}

func TestAlignAppliesOffsetAndManualShift(t *testing.T) {
	sets, hr := session(0, 2)
	manual := int64(10_000)
	sess := store.Session{ClockOffsetMs: 3_000, HRShiftMs: &manual}
	out, al := Align(sess, sets, hr)
	if al.Source != "manual" || al.HRShiftMs != manual {
		t.Fatalf("alignment %+v", al)
	}
	if out[0].T != hr[0].T-3_000-10_000 {
		t.Fatalf("sample moved to %d, want %d", out[0].T, hr[0].T-13_000)
	}
	if hr[0].T == out[0].T {
		t.Fatal("input was modified")
	}
}
