package api

import (
	"fmt"
	"reflect"
	"testing"

	"domestique/internal/coach"
	"domestique/internal/store"
	"domestique/internal/strength"
)

func TestChanges(t *testing.T) {
	base := &strength.Baseline{Days: 27, HRVms: 36, RestingHR: 59}
	mornings := func(scores ...int) []store.Recovery {
		var out []store.Recovery
		for i, s := range scores {
			out = append(out, store.Recovery{Date: fmt.Sprintf("2026-11-%02d", 22-len(scores)+i), Score: s, HRVms: 36, RestingHR: 59})
		}
		return out
	}
	day := func(score int, hrv float64, rhr int, sleep float64, prev []store.Recovery) dayState {
		r := &store.Recovery{Date: "2026-11-22", Score: score, HRVms: hrv, RestingHR: rhr}
		return dayState{Rec: r, Mornings: prev, Morning: strength.Morning{Rec: r, Base: base, SleepH: sleep}}
	}
	for _, c := range []struct {
		name string
		d    dayState
		want []string
	}{
		{"bounce back", day(44, 31, 63, 7, mornings(63, 44, 12)), []string{
			"recovery 44%, up from 12% yesterday",
			"HRV 31 ms, 14% under your norm of 36",
			"resting heart rate 63, 4 above your norm of 59",
		}},
		{"steady", day(66, 37, 58, 7.5, mornings(70, 61, 63)), []string{"a morning much like the last few"}},
		{"sliding", day(40, 34, 60, 7, mornings(80, 66, 52)), []string{"recovery has fallen four mornings running, from 80%"}},
		{"short night", day(60, 35, 59, 4.6, mornings(62)), []string{"a short night: 4.6 h asleep"}},
		{"gap: no yesterday to compare", day(40, 36, 59, 7, []store.Recovery{{Date: "2026-11-19", Score: 90}}), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			var f coach.Facts
			changes(&f, c.d)
			if !reflect.DeepEqual(f.Changed, c.want) {
				t.Errorf("got %q, want %q", f.Changed, c.want)
			}
			if *f.HRVNorm != 36 || *f.RHRNorm != 59 {
				t.Errorf("norm %d / %d", *f.HRVNorm, *f.RHRNorm)
			}
		})
	}
}
