package ingest_test

import (
	"context"
	"testing"
	"time"

	"domestique/internal/ingest"
	"domestique/internal/store"
)

// putRecording stores an activity with one stream-less source, as an import
// under the old matching rule would have left it.
func putRecording(t *testing.T, st *store.Store, actID, srcID, kind, sport string, start time.Time, mins int, session *string) {
	t.Helper()
	ctx := context.Background()
	at := start.UnixMilli()
	if err := st.PutActivity(ctx, store.Activity{ID: actID, Sport: sport, Name: kind + " " + sport, StartAt: at,
		ElapsedS: mins * 60, MovingS: mins * 60, Metrics: []byte("{}"), SessionID: session}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSource(ctx, store.Source{ID: srcID, ActivityID: actID, Source: kind, Sport: sport,
		StartAt: at, ElapsedS: mins * 60, MovingS: mins * 60, Extra: []byte("{}"), ImportedAt: at}); err != nil {
		t.Fatal(err)
	}
}

// Whoop notices a workout minutes after it starts. A Whoop workout that sits
// inside a bike computer's ride is the same ride, not a second one.
func TestWhoopLateStartMerges(t *testing.T) {
	in, st, _ := setup(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 17, 46, 0, 0, time.UTC)
	putRecording(t, st, "a_ride", "zwift:1", "zwift", "virtual_ride", start, 60, nil)
	strain := 11.2
	res, err := in.AddWhoopWorkout(ctx, "w1", start.Add(6*time.Minute), 50*60, 0,
		ingest.Hint{Source: "whoop", Sport: "Ride", Strain: &strain}, 140, 165, [6]int64{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ActivityID != "a_ride" {
		t.Fatalf("Whoop workout went to %s, want merged into a_ride", res.ActivityID)
	}
}

// Remerge folds activities an older rule left apart, and keeps the one a
// logged lifting session hangs off.
func TestRemerge(t *testing.T) {
	in, st, _ := setup(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 2, 18, 0, 0, 0, time.UTC)
	sess := "sess1"
	putRecording(t, st, "a_whoop", "whoop:lift", "whoop", "strength", start.Add(4*time.Minute), 40, nil)
	putRecording(t, st, "s_sess1", "app:sess1", "app", "strength", start, 45, &sess)
	// A walk the same evening, well clear of the lift: stays its own.
	putRecording(t, st, "a_walk", "strava:9", "strava", "walk", start.Add(3*time.Hour), 30, nil)

	changed, err := in.Remerge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 {
		t.Fatalf("changed %v, want one merge", changed)
	}
	srcs, err := st.SourcesFor(ctx, "s_sess1")
	if err != nil || len(srcs) != 2 {
		t.Fatalf("s_sess1 sources = %v (%v), want app + whoop", srcs, err)
	}
	if _, err := st.GetActivity(ctx, "a_whoop"); err == nil {
		t.Fatal("a_whoop should have been folded into the session's activity")
	}
	if a, err := st.GetActivity(ctx, "s_sess1"); err != nil || a.SessionID == nil || *a.SessionID != "sess1" {
		t.Fatalf("session link lost: %+v %v", a, err)
	}
	if _, err := st.GetActivity(ctx, "a_walk"); err != nil {
		t.Fatalf("the walk was merged away: %v", err)
	}
	// A second pass finds nothing more.
	if again, err := in.Remerge(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second pass changed %v (%v)", again, err)
	}
}
