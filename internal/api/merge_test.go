package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"domestique/internal/ingest"
	"domestique/internal/store"
)

// A goblet squat and push-up circuit, 3 rounds planned.
var circuitPlan = []store.Exercise{
	{Name: "Goblet squat", Sets: []store.TargetSet{{Reps: 10, WeightKg: 20}, {Reps: 10, WeightKg: 20}, {Reps: 10, WeightKg: 20}}, RestSeconds: 30, Circuit: "a"},
	{Name: "Push-up", Sets: []store.TargetSet{{Reps: 12}, {Reps: 12}, {Reps: 12}}, RestSeconds: 90, Circuit: "a"},
}

// upload saves a finished session through the API as the phone would: its
// sets, then the session, with rounds done of the circuit from start.
func upload(t *testing.T, h http.Handler, id, template string, start time.Time, rounds int) store.Session {
	t.Helper()
	at := start.UnixMilli()
	for r := range rounds {
		for e, ex := range circuitPlan {
			s := store.Set{ID: fmt.Sprintf("%s-%d-%d", id, r, e), SessionID: id, ExerciseIndex: e, ExerciseName: ex.Name, SetIndex: r,
				TargetReps: ex.Sets[r].Reps, TargetWeightKg: ex.Sets[r].WeightKg, Reps: ex.Sets[r].Reps, WeightKg: ex.Sets[r].WeightKg,
				StartedAt: at, EndedAt: at + 40_000, UpdatedAt: at + 40_000}
			if w := do(t, h, "PUT", "/v1/sets/"+s.ID, s); w.Code != 200 {
				t.Fatalf("set %s: %d %s", s.ID, w.Code, w.Body)
			}
			at += 70_000
		}
	}
	fin := at
	s := store.Session{ID: id, TemplateID: template, Name: "Circuit", Plan: circuitPlan, StartedAt: start.UnixMilli(), FinishedAt: &fin, UpdatedAt: fin}
	if w := do(t, h, "PUT", "/v1/sessions/"+id, s); w.Code != 200 {
		t.Fatalf("session %s: %d %s", id, w.Code, w.Body)
	}
	return s
}

