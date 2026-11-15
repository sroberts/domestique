package checkin

import (
	"slices"
	"strings"
	"testing"

	"domestique/internal/store"
)

func TestQuestionsFitTheWorkout(t *testing.T) {
	for _, c := range []struct {
		name string
		w    Workout
		want []string
	}{
		{"lifting session", Workout{Session: true, Planned: true, Minutes: 50}, []string{Effort, Legs, Plan, Pain}},
		{"long lifting session", Workout{Session: true, Planned: true, Minutes: 75}, []string{Effort, Legs, Plan, Pain, Fuel}},
		{"planned ride", Workout{Ride: true, Planned: true, Minutes: 90}, []string{Effort, Legs, Plan, Pain, Fuel}},
		{"unplanned ride", Workout{Ride: true, Minutes: 40}, []string{Effort, Legs, Pain, Fuel}},
		{"recovery spin", Workout{Ride: true, Planned: true, Recovery: true, Minutes: 45}, []string{Effort, Legs}},
		{"short run", Workout{Minutes: 30}, []string{Effort, Legs, Pain}},
		{"long walk", Workout{Minutes: 120}, []string{Effort, Legs, Pain, Fuel}},
	} {
		if got := Questions(c.w); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if n := len(Questions(c.w)); n < 2 || n > 5 {
			t.Errorf("%s: %d questions", c.name, n)
		}
	}
}

func TestCheck(t *testing.T) {
	c := store.CheckIn{Effort: " Hard ", Legs: "heavy", Pain: "niggle", Where: []string{"Knee", "knee", "back"}, Notes: "  sore  ", Skipped: true}
	if msg := Check(&c); msg != "" {
		t.Fatal(msg)
	}
	if c.Effort != "hard" || !slices.Equal(c.Where, []string{"knee", "back"}) || c.Notes != "sore" || c.Skipped {
		t.Fatalf("tidied %+v", c)
	}
	// No niggle, no body parts.
	c = store.CheckIn{Pain: "none", Where: []string{"knee"}}
	if Check(&c); c.Where != nil {
		t.Fatalf("where %v with nothing hurting", c.Where)
	}
	for _, bad := range []store.CheckIn{
		{Effort: "brutal"}, {Legs: "Very hard"}, {Plan: "yes"}, {Fuel: "ok"},
		{Pain: "pain", Where: []string{"spleen"}},
		{Pain: "pain", Where: []string{"knee", "hip", "calf", "neck", "foot"}},
		{Notes: strings.Repeat("x", 501)},
	} {
		if msg := Check(&bad); msg == "" {
			t.Errorf("%+v passed", bad)
		}
	}
	// A skip with no answers stays a skip.
	c = store.CheckIn{Skipped: true}
	if Check(&c); !c.Skipped || Answered(c) {
		t.Fatalf("skip %+v", c)
	}
}

func TestSRPEAndWords(t *testing.T) {
	c := store.CheckIn{Effort: "very_hard", Legs: "dead", Plan: "cut_short", Pain: "pain", Where: []string{"back", "knee"}, Fuel: "low_energy"}
	if got := SRPE(c, 60); got != 480 {
		t.Errorf("srpe %d, want 8 x 60", got)
	}
	if SRPE(store.CheckIn{Legs: "fresh"}, 60) != 0 {
		t.Error("srpe without effort")
	}
	if got := Line(c); got != "very hard, legs dead, cut short, pain that changed the workout: lower back, knee, fuel low on energy" {
		t.Errorf("line %q", got)
	}
	f := Words(store.CheckIn{Effort: "easy", Pain: "none"})
	if f.Effort != "easy" || f.Pain != "nothing hurting" || Line(store.CheckIn{Effort: "easy", Pain: "none"}) != "easy" {
		t.Errorf("words %+v", f)
	}
	if Words(store.CheckIn{Skipped: true}) != nil {
		t.Error("a skip has words")
	}
}
