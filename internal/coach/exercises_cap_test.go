package coach

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"
)

var sixNames = []string{"Romanian Deadlift", "Deadlift", "Back Squat", "Bench Press", "Pull-Up", "Overhead Press"}

// capServer refuses any question offering more than max names, and
// answers the rest with match (or "new exercise" when it isn't offered).
// It records the names offered per call.
func capServer(t *testing.T, max int, match func(offered []string) string) (*AI, *[][]string) {
	t.Helper()
	var calls [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]struct {
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var offered []string
		for k := range body.Questions["same"].Criteria {
			if k != NewExercise {
				offered = append(offered, k)
			}
		}
		sort.Strings(offered)
		calls = append(calls, offered)
		if len(offered) > max {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"question 0: instructions with the question type have 57 tokens; limit is 31 with these options"}`)
			return
		}
		c := match(offered)
		if c == "" {
			c = NewExercise
		}
		fmt.Fprintf(w, `{"model":"laya","answers":{"same":{"type":"choice","choice":%q,"probabilities":{%q:0.97}}}}`, c, c)
	}))
	t.Cleanup(srv.Close)
	return &AI{URL: srv.URL, Token: "t", HTTP: &http.Client{Timeout: 5 * time.Second}}, &calls
}

func offers(name string) func([]string) string {
	return func(o []string) string {
		for _, k := range o {
			if k == name {
				return name
			}
		}
		return ""
	}
}

// Refused as too long, the question is split in halves until it fits, and
// every name is still offered: the nearest half first, through two names.
func TestSameExerciseFitsTheCap(t *testing.T) {
	a, calls := capServer(t, 1, offers("Romanian Deadlift"))
	got, p, err := a.SameExercise(context.Background(), "RDL", sixNames)
	if err != nil || got != "Romanian Deadlift" || p != 0.97 {
		t.Fatalf("got %q %v %v", got, p, err)
	}
	var sizes []int
	seen := map[string]bool{}
	for _, c := range *calls {
		sizes = append(sizes, len(c))
		if len(c) == 1 {
			seen[c[0]] = true
		}
	}
	if fmt.Sprint(sizes) != "[6 3 2 1 1 1 3 2 1 1 1]" {
		t.Errorf("names offered per call: %v", sizes)
	}
	if len(seen) != len(sixNames) {
		t.Errorf("only %d of %d names offered on their own", len(seen), len(sixNames))
	}
}

// A split question offers fewer names, so its answers read as surer: a
// match with anything but the nearest name isn't trusted.
func TestSameExerciseSplitOnlyTrustsTheNearest(t *testing.T) {
	a, _ := capServer(t, 3, offers("Bench Press"))
	if got, _, err := a.SameExercise(context.Background(), "Bench", sixNames); err != nil || got != "" {
		t.Fatalf("got %q %v, want no match", got, err)
	}
	// Asked whole, the same answer stands.
	a, _ = capServer(t, 6, offers("Bench Press"))
	if got, _, err := a.SameExercise(context.Background(), "Bench", sixNames); err != nil || got != "Bench Press" {
		t.Fatalf("whole question: got %q %v", got, err)
	}
}

// Two parts each claiming a match is no answer.
func TestSameExerciseSplitDisputed(t *testing.T) {
	a, _ := capServer(t, 3, func(o []string) string { return o[0] })
	if got, _, err := a.SameExercise(context.Background(), "Romanian DL", sixNames); err != nil || got != "" {
		t.Fatalf("got %q %v, want no match", got, err)
	}
}

// Split up, an answer must be one of the names that part offered, not one
// offered only in another part.
func TestSameExerciseSplitNotOffered(t *testing.T) {
	a, _ := capServer(t, 3, func([]string) string { return "Deadlift" })
	if _, _, err := a.SameExercise(context.Background(), "DL", sixNames); err == nil {
		t.Fatal("want an error for a name the part didn't offer")
	}
}

// Too long even with one name, the refusal is returned.
func TestSameExerciseTooLongForOne(t *testing.T) {
	a, calls := capServer(t, 0, offers(""))
	if _, _, err := a.SameExercise(context.Background(), "RDL", sixNames[:2]); err == nil {
		t.Fatal("want the refusal")
	}
	if len(*calls) != 2 { // two names, then the nearer one alone
		t.Errorf("%d calls, want 2", len(*calls))
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
