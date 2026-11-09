package sweep_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"domestique/internal/activity"
	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/ingest"
	"domestique/internal/store"
	"domestique/internal/sweep"
)

// ride is an hour at 1 Hz: power around 200 W, cadence 90, and heart rate
// either real (drifting 130-150) or a strap dropping out every few minutes.
func ride(badHR bool) activity.Stream {
	var s activity.Stream
	for i := range 3600 {
		s.T = append(s.T, int32(i))
		s.Power = append(s.Power, int32(190+i%20))
		s.Cadence = append(s.Cadence, 90)
		hr := int32(130 + i/180 + i%5)
		if badHR && i%300 < 60 {
			hr = activity.Missing
		}
		s.HR = append(s.HR, hr)
	}
	return s
}

func TestBadData(t *testing.T) {
	var asked []string
	f := &coachtest.Fake{SystemOne: func(state json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
		out := map[string]coach.Answer{}
		for q := range qs {
			asked = append(asked, q)
			switch q {
			case "hr_bad":
				out[q] = coach.Answer{Type: "noul", Noul: 0.97}
			case "power_bad":
				out[q] = coach.Answer{Type: "noul", Noul: 0.99} // never asked: power is clean
			case "ride_type":
				out[q] = coach.Answer{Type: "choice", Choice: "endurance", Probabilities: map[string]float64{"endurance": 0.9}}
			}
		}
		return out
	}}
	sw, st := setup(t, f)
	ctx := context.Background()
	day := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	put(t, st, "a_bad", "wahoo:1", "wahoo", "ride", "Morning Ride", day, 60, "", true)
	put(t, st, "a_ok", "wahoo:2", "wahoo", "ride", "Evening Ride", day.Add(24*time.Hour), 60, "", true)
	for id, bad := range map[string]bool{"wahoo:1": true, "wahoo:2": false} {
		s := ride(bad)
		if err := st.PutStream(ctx, id, &s); err != nil {
			t.Fatal(err)
		}
		markChannels(t, st, id)
	}
	if err := sw.Ingest.Rebuild(ctx, "a_bad"); err != nil {
		t.Fatal(err)
	}
	if err := sw.Ingest.Rebuild(ctx, "a_ok"); err != nil {
		t.Fatal(err)
	}

	sw.Run(ctx)

	if strings.Count(strings.Join(asked, ","), "hr_bad") != 1 || strings.Contains(strings.Join(asked, ","), "power_bad") {
		t.Fatalf("asked %v: want hr_bad once, for the bad strap only", asked)
	}
	src, _ := st.SourcesFor(ctx, "a_bad")
	if why := ingest.BadChannels(src[0].Extra)["hr"]; why != "strap dropouts" {
		t.Fatalf("bad channels = %v", ingest.BadChannels(src[0].Extra))
	}
	a, _ := st.GetActivity(ctx, "a_bad")
	var m activity.Metrics
	_ = json.Unmarshal(a.Metrics, &m)
	if m.AvgHR != 0 || len(m.HRHist) != 0 || m.NP == 0 {
		t.Fatalf("bad heart rate still in the metrics: avg %d, hist %d, np %d", m.AvgHR, len(m.HRHist), m.NP)
	}
	if v, _ := st.SyncState(ctx, sweep.RideTypePrefix+"a_ok"); v != "endurance" {
		t.Fatalf("ride type = %q", v)
	}

	// These are head-unit files, so the indoor pass never asks about them;
	// a second sweep asks nothing.
	n := f.Calls.Load()
	sw.Run(ctx)
	if f.Calls.Load() != n {
		t.Fatalf("second sweep made %d calls", f.Calls.Load()-n)
	}
}

// markChannels records that a source has heart rate and power, as an
// import would.
func markChannels(t *testing.T, st *store.Store, id string) {
	t.Helper()
	ctx := context.Background()
	src, err := st.GetSource(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	src.HasHR, src.HasPower = true, true
	if err := st.PutSource(ctx, src); err != nil {
		t.Fatal(err)
	}
}
