package coach_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
)

// Questions go out in the shape every provider takes: instructions always,
// criteria as a label map for choice and a level list for score.
func TestSystemOneQuestionShape(t *testing.T) {
	var got map[string]json.RawMessage
	f := &coachtest.Fake{SystemOne: func(_ json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
		got = qs
		return map[string]coach.Answer{"sport": {Type: "choice", Choice: "strength", Probabilities: map[string]float64{"strength": 0.93}}}
	}}
	sport, ok, err := f.AI(t).Sport(context.Background(), coach.WorkoutFacts{Name: "Afternoon Workout", Minutes: 50})
	if err != nil || !ok || sport != "strength" {
		t.Fatalf("Sport = %q %v %v", sport, ok, err)
	}
	var q struct {
		Type         string            `json:"type"`
		Instructions string            `json:"instructions"`
		Criteria     map[string]string `json:"criteria"`
	}
	if err := json.Unmarshal(got["sport"], &q); err != nil || q.Type != "choice" || q.Instructions == "" || len(q.Criteria) < 2 {
		t.Fatalf("question = %s", got["sport"])
	}
}

func TestSportNeedsConfidence(t *testing.T) {
	for name, a := range map[string]coach.Answer{
		"unsure":  {Type: "choice", Choice: "run", Probabilities: map[string]float64{"run": 0.6}},
		"other":   {Type: "choice", Choice: "other", Probabilities: map[string]float64{"other": 0.99}},
		"refusal": {Type: "refusal"},
	} {
		f := &coachtest.Fake{SystemOne: func(json.RawMessage, map[string]json.RawMessage) map[string]coach.Answer {
			return map[string]coach.Answer{"sport": a}
		}}
		if sp, ok, err := f.AI(t).Sport(context.Background(), coach.WorkoutFacts{}); ok || err != nil {
			t.Errorf("%s: took %q (err %v)", name, sp, err)
		}
	}
}

func TestSetEffortsScale(t *testing.T) {
	f := &coachtest.Fake{SystemOne: func(_ json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
		var q struct {
			Criteria []string `json:"criteria"`
		}
		_ = json.Unmarshal(qs["s1"], &q)
		if strings.Join(q.Criteria, ",") != "easy,working,hard,limit" {
			t.Errorf("levels = %v", q.Criteria)
		}
		return map[string]coach.Answer{
			"s1":    {Type: "score", Score: 2.89, Confidence: 0.8},
			"s2":    {Type: "score", Score: 0.4, Confidence: 0.33}, // a real model's usual
			"s3":    {Type: "score", Score: 1.5, Confidence: 0.2},  // unsure: left out
			"ghost": {Type: "score", Score: 1},                     // never asked: ignored
		}
	}}
	sets := map[string]coach.SetState{"s1": {Exercise: "Back Squat", Reps: 5}, "s2": {}, "s3": {}}
	got, err := f.AI(t).SetEfforts(context.Background(), sets)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["s1"].Level != "limit" || got["s2"].Level != "easy" {
		t.Fatalf("efforts = %+v", got)
	}
}

func TestNotesChecked(t *testing.T) {
	reply := ""
	f := &coachtest.Fake{Chat: func(string, string) string { return reply }}
	w := &coach.Writer{AI: f.AI(t), KV: &mapKV{}, Log: slog.New(slog.DiscardHandler)}
	ctx := context.Background()
	np := 182
	facts := coach.ActivityFacts{Name: "Two Bridges Loop", Sport: "virtual_ride", Minutes: 62, NP: &np}

	reply = `{"note": "Steady endurance on Zwift. NP 182 W for 62 minutes, never above zone 3."}`
	if _, ok, pending := w.ActivityNote(ctx, "a1", facts); ok || !pending {
		t.Fatalf("first: ok=%v pending=%v", ok, pending)
	}
	waitFor(t, func() bool { _, ok, _ := w.ActivityNote(ctx, "a1", facts); return ok })
	n, _, _ := w.ActivityNote(ctx, "a1", facts)
	if !strings.HasPrefix(n.Text, "Steady endurance") || n.Model != "fake-llm" {
		t.Fatalf("note = %+v", n)
	}

	reply = `{"note": "A hard 75 minutes at 240 W."}` // numbers not in the facts
	if _, _, pending := w.ActivityNote(ctx, "a2", facts); !pending {
		t.Fatal("a2 not started")
	}
	waitFor(t, func() bool { _, _, pending := w.ActivityNote(ctx, "a2", facts); return !pending })
	if _, ok, _ := w.ActivityNote(ctx, "a2", facts); ok {
		t.Fatal("invented numbers accepted")
	}
}

func TestDescribeTags(t *testing.T) {
	f := &coachtest.Fake{Chat: func(system, user string) string {
		if !strings.Contains(system, "tired legs") || !strings.Contains(user, "donuts") {
			t.Errorf("prompt: %q / %q", system, user)
		}
		return `{"tags": ["Tired Legs", "fun", "rain", "rain"], "note": "9 donuts on the ride"}`
	}}
	w := &coach.Writer{AI: f.AI(t), KV: &mapKV{}, Log: slog.New(slog.DiscardHandler)}
	tg, err := w.DescribeTags(context.Background(), "Soggy and slow, legs dead. Deduct 45 min for my 9 donuts.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tg.Tags, ",") != "tired legs,rain" || tg.Note != "9 donuts on the ride" {
		t.Fatalf("tags = %+v", tg)
	}
}

func TestParseWorkout(t *testing.T) {
	f := &coachtest.Fake{Chat: func(string, string) string {
		return "```json\n" + `{"name": "Lower", "exercises": [{"name": "Back Squat", "rest_seconds": 180,
			"sets": [{"reps": 5, "weight_kg": 100}, {"reps": 5, "weight_kg": 100}]}]}` + "\n```"
	}}
	w := &coach.Writer{AI: f.AI(t), KV: &mapKV{}, Log: slog.New(slog.DiscardHandler)}
	name, ex, err := w.ParseWorkout(context.Background(), "2x5 squat at 100, rest 3 min")
	if err != nil || name != "Lower" || len(ex) != 1 || len(ex[0].Sets) != 2 || ex[0].RestSeconds != 180 {
		t.Fatalf("parse = %q %+v %v", name, ex, err)
	}
}

type mapKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *mapKV) SyncState(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[key], nil
}

func (k *mapKV) SetSyncState(_ context.Context, key, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = v
	return nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 400 {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestOneOff(t *testing.T) {
	for err, want := range map[error]bool{
		&coach.StatusError{Status: 400}: true,
		&coach.StatusError{Status: 404}: false, // an Outpost without the endpoint
		&coach.StatusError{Status: 503}: false,
		&coach.StatusError{Status: 401}: false,
		errors.New("reply is not JSON"): true,
		context.DeadlineExceeded:        false,
	} {
		if got := coach.OneOff(err); got != want {
			t.Errorf("OneOff(%v) = %v", err, got)
		}
	}
}
