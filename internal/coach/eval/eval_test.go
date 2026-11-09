package eval_test

import (
	"context"
	"encoding/json"
	"testing"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/coach/eval"
)

// Every question has cases, and they parse.
func TestCases(t *testing.T) {
	for _, n := range eval.Names() {
		cs, err := eval.Cases(n)
		if err != nil || len(cs) == 0 {
			t.Errorf("%s: %d cases, %v", n, len(cs), err)
		}
	}
}

// A model that says yes to everything is WRONG on every "no" case, and the
// safe bar sits above its answers.
func TestScoring(t *testing.T) {
	f := &coachtest.Fake{SystemOne: func(_ json.RawMessage, qs map[string]json.RawMessage) map[string]coach.Answer {
		return map[string]coach.Answer{"stray": {Type: "noul", Noul: 0.97}}
	}}
	sc, err := eval.Run(context.Background(), f.AI(t), "stray")
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := eval.Cases("stray")
	no := 0
	for _, c := range cs {
		if c.Want == "no" {
			no++
		}
	}
	if sc.Counts[eval.Wrong] != no || sc.Counts[eval.Hit] != len(cs)-no {
		t.Fatalf("counts = %v", sc.Counts)
	}
	if sc.SafeBar < 0.97 || sc.SafeHits != 0 {
		t.Fatalf("safe bar %.2f, %d hits", sc.SafeBar, sc.SafeHits)
	}
}
