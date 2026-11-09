package sweep_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/store"
	"domestique/internal/sweep"
)

func setup(t *testing.T, f *coachtest.Fake) (*sweep.Sweeper, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.DiscardHandler)
	in := &ingest.Ingester{Store: st, StorageDir: t.TempDir(), Log: log, Loc: time.UTC}
	w := &coach.Writer{AI: f.AI(t), KV: st, Log: log}
	return &sweep.Sweeper{W: w, Store: st, Ingest: in, Log: log, Loc: time.UTC}, st
}

// put stores an activity with one stream-less source.
func put(t *testing.T, st *store.Store, actID, srcID, kind, sport, name string, start time.Time, mins int, extra string, file bool) {
	t.Helper()
	ctx := context.Background()
	at := start.UnixMilli()
	if err := st.PutActivity(ctx, store.Activity{ID: actID, Sport: sport, Name: name, StartAt: at,
		ElapsedS: mins * 60, MovingS: mins * 60, Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	var sha *string
	if file {
		s := "sha-" + srcID
		sha = &s
	}
	if extra == "" {
		extra = "{}"
	}
	if err := st.PutSource(ctx, store.Source{ID: srcID, ActivityID: actID, Source: kind, Sport: sport, Name: name,
		StartAt: at, ElapsedS: mins * 60, MovingS: mins * 60, Extra: []byte(extra), RawSHA256: sha, ImportedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func TestSweep(t *testing.T) {
	day := time.Date(2026, 10, 6, 17, 0, 0, 0, time.UTC)
	f := &coachtest.Fake{
		SystemOne: func(state json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
			s := string(state)
			switch {
			case qs["sport"] != nil:
				return map[string]coach.Answer{"sport": {Type: "choice", Choice: "strength", Probabilities: map[string]float64{"strength": 0.95}}}
			case qs["indoor"] != nil:
				p := 0.2
				if strings.Contains(s, "Trainer") {
					p = 0.93
				}
				return map[string]coach.Answer{"indoor": {Type: "noul", Noul: p}}
			case qs["same"] != nil:
				return map[string]coach.Answer{"same": {Type: "noul", Noul: 0.97}}
			case qs["stray"] != nil:
				p := 0.1
				if strings.Contains(s, "Test entry") {
					p = 0.96
				}
				return map[string]coach.Answer{"stray": {Type: "noul", Noul: p}}
			case qs["ride_type"] != nil:
				return map[string]coach.Answer{"ride_type": {Type: "choice", Choice: "endurance", Probabilities: map[string]float64{"endurance": 0.3}}}
			}
			t.Errorf("unexpected questions %v", qs)
			return nil
		},
		Chat: func(system, user string) string {
			if !strings.Contains(user, "legs dead") {
				t.Errorf("tags asked about %q", user)
			}
			return `{"tags": ["tired legs"], "note": "new saddle"}`
		},
	}
	sw, st := setup(t, f)
	ctx := context.Background()

	// An "other" workout Whoop couldn't name.
	put(t, st, "a_other", "whoop:1", "whoop", "other", "Afternoon Workout", day, 50, "", false)
	// Two recordings of one walk that heart rate kept apart.
	put(t, st, "a_walk1", "strava:2", "strava", "walk", "Evening Walk", day.Add(24*time.Hour), 40, "", true)
	put(t, st, "a_walk2", "whoop:3", "whoop", "walk", "Walk", day.Add(24*time.Hour+time.Minute), 38, "", false)
	// A hand-typed Strava entry, and a real one.
	put(t, st, "a_stray", "strava:4", "strava", "ride", "Test entry", day.Add(48*time.Hour), 1, "", false)
	put(t, st, "a_real", "strava:5", "strava", "ride", "Club run", day.Add(72*time.Hour), 90, "", false)
	// A ride with no GPS that was on the trainer.
	put(t, st, "a_trainer", "wahoo:8", "wahoo", "ride", "Trainer session", day.Add(144*time.Hour), 45, "", true)
	// A description of the athlete's own, and one that's only Zwift's lines.
	put(t, st, "a_zwift", "strava:6", "strava", "virtual_ride", "Zwift", day.Add(96*time.Hour), 60,
		`{"description":"🗺️ Tempus Fugit in Watopia\nlegs dead, new saddle"}`, true)
	put(t, st, "a_zwift2", "strava:7", "strava", "virtual_ride", "Zwift", day.Add(120*time.Hour), 60,
		`{"description":"🗺️ Volcano Circuit in Watopia\n⚡️ New 5sec power best (90day)"}`, true)

	sw.Run(ctx)

	if a, err := st.GetActivity(ctx, "a_other"); err != nil || a.Sport != "strength" {
		t.Errorf("other workout: sport %q, err %v", a.Sport, err)
	}
	if a, _ := st.GetActivity(ctx, "a_trainer"); a.Sport != "virtual_ride" {
		t.Errorf("trainer ride: sport %q", a.Sport)
	}
	if a, _ := st.GetActivity(ctx, "a_real"); a.Sport != "ride" {
		t.Errorf("outdoor ride: sport %q", a.Sport)
	}
	_, err1 := st.GetActivity(ctx, "a_walk1")
	_, err2 := st.GetActivity(ctx, "a_walk2")
	if (err1 == nil) == (err2 == nil) {
		t.Errorf("walks not merged: %v / %v", err1, err2)
	}
	strays, err := load.Strays(ctx, st)
	if err != nil || !strays["a_stray"] || strays["a_real"] {
		t.Errorf("strays = %v, err %v", strays, err)
	}
	src, _ := st.SourcesFor(ctx, "a_zwift")
	if tg, ok := sweep.SourceTags(ctx, sw.W, src[0]); !ok || tg.Tags[0] != "tired legs" || tg.Note != "new saddle" {
		t.Errorf("tags = %+v %v", tg, ok)
	}

	// Everything was asked once; a second sweep asks nothing.
	n := f.Calls.Load()
	sw.Run(ctx)
	if f.Calls.Load() != n {
		t.Errorf("second sweep made %d calls", f.Calls.Load()-n)
	}

	// The athlete overrules a stray verdict; it isn't asked about again.
	if err := st.SetSyncState(ctx, load.StrayPrefix+"a_stray", "kept"); err != nil {
		t.Fatal(err)
	}
	sw.Run(ctx)
	if strays, _ := load.Strays(ctx, st); strays["a_stray"] {
		t.Error("kept entry still a stray")
	}
}

// With no models chosen, a sweep changes nothing and doesn't fail.
func TestSweepUnavailable(t *testing.T) {
	sw, st := setup(t, &coachtest.Fake{})
	ctx := context.Background()
	put(t, st, "a_other", "whoop:1", "whoop", "other", "Afternoon Workout", time.Date(2026, 10, 6, 17, 0, 0, 0, time.UTC), 50, "", false)
	sw.Run(ctx)
	if a, _ := st.GetActivity(ctx, "a_other"); a.Sport != "other" {
		t.Fatalf("sport changed to %q with no model", a.Sport)
	}
}

// One unreadable reply is skipped, and doesn't stop the descriptions after it.
func TestSweepSkipsBadReply(t *testing.T) {
	chats := 0
	f := &coachtest.Fake{Chat: func(_, user string) string {
		chats++
		if strings.Contains(user, "first") {
			return "I can't help with that."
		}
		return `{"tags": ["rain"], "note": ""}`
	}}
	sw, st := setup(t, f)
	ctx := context.Background()
	day := time.Date(2026, 10, 6, 17, 0, 0, 0, time.UTC)
	put(t, st, "a1", "strava:1", "strava", "ride", "One", day, 60, `{"description":"first ride, wet"}`, true)
	put(t, st, "a2", "strava:2", "strava", "ride", "Two", day.Add(48*time.Hour), 60, `{"description":"second, soaked"}`, true)
	sw.Run(ctx)
	src, _ := st.SourcesFor(ctx, "a2")
	if tg, ok := sweep.SourceTags(ctx, sw.W, src[0]); !ok || tg.Tags[0] != "rain" {
		t.Fatalf("second description not tagged: %+v", tg)
	}
	n := chats
	sw.Run(ctx)
	if chats != n {
		t.Fatalf("bad reply asked again: %d calls", chats-n)
	}
}
