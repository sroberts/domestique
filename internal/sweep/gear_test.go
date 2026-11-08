package sweep_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"domestique/internal/coach/coachtest"
	"domestique/internal/sweep"
)

// The parts pass asks only about descriptions that name a part, once each,
// and keeps only what the note says in its own words.
func TestSweepGear(t *testing.T) {
	var gearAsks atomic.Int32
	f := &coachtest.Fake{Chat: func(system, user string) string {
		if !strings.Contains(system, "pick out bike parts") {
			return `{"tags": [], "note": ""}`
		}
		gearAsks.Add(1)
		if !strings.Contains(user, "new chain") {
			t.Errorf("asked about %q", user)
		}
		return `{"events": [{"part": "chain", "action": "replaced", "bike": "", "quote": "new chain today"}]}`
	}}
	sw, st := setup(t, f)
	ctx := context.Background()
	day := time.Date(2026, 10, 6, 17, 0, 0, 0, time.UTC)
	put(t, st, "a1", "strava:1", "strava", "ride", "Ride", day, 60, `{"description":"new chain today, shifting crisp"}`, true)
	put(t, st, "a2", "strava:2", "strava", "ride", "Ride", day.Add(48*time.Hour), 60, `{"description":"legs dead, soggy"}`, true)
	sw.Run(ctx)
	src, _ := st.SourcesFor(ctx, "a1")
	evs := sweep.SourceGear(ctx, sw.W, src[0])
	if len(evs) != 1 || evs[0].Part != "chain" {
		t.Fatalf("events = %+v", evs)
	}
	sw.Run(ctx)
	if n := gearAsks.Load(); n != 1 {
		t.Fatalf("asked %d times", n)
	}
}
