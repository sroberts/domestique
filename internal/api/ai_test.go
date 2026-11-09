package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/store"
	"domestique/internal/strength"
)

func TestParseTemplate(t *testing.T) {
	a, _, h := testAPI(t)
	if w := call(t, h, "POST", "/v1/templates/parse", `{"text":"5x5 squat at 100"}`); w.Code != 503 {
		t.Fatalf("no model: %d %s", w.Code, w.Body)
	}
	f := &coachtest.Fake{Chat: func(string, string) string {
		return `{"name":"Lower","exercises":[{"name":"Back Squat","rest_seconds":180,"sets":[{"reps":5,"weight_kg":100},{"reps":5,"weight_kg":100}]}]}`
	}}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: a.Store, Log: a.Log}
	w := call(t, h, "POST", "/v1/templates/parse", `{"text":"2x5 squat at 100, rest 3 min"}`)
	var got struct {
		Name      string           `json:"name"`
		Exercises []store.Exercise `json:"exercises"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Name != "Lower" || len(got.Exercises[0].Sets) != 2 {
		t.Fatalf("parse: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "POST", "/v1/templates/parse", `{"text":""}`); w.Code != 400 {
		t.Fatalf("empty text: %d", w.Code)
	}
	// What the model returns must still make a valid template.
	f.Chat = func(string, string) string {
		return `{"exercises":[{"name":"Squat","sets":[{"reps":0,"weight_kg":100}]}]}`
	}
	if w := call(t, h, "POST", "/v1/templates/parse", `{"text":"squat"}`); w.Code != 422 {
		t.Fatalf("invalid reading: %d %s", w.Code, w.Body)
	}
}

func TestStrayKept(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	if err := st.PutActivity(ctx, store.Activity{ID: "a1", Sport: "ride", Name: "Test", StartAt: at, ElapsedS: 60, Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSource(ctx, store.Source{ID: "strava:1", ActivityID: "a1", Source: "strava", Sport: "ride",
		StartAt: at, ElapsedS: 60, Extra: []byte("{}"), ImportedAt: at}); err != nil {
		t.Fatal(err)
	}
	a.Ingest = &ingest.Ingester{Store: st, Log: a.Log, Loc: time.UTC}
	_ = st.SetSyncState(ctx, load.StrayPrefix+"a1", "stray")
	var d detailResp
	w := call(t, h, "GET", "/v1/activities/a1", "")
	if _ = json.Unmarshal(w.Body.Bytes(), &d); !d.Stray {
		t.Fatalf("detail: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "POST", "/v1/activities/a1/keep", ""); w.Code != 200 {
		t.Fatalf("keep: %d %s", w.Code, w.Body)
	}
	if s, _ := load.Strays(ctx, st); s["a1"] {
		t.Fatal("still stray after keep")
	}
	if w := call(t, h, "POST", "/v1/activities/nope/keep", ""); w.Code != 404 {
		t.Fatalf("keep missing: %d", w.Code)
	}
}

func TestWeekNote(t *testing.T) {
	a, st, h := testAPI(t)
	w := call(t, h, "GET", "/v1/week-note", "")
	var r weekNoteResp
	if _ = json.Unmarshal(w.Body.Bytes(), &r); w.Code != 200 || r.AI || r.Note != nil {
		t.Fatalf("no model: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "GET", "/v1/week-note?week=2026-11-04", ""); w.Code != 400 {
		t.Fatalf("not a Monday: %d", w.Code)
	}
	var facts string
	f := &coachtest.Fake{Chat: func(_, user string) string {
		facts = user
		return `{"note":"A light week: 90 minutes on the bike. Lift twice next week."}`
	}}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: st, Log: a.Log}
	// Last week, with one ride in it.
	now := time.Now().UTC()
	mon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	mon = mon.AddDate(0, 0, -((int(mon.Weekday())+6)%7)-7)
	at := mon.Add(26 * time.Hour).UnixMilli()
	if err := st.PutActivity(context.Background(), store.Activity{ID: "r1", Sport: "ride", Name: "Tuesday ride", StartAt: at,
		ElapsedS: 5400, MovingS: 5400, TSS: 80, Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	get := func() weekNoteResp {
		var r weekNoteResp
		_ = json.Unmarshal(call(t, h, "GET", "/v1/week-note", "").Body.Bytes(), &r)
		return r
	}
	if r := get(); !r.AI || r.Note == nil || !r.Note.Pending || r.Week != mon.Format("2006-01-02") {
		t.Fatalf("first: %+v", r)
	}
	for range 200 {
		if r = get(); r.Note != nil && r.Note.Text != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r.Note == nil || !strings.HasPrefix(r.Note.Text, "A light week") {
		t.Fatalf("note: %+v", r.Note)
	}
	if !strings.Contains(facts, "Tuesday ride (90 min)") || !strings.Contains(facts, `"hours": 1.5`) {
		t.Fatalf("facts sent: %s", facts)
	}
}

// A finished session's summary carries the LLM's debrief, written from the
// sets as lifted, missed reps included.
func TestSessionNote(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	start := time.Now().Add(-48 * time.Hour).UnixMilli()
	end := start + 40*60*1000
	plan := []store.Exercise{{Name: "Back Squat", RestSeconds: 180, Change: "Up 2.5 kg: every set hit 5 last time",
		Sets: []store.TargetSet{{Reps: 5, WeightKg: 102.5}, {Reps: 5, WeightKg: 102.5}}}}
	if err := st.PutSession(ctx, store.Session{ID: "s1", Name: "Lower A", Plan: plan, StartedAt: start, FinishedAt: &end, UpdatedAt: end}); err != nil {
		t.Fatal(err)
	}
	for i, reps := range []int{5, 4} {
		at := start + int64(i)*5*60*1000
		if err := st.PutSet(ctx, store.Set{ID: fmt.Sprintf("set%d", i), SessionID: "s1", ExerciseName: "Back Squat", SetIndex: i,
			TargetReps: 5, TargetWeightKg: 102.5, Reps: reps, WeightKg: 102.5, StartedAt: at, EndedAt: at + 40000, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	var facts string
	f := &coachtest.Fake{Chat: func(_, user string) string {
		facts = user
		return `{"note":"Squat went up to 102.5 kg. The second set stopped one short at 4 of 5."}`
	}}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: st, Log: a.Log}

	get := func() *strength.AINote {
		w := call(t, h, "GET", "/v1/sessions/s1/summary", "")
		var s strength.Summary
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
			t.Fatalf("summary: %d %s", w.Code, w.Body)
		}
		return s.Note
	}
	if n := get(); n == nil || !n.Pending {
		t.Fatalf("first: %+v", n)
	}
	var n *strength.AINote
	for range 200 {
		if n = get(); n != nil && n.Text != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n == nil || !strings.HasPrefix(n.Text, "Squat went up") {
		t.Fatalf("note: %+v", n)
	}
	for _, want := range []string{`"reps": 4`, `"target_reps": 5`, "Up 2.5 kg", `"sets_planned": 2`} {
		if !strings.Contains(facts, want) {
			t.Errorf("facts lack %s: %s", want, facts)
		}
	}
}

// With heart rate in, each set gets System 1's effort, keyed by set ID.
func TestSetEfforts(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	start := time.Now().Add(-3 * time.Hour).Truncate(time.Second).UnixMilli()
	end := start + 12*60*1000
	plan := []store.Exercise{{Name: "Back Squat", Sets: []store.TargetSet{{Reps: 5, WeightKg: 100}, {Reps: 5, WeightKg: 100}}}}
	if err := st.PutSession(ctx, store.Session{ID: "s2", Name: "Lower", Plan: plan, StartedAt: start, FinishedAt: &end, UpdatedAt: end}); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		at := start + int64(i)*5*60*1000
		_ = st.PutSet(ctx, store.Set{ID: fmt.Sprintf("e%d", i), SessionID: "s2", ExerciseName: "Back Squat", SetIndex: i,
			TargetReps: 5, TargetWeightKg: 100, Reps: 5, WeightKg: 100, StartedAt: at, EndedAt: at + 40000, UpdatedAt: at})
	}
	var hr []store.HRSample
	for ts := start - 4*60*1000; ts <= end+4*60*1000; ts += 1000 {
		bpm := 95
		if off := (ts - start) % (5 * 60 * 1000); ts >= start && off < 70000 {
			bpm = 120 + int(off/1000) // climbs through the set and just after
		}
		hr = append(hr, store.HRSample{T: ts, BPM: bpm})
	}
	if err := st.PutHR(ctx, "test", hr); err != nil {
		t.Fatal(err)
	}
	var asked []string
	f := &coachtest.Fake{
		Chat: func(string, string) string { return `{"note":"Two solid sets of squats."}` },
		SystemOne: func(_ json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
			out := map[string]coach.Answer{}
			for id := range qs {
				asked = append(asked, id)
				out[id] = coach.Answer{Type: "score", Score: 2.1, Confidence: 0.8}
			}
			return out
		},
	}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: st, Log: a.Log}
	var s strength.Summary
	for range 200 {
		w := call(t, h, "GET", "/v1/sessions/s2/summary", "")
		s = strength.Summary{}
		_ = json.Unmarshal(w.Body.Bytes(), &s)
		if len(s.Efforts) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s.HRStatus != "ready" {
		t.Fatalf("hr status %q", s.HRStatus)
	}
	if s.Efforts["e0"].Level != "hard" || s.Efforts["e1"].Level != "hard" || len(asked) != 2 {
		t.Fatalf("efforts = %+v, asked %v", s.Efforts, asked)
	}
}
