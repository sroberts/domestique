package strength

import (
	"strings"
	"testing"

	"domestique/internal/store"
)

func TestAfterCheckIns(t *testing.T) {
	normal := Readiness{Level: "normal", Title: "Train as planned", Detail: "Recovery is 80%."}
	push := Readiness{Level: "push", Title: "Fresh and recovered", Detail: "Recovery 90%, form +8."}
	easy := Readiness{Level: "easy", Title: "Ease off today", Detail: "Recovery is 20%."}

	if r := AfterCheckIns(normal, nil); r != normal {
		t.Fatalf("no check-ins changed the call: %+v", r)
	}
	r := AfterCheckIns(normal, []store.CheckIn{{Legs: "dead"}})
	if r.Level != "hold" || r.Title != "Legs still tired" || !strings.Contains(r.Detail, "dead yesterday") {
		t.Errorf("dead legs: %+v", r)
	}
	r = AfterCheckIns(push, []store.CheckIn{{Legs: "heavy"}})
	if r.Level != "normal" || !strings.HasSuffix(r.Detail, "Legs felt heavy yesterday.") {
		t.Errorf("heavy legs on a push day: %+v", r)
	}
	if r = AfterCheckIns(normal, []store.CheckIn{{Legs: "heavy"}}); r.Level != "normal" {
		t.Errorf("heavy legs on a normal day: %+v", r)
	}
	r = AfterCheckIns(normal, []store.CheckIn{{Pain: "pain", Where: []string{"knee"}}, {Legs: "fresh"}})
	if r.Level != "hold" || !strings.Contains(r.Detail, "Yesterday's knee pain changed the workout") || !strings.Contains(r.Detail, "Hold the weights") {
		t.Errorf("pain: %+v", r)
	}
	r = AfterCheckIns(normal, []store.CheckIn{{Pain: "niggle", Where: []string{"back"}}})
	if r.Level != "normal" || !strings.HasSuffix(r.Detail, "Keep an eye on the lower back niggle.") {
		t.Errorf("niggle: %+v", r)
	}
	// Never harder, and an easy day stays easy, its words kept.
	r = AfterCheckIns(easy, []store.CheckIn{{Legs: "dead", Pain: "pain"}})
	if r.Level != "easy" || r.Title != easy.Title || !strings.HasPrefix(r.Detail, easy.Detail) || strings.Contains(r.Detail, "Hold the weights") {
		t.Errorf("easy day: %+v", r)
	}
}
