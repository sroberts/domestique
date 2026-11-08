package sweep

import (
	"testing"

	"domestique/internal/activity"
	"domestique/internal/store"
)

// A paused recording has no samples while stopped: that's no dropout.
func TestGapsIgnorePauses(t *testing.T) {
	var s activity.Stream
	for i := range 1200 {
		if i >= 600 && i < 900 {
			continue // ten-minute café stop, auto-paused
		}
		s.T = append(s.T, int32(i))
		s.HR = append(s.HR, int32(130+i%9))
	}
	f := recordingFacts(store.Source{Source: "wahoo"}, "ride", &s, 185, 0)
	if f.HR.Dropouts != 0 || f.HR.Coverage < 0.99 || f.Minutes != 15 {
		t.Fatalf("paused ride: %+v, %d min", *f.HR, f.Minutes)
	}
	// The same ride with the strap missing for 20 s, three times.
	for _, at := range []int{100, 300, 1000} {
		for i := range s.T {
			if s.T[i] >= int32(at) && s.T[i] < int32(at+20) {
				s.HR[i] = activity.Missing
			}
		}
	}
	f = recordingFacts(store.Source{Source: "wahoo"}, "ride", &s, 185, 0)
	if f.HR.Dropouts != 3 || hrReason(f.HR) != "strap dropouts" {
		t.Fatalf("dropouts = %d, reason %q", f.HR.Dropouts, hrReason(f.HR))
	}
}

func TestCadenceLock(t *testing.T) {
	var s activity.Stream
	for i := range 600 {
		s.T = append(s.T, int32(i))
		c := int32(85 + i%10)
		s.Cadence = append(s.Cadence, c)
		s.HR = append(s.HR, c+1)
	}
	f := recordingFacts(store.Source{}, "ride", &s, 185, 0)
	if f.HR.CadenceLocked < 0.95 || hrReason(f.HR) != "it followed cadence" {
		t.Fatalf("lock = %.2f, reason %q", f.HR.CadenceLocked, hrReason(f.HR))
	}
}

// Five 3-minute efforts at 300 W, for an FTP of 250, with easy riding
// between: five hard efforts.
func TestHardEfforts(t *testing.T) {
	var s activity.Stream
	for i := range 3600 {
		s.T = append(s.T, int32(i))
		w := int32(150)
		if i >= 600 && i < 2400 && (i-600)%360 < 180 {
			w = 300
		}
		s.Power = append(s.Power, w)
	}
	if n, ok := hardEfforts(&s, 250, 185); !ok || n != 5 {
		t.Fatalf("hard efforts = %d %v", n, ok)
	}
}

func TestStops(t *testing.T) {
	var s activity.Stream
	for i := range 600 {
		s.T = append(s.T, int32(i))
		v := float32(8)
		if (i >= 100 && i < 130) || (i >= 300 && i < 305) {
			v = 0 // a 30 s red light, and a 5 s slow-down
		}
		s.Speed = append(s.Speed, v)
	}
	if n := stops(&s); n != 1 {
		t.Fatalf("stops = %d", n)
	}
}
