package coach_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
)

func TestNearest(t *testing.T) {
	known := []string{"Back Squat", "Romanian Deadlift", "Bench Press", "Pull-Up", "Overhead Press", "Leg Curl", "Barbell Row", "Deadlift"}
	for name, want := range map[string]string{
		"RDL":       "Romanian Deadlift",
		"BB bench":  "Bench Press",
		"Pull Ups":  "Pull-Up",
		"Bak Squat": "Back Squat",
		"OHP":       "Overhead Press",
	} {
		got := coach.Nearest(name, known, 3)
		if len(got) != 3 || !contains(got, want) {
			t.Errorf("Nearest(%q) = %v, want %q among them", name, got, want)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestSameExercise(t *testing.T) {
	var q struct {
		Criteria map[string]string `json:"criteria"`
	}
	f := &coachtest.Fake{SystemOne: func(_ json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
		_ = json.Unmarshal(qs["same"], &q)
		return map[string]coach.Answer{"same": {Type: "choice", Choice: "Bench Press", Probabilities: map[string]float64{"Bench Press": 0.95}}}
	}}
	got, p, err := f.AI(t).SameExercise(context.Background(), "BB bench", []string{"Bench Press", "Back Squat"})
	if err != nil || got != "Bench Press" || p != 0.95 {
		t.Fatalf("= %q %v %v", got, p, err)
	}
	if len(q.Criteria) != 3 || q.Criteria[coach.NewExercise] == "" {
		t.Fatalf("criteria = %v", q.Criteria)
	}
	f.SystemOne = func(json.RawMessage, map[string]json.RawMessage) map[string]coach.Answer {
		return map[string]coach.Answer{"same": {Type: "choice", Choice: "Squat Jump", Probabilities: map[string]float64{"Squat Jump": 0.99}}}
	}
	if _, _, err := f.AI(t).SameExercise(context.Background(), "x", []string{"Bench Press"}); err == nil {
		t.Fatal("an answer never offered was taken")
	}
}

func TestParseLog(t *testing.T) {
	reply := ""
	f := &coachtest.Fake{Chat: func(system, user string) string {
		if !strings.Contains(system, "Sunday 2026-11-08") {
			t.Errorf("today missing from prompt")
		}
		return reply
	}}
	w := &coach.Writer{AI: f.AI(t), KV: &mapKV{}, Log: slog.New(slog.DiscardHandler)}
	today := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	reply = `{"name":"Push","date":"2026-11-07","time":"7:05","minutes":40,
		"exercises":[{"name":"Push-Up","sets":[{"reps":10,"weight_kg":0}]}],"not_logged":["20 min walk", " "]}`
	got, err := w.ParseLog(context.Background(), "yesterday", today)
	if err != nil || got.Date != "2026-11-07" || got.Time != "7:05" || got.Minutes != 40 || len(got.NotLogged) != 1 {
		t.Fatalf("= %+v %v", got, err)
	}
	// A day in the future, a bad clock and a silly duration are dropped.
	reply = `{"date":"2026-11-09","time":"25:00","minutes":900,"exercises":[{"name":"Squat","sets":[{"reps":5,"weight_kg":80}]}]}`
	got, err = w.ParseLog(context.Background(), "tomorrow?", today)
	if err != nil || got.Date != "" || got.Time != "" || got.Minutes != 0 {
		t.Fatalf("= %+v %v", got, err)
	}
	reply = `{"exercises":[],"not_logged":["a run"]}`
	if _, err := w.ParseLog(context.Background(), "ran 5k", today); err == nil {
		t.Fatal("no lifting accepted")
	}
}
