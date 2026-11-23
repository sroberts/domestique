package coach

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// System 1 caps a question's length, options included. Refused, the
// question is asked again with the nearest half of the names, until it fits.
func TestSameExerciseFitsTheCap(t *testing.T) {
	var offered []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]struct {
				Instructions string            `json:"instructions"`
				Criteria     map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		q := body.Questions["same"]
		n := len(q.Criteria) - 1 // less "new exercise"
		offered = append(offered, n)
		if n > 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"question 0: instructions with the question type have 57 tokens; limit is 31 with these options"}`)
			return
		}
		fmt.Fprint(w, `{"model":"laya","answers":{"same":{"type":"choice","choice":"Romanian Deadlift","probabilities":{"Romanian Deadlift":0.97,"new exercise":0.03}}}}`)
	}))
	defer srv.Close()
	a := &AI{URL: srv.URL, Token: "t", HTTP: &http.Client{Timeout: 5 * time.Second}}
	known := []string{"Romanian Deadlift", "Deadlift", "Back Squat", "Bench Press", "Pull-Up", "Overhead Press"}
	got, p, err := a.SameExercise(context.Background(), "RDL", known)
	if err != nil || got != "Romanian Deadlift" || p != 0.97 {
		t.Fatalf("got %q %v %v", got, p, err)
	}
	if fmt.Sprint(offered) != "[6 3 1]" {
		t.Errorf("names offered per try: %v, want [6 3 1]", offered)
	}
}

// Any other refusal is returned, not retried.
func TestSameExerciseOtherErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"no model"}`)
	}))
	defer srv.Close()
	a := &AI{URL: srv.URL, Token: "t", HTTP: &http.Client{Timeout: 5 * time.Second}}
	if _, _, err := a.SameExercise(context.Background(), "RDL", []string{"Romanian Deadlift", "Deadlift"}); err == nil || calls != 1 {
		t.Fatalf("err %v after %d calls", err, calls)
	}
}