func summaryOf(t *testing.T, h http.Handler, id string) (int, map[string]json.RawMessage) {
	t.Helper()
	w := do(t, h, "GET", "/v1/sessions/"+id+"/summary", nil)
	var out map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func mergeAPI(t *testing.T) (*API, http.Handler) {
	t.Helper()
	a, h := newTestAPI(t)
	a.Ingest = &ingest.Ingester{Store: a.Store, StorageDir: t.TempDir(), Log: a.Log, Loc: time.UTC}
	return a, h
}

var morning = time.Date(2026, 11, 3, 7, 0, 0, 0, time.UTC)

// 1 round, then the same circuit again for 3: one 4-round session, with
// Whoop's recording of the second part on its activity.
func TestMergeSessions(t *testing.T) {
	a, h := mergeAPI(t)
	ctx := context.Background()
	upload(t, h, "first", "circuit", morning, 1)
	secondStart := morning.Add(10 * time.Minute)
	upload(t, h, "second", "circuit", secondStart, 3)
	strain := 6.1
	whoop, err := a.Ingest.AddWhoopWorkout(ctx, "w1", secondStart, 7*60, 0,
		ingest.Hint{Source: "whoop", ExternalID: "w1", Sport: "strength", Strain: &strain}, 130, 160, [6]int64{})
	if err != nil {
		t.Fatal(err)
	}
	if act, err := a.Store.ActivityBySession(ctx, "second"); err != nil || act.ID != whoop.ActivityID {
		t.Fatalf("setup: Whoop's workout should be on the second session's activity: %v %v", act.ID, err)
	}

	// Asked from the later one: the earlier is kept.
	w := do(t, h, "POST", "/v1/sessions/second/merge", map[string]string{"with": "first"})
	if w.Code != 200 {
		t.Fatalf("merge: %d %s", w.Code, w.Body)
	}
	var res map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res["id"] != "first" || res["merged"] != "second" {
		t.Fatalf("merge result %v", res)
	}

	sess, err := a.Store.GetSession(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Plan) != 2 || len(sess.Plan[0].Sets) != 4 || len(sess.Plan[1].Sets) != 4 {
		t.Fatalf("plan %+v, want 4 rounds", sess.Plan)
	}
	sets, _ := a.Store.SessionSets(ctx, "first")
	if len(sets) != 8 {
		t.Fatalf("%d sets, want 8", len(sets))
	}
	rounds := map[int]int{}
	for _, s := range sets {
		rounds[s.SetIndex]++
	}
	if rounds[0] != 2 || rounds[3] != 2 {
		t.Fatalf("rounds %v, want 0-3 with both exercises in each", rounds)
	}
	if *sess.FinishedAt <= secondStart.UnixMilli() {
		t.Fatal("finish should be the second part's")
	}
	code, sum := summaryOf(t, h, "first")
	if code != 200 || string(sum["progress"]) != `{"done":8,"logged":8,"planned":8}` {
		t.Fatalf("summary %d progress %s", code, sum["progress"])
	}

	// The second is gone, and says where it went.
	if _, err := a.Store.GetSession(ctx, "second"); err == nil {
		t.Fatal("second still there")
	}
	code, sum = summaryOf(t, h, "second")
	if code != http.StatusGone || string(sum["merged_into"]) != `"first"` {
		t.Fatalf("second's summary: %d %v", code, sum)
	}

	// One activity, on the kept session, with the app's sets and Whoop's
	// recording; none left on the second.
	act, err := a.Store.ActivityBySession(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	srcs, _ := a.Store.SourcesFor(ctx, act.ID)
	got := map[string]bool{}
	for _, s := range srcs {
		got[s.Source] = true
	}
	if !got["app"] || !got["whoop"] || len(srcs) != 2 {
		t.Fatalf("sources %+v, want the app's and Whoop's", srcs)
	}
	if _, err := a.Store.ActivityBySession(ctx, "second"); err == nil {
		t.Fatal("an activity still hangs off the merged-away session")
	}

	w = do(t, h, "GET", "/v1/sessions/merged?since=0", nil)
	var merges []store.Merge
	_ = json.Unmarshal(w.Body.Bytes(), &merges)
	if len(merges) != 1 || merges[0].From != "second" || merges[0].Into != "first" {
		t.Fatalf("merges %+v", merges)
	}
}

// A phone that still holds the merged-away session keeps uploading to it.
// Nothing it sends may bring it back or knock the merged sets out of place.
func TestMergeLateUploads(t *testing.T) {
	a, h := mergeAPI(t)
	ctx := context.Background()
	upload(t, h, "first", "circuit", morning, 1)
	second := upload(t, h, "second", "circuit", morning.Add(10*time.Minute), 3)
	if w := do(t, h, "POST", "/v1/sessions/first/merge", map[string]string{"with": "second"}); w.Code != 200 {
		t.Fatalf("merge: %d %s", w.Code, w.Body)
	}

	// The session itself, replayed.
	second.UpdatedAt = time.Now().UnixMilli()
	if w := do(t, h, "PUT", "/v1/sessions/second", second); w.Code != 200 {
		t.Fatalf("replayed session: %d %s", w.Code, w.Body)
	}
	if _, err := a.Store.GetSession(ctx, "second"); err == nil {
		t.Fatal("replaying the session brought it back")
	}

	// An edit to a set that moved: the new reps land, the place stays.
	edit := store.Set{ID: "second-0-0", SessionID: "second", ExerciseIndex: 0, ExerciseName: "Goblet squat", SetIndex: 0,
		TargetReps: 10, TargetWeightKg: 20, Reps: 8, WeightKg: 20,
		StartedAt: morning.Add(10 * time.Minute).UnixMilli(), EndedAt: morning.Add(11 * time.Minute).UnixMilli(), UpdatedAt: time.Now().Add(time.Minute).UnixMilli()}
	if w := do(t, h, "PUT", "/v1/sets/"+edit.ID, edit); w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	got, err := a.Store.GetSet(ctx, edit.ID)
	if err != nil || got.SessionID != "first" || got.SetIndex != 1 || got.Reps != 8 {
		t.Fatalf("edited set %+v %v, want it on first, round 2 (index 1), 8 reps", got, err)
	}

	// A set the server never saw: after the merged session's push-ups.
	late := store.Set{ID: "second-late", SessionID: "second", ExerciseIndex: 1, ExerciseName: "Push-up", SetIndex: 3,
		TargetReps: 12, Reps: 12, StartedAt: morning.Add(30 * time.Minute).UnixMilli(), EndedAt: morning.Add(31 * time.Minute).UnixMilli(),
		UpdatedAt: time.Now().UnixMilli()}
	if w := do(t, h, "PUT", "/v1/sets/"+late.ID, late); w.Code != 200 {
		t.Fatalf("late set: %d %s", w.Code, w.Body)
	}
	got, _ = a.Store.GetSet(ctx, late.ID)
	sess, _ := a.Store.GetSession(ctx, "first")
	if got.SessionID != "first" || got.ExerciseIndex != 1 || got.SetIndex != 4 || len(sess.Plan[1].Sets) != 5 {
		t.Fatalf("late set %+v, push-ups planned %d; want set index 4 of 5", got, len(sess.Plan[1].Sets))
	}
	// One for an exercise the merged session doesn't have can't be placed.
	late.ID, late.ExerciseName = "second-odd", "Calf raise"
	if w := do(t, h, "PUT", "/v1/sets/"+late.ID, late); w.Code != http.StatusConflict {
		t.Fatalf("unplaceable set: %d, want 409", w.Code)
	}

	// Discarding it on the phone mustn't touch the session it became.
	if w := do(t, h, "DELETE", "/v1/sessions/second", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := a.Store.GetSession(ctx, "first"); err != nil {
		t.Fatal("deleting the merged-away session deleted the kept one")
	}
}

func TestMergeRefusals(t *testing.T) {
	a, h := mergeAPI(t)
	upload(t, h, "first", "circuit", morning, 1)
	upload(t, h, "next-day", "circuit", morning.Add(24*time.Hour), 3)
	open := store.Session{ID: "open", TemplateID: "circuit", Name: "Circuit", Plan: circuitPlan,
		StartedAt: morning.Add(time.Hour).UnixMilli(), UpdatedAt: morning.Add(time.Hour).UnixMilli()}
	if err := a.Store.PutSession(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, id, with string
		want           int
	}{
		{"itself", "first", "first", 400},
		{"unknown", "first", "nope", 404},
		{"bad id", "first", "../x", 400},
		{"another day", "first", "next-day", 409},
		{"still going", "first", "open", 409},
	} {
		if w := do(t, h, "POST", "/v1/sessions/"+c.id+"/merge", map[string]string{"with": c.with}); w.Code != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, w.Code, w.Body, c.want)
		}
	}
	if sets, _ := a.Store.SessionSets(context.Background(), "first"); len(sets) != 2 {
		t.Fatal("a refused merge changed the session")
	}
}

// A merge into a session that was itself merged into: the first merge's
// redirect follows to the session that's left.
func TestMergeChain(t *testing.T) {
	a, h := mergeAPI(t)
	upload(t, h, "a", "circuit", morning, 1)
	upload(t, h, "b", "circuit", morning.Add(10*time.Minute), 1)
	upload(t, h, "c", "circuit", morning.Add(20*time.Minute), 1)
	for _, p := range [][2]string{{"b", "c"}, {"a", "b"}} {
		if w := do(t, h, "POST", "/v1/sessions/"+p[0]+"/merge", map[string]string{"with": p[1]}); w.Code != 200 {
			t.Fatalf("merge %v: %d %s", p, w.Code, w.Body)
		}
	}
	for _, gone := range []string{"b", "c"} {
		if into, _ := a.Store.MergedInto(context.Background(), gone); into != "a" {
			t.Errorf("%s merged into %q, want a", gone, into)
		}
	}
	if sets, _ := a.Store.SessionSets(context.Background(), "a"); len(sets) != 6 {
		t.Fatalf("%d sets, want all three rounds' 6", len(sets))
	}
}

// The summary lists the day's other sessions, and suggests the one this
// looks like the continuation of: the same workout, within two hours.
func TestMergeSuggestion(t *testing.T) {
	_, h := mergeAPI(t)
	upload(t, h, "first", "circuit", morning, 1)
	upload(t, h, "second", "circuit", morning.Add(40*time.Minute), 3)
	upload(t, h, "other", "upper", morning.Add(3*time.Hour), 1)
	upload(t, h, "later", "circuit", morning.Add(6*time.Hour), 2)

	type ref struct{ ID string }
	read := func(id string) ([]string, string) {
		_, sum := summaryOf(t, h, id)
		var same []ref
		var suggest string
		_ = json.Unmarshal(sum["same_day"], &same)
		_ = json.Unmarshal(sum["suggest_merge"], &suggest)
		var ids []string
		for _, r := range same {
			ids = append(ids, r.ID)
		}
		return ids, suggest
	}
	if same, suggest := read("second"); len(same) != 3 || suggest != "first" {
		t.Fatalf("second: same day %v, suggest %q; want the 3 others and first", same, suggest)
	}
	if _, suggest := read("first"); suggest != "" {
		t.Fatalf("first suggests %q: only the later part should", suggest)
	}
	if _, suggest := read("other"); suggest != "" {
		t.Fatalf("a different workout suggests %q", suggest)
	}
	if _, suggest := read("later"); suggest != "" {
		t.Fatalf("hours later suggests %q", suggest)
	}
}

// A dry run shows the merged session and changes nothing.
func TestMergeDryRun(t *testing.T) {
	a, h := mergeAPI(t)
	ctx := context.Background()
	upload(t, h, "first", "circuit", morning, 1)
	upload(t, h, "second", "circuit", morning.Add(10*time.Minute), 3)
	w := do(t, h, "POST", "/v1/sessions/second/merge?dry_run=1", map[string]string{"with": "first"})
	if w.Code != 200 {
		t.Fatalf("dry run: %d %s", w.Code, w.Body)
	}
	var p struct {
		ID, Merged, Name string
		Plan             []store.Exercise
		Sets             int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if p.ID != "first" || p.Merged != "second" || p.Sets != 8 || len(p.Plan) != 2 || len(p.Plan[0].Sets) != 4 {
		t.Fatalf("preview %+v", p)
	}
	if _, err := a.Store.GetSession(ctx, "second"); err != nil {
		t.Fatal("a dry run merged")
	}
	if sets, _ := a.Store.SessionSets(ctx, "first"); len(sets) != 2 {
		t.Fatal("a dry run moved sets")
	}
	if into, _ := a.Store.MergedInto(ctx, "second"); into != "" {
		t.Fatal("a dry run recorded a merge")
	}
}
