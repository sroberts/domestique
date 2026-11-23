package strength

import (
	"strings"
	"testing"

	"domestique/internal/store"
)

func TestAssessReadiness(t *testing.T) {
	base := &Baseline{Days: 27, HRVms: 36, RestingHR: 59}
	rec := func(score int, hrv float64, rhr int) *store.Recovery {
		return &store.Recovery{Date: "2026-11-22", Score: score, HRVms: hrv, RestingHR: rhr}
	}
	for _, c := range []struct {
		name  string
		m     Morning
		level string
		says  string // a fragment the detail must contain
	}{
		{"no score, no load", Morning{}, "normal", "No recovery score yet."},
		{"no score, deep fatigue", Morning{TSB: -35, HasLoad: true}, "easy", "Form is -35"},
		{"red", Morning{Rec: rec(20, 30, 63), Base: base, TSB: -5, HasLoad: true}, "easy", "Recovery is 20%"},
		{"yellow", Morning{Rec: rec(50, 35, 59), Base: base, TSB: -5, HasLoad: true}, "hold", "hold the weights"},
		{"green", Morning{Rec: rec(72, 35, 60), Base: base, TSB: -5, HasLoad: true}, "normal", "nothing in the way"},

		// Near a band's edge, the athlete's own norm decides.
		{"low yellow, HRV well down", Morning{Rec: rec(36, 29, 62), Base: base, HasLoad: true}, "easy", "HRV is 19% under your norm"},
		{"low green, resting HR up", Morning{Rec: rec(69, 34, 65), Base: base, HasLoad: true}, "hold", "resting heart rate is 6 above your norm"},
		{"low green, short night", Morning{Rec: rec(68, 36, 59), SleepH: 4.6, Base: base, HasLoad: true}, "hold", "you slept 4.6 h"},
		{"low green, short for them", Morning{Rec: rec(68, 36, 59), SleepH: 5.8, Base: &Baseline{Days: 27, HRVms: 36, RestingHR: 59, SleepH: 7.5}, HasLoad: true}, "hold", "you slept 5.8 h"},
		{"low green, their usual short night", Morning{Rec: rec(68, 36, 59), SleepH: 5.5, Base: &Baseline{Days: 27, HRVms: 36, RestingHR: 59, SleepH: 6}, HasLoad: true}, "normal", ""},
		{"high yellow, at norm", Morning{Rec: rec(64, 37, 58), Base: base, HasLoad: true, TSB: -4}, "normal", "at your norm"},
		{"high yellow, at norm but short night", Morning{Rec: rec(64, 37, 58), SleepH: 4.5, Base: base, HasLoad: true}, "hold", "Recovery is 64%."},
		{"mid green, HRV down: not borderline", Morning{Rec: rec(85, 29, 59), Base: base, HasLoad: true, TSB: -3}, "normal", "nothing in the way"},
		{"red is never raised", Morning{Rec: rec(32, 40, 55), Base: base, HasLoad: true}, "easy", "Recovery is 32%"},
		{"no baseline: bands only", Morning{Rec: rec(64, 37, 58), HasLoad: true}, "hold", "Recovery is 64%."},

		// Push: a high score with HRV at the norm, while training normally.
		{"push while training", Morning{Rec: rec(88, 42, 56), Base: base, TSB: -8, HasLoad: true}, "push", "HRV at or above your norm"},
		{"high score, HRV under norm", Morning{Rec: rec(88, 33, 58), Base: base, TSB: -8, HasLoad: true}, "normal", ""},
		{"high score, fatigue too deep to push", Morning{Rec: rec(88, 42, 56), Base: base, TSB: -18, HasLoad: true}, "normal", ""},
		{"raised days don't push", Morning{Rec: rec(62, 37, 59), Base: base, TSB: 7, HasLoad: true}, "normal", "at your norm"},
		{"fresh after a taper", Morning{Rec: rec(70, 34, 60), TSB: 8, HasLoad: true}, "push", "form +8"},
		{"push needs a decent night", Morning{Rec: rec(88, 42, 56), SleepH: 4.8, Base: base, TSB: -8, HasLoad: true}, "normal", ""},
		{"push needs load history", Morning{Rec: rec(88, 42, 56), Base: base}, "normal", "Recovery is 88%."},

		// Fatigue caps the day.
		{"green, heavy fatigue", Morning{Rec: rec(80, 40, 57), Base: base, TSB: -24, HasLoad: true}, "hold", "form is -24"},
		{"green, very heavy fatigue", Morning{Rec: rec(90, 40, 57), Base: base, TSB: -33, HasLoad: true}, "easy", "Form is -33"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := AssessReadiness(c.m)
			if got.Level != c.level || !strings.Contains(got.Detail, c.says) {
				t.Errorf("got %s %q / %q; want %s containing %q", got.Level, got.Title, got.Detail, c.level, c.says)
			}
		})
	}
}
