package coach_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
)

var kinds = []string{"chain", "tyres", "cassette", "brake_pads", "other"}

// Only fittings the note plainly states, in its own words, survive.
func TestGearEvents(t *testing.T) {
	f := &coachtest.Fake{Chat: func(system, user string) string {
		if !strings.Contains(system, "chain, tyres") {
			t.Errorf("kinds missing from prompt: %q", system)
		}
		return `{"events": [
			{"part": "chain", "action": "replaced", "bike": "the Tarmac", "quote": "New chain on the Tarmac"},
			{"part": "tyres", "action": "installed", "name": "GP5000s", "bike": "the Crux", "quote": "fitted GP5000s"},
			{"part": "cassette", "action": "serviced", "quote": "cleaned the cassette"},
			{"part": "cassette", "action": "replaced", "quote": "a new cassette last week"},
			{"part": "wheels", "action": "replaced", "quote": "New chain on the Tarmac"},
			{"part": "other", "action": "installed", "quote": "fitted GP5000s"}
		]}`
	}}
	w := &coach.Writer{AI: f.AI(t), KV: &mapKV{}, Log: slog.New(slog.DiscardHandler)}
	d := "New chain on the Tarmac, and fitted GP5000s. Cleaned the cassette too."
	g, err := w.GearEvents(context.Background(), d, kinds)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Events) != 2 {
		t.Fatalf("events = %+v", g.Events)
	}
	if e := g.Events[0]; e.Part != "chain" || e.Bike != "the Tarmac" {
		t.Errorf("chain: %+v", e)
	}
	// The note doesn't name the Crux: no bike, rather than a guess.
	if e := g.Events[1]; e.Part != "tyres" || e.Name != "GP5000s" || e.Bike != "" {
		t.Errorf("tyres: %+v", e)
	}
}

func TestGenericName(t *testing.T) {
	for name, want := range map[string]bool{
		"Afternoon Ride": true, "Morning Activity": true, "Zwift": true, "Evening  Walk": true, "Workout": true,
		"Lunch Run": true, "Indoor Cycling": true, "Night Indoor Ride": true,
		"Coast Road with the Club": false, "Zwift - Group Ride: Tempo Tuesday": false, "Lower A": false, "Box Hill repeats": false,
	} {
		if got := coach.GenericName(name); got != want {
			t.Errorf("GenericName(%q) = %v", name, got)
		}
	}
	if !coach.MentionsGear("new tyres today") || coach.MentionsGear("legs dead, soggy") {
		t.Error("MentionsGear")
	}
}

func TestNameSuggestion(t *testing.T) {
	reply := ""
	f := &coachtest.Fake{Chat: func(string, string) string { return reply }}
	w := &coach.Writer{AI: f.AI(t), KV: &mapKV{}, Log: slog.New(slog.DiscardHandler)}
	ctx := context.Background()
	km := 42.3
	facts := coach.NameFacts{Name: "Afternoon Ride", Sport: "ride", Route: "Coast Road", When: "Saturday afternoon", Minutes: 95, KM: &km,
		Notes: "with Jo and Sam, windy"}
	name := func(id string) (coach.Note, bool) {
		w.NameSuggestion(ctx, id, facts)
		var n coach.Note
		var ok bool
		waitFor(t, func() bool {
			var pending bool
			n, ok, pending = w.NameSuggestion(ctx, id, facts)
			return !pending
		})
		return n, ok
	}
	reply = `{"name": "\"Coast Road with Jo and Sam\""}`
	if n, ok := name("a1"); !ok || n.Text != "Coast Road with Jo and Sam" {
		t.Fatalf("a1 = %+v %v", n, ok)
	}
	// A generic reply is kept as nothing to suggest, and not asked again.
	reply = `{"name": "Afternoon Ride"}`
	if n, ok := name("a2"); !ok || n.Text != "" {
		t.Fatalf("a2 = %+v %v", n, ok)
	}
	for id, bad := range map[string]string{
		"a3": `{"name": "Coast Road 60 km"}`,        // invented number
		"a4": `{"name": "Windy Coast Road 🌬️"}`,     // emoji
		"a5": `{"name": "Smashed the Coast Road!"}`, // voice
	} {
		reply = bad
		if n, ok := name(id); ok {
			t.Errorf("%s accepted: %+v", id, n)
		}
	}
}

func TestRecurring(t *testing.T) {
	end := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return end.AddDate(0, 0, -n).Add(9 * time.Hour) }
	ms := []coach.Mention{
		{At: day(2), Text: "left knee sore on the climbs"},
		{At: day(2), Text: "knee again"}, // same day: counts once
		{At: day(6), Text: "Knees ached"},
		{At: day(13), Text: "knee pain"},
		{At: day(30), Text: "knee"}, // before the window
		{At: day(3), Text: "back to back hills, back home late"},
		{At: day(4), Text: "sore back"},
		{At: day(5), Tags: []string{"ill"}},
		{At: day(9), Tags: []string{"ill"}},
		{At: day(1), Text: "lower back tight"},
	}
	got := coach.Recurring(ms, end)
	if len(got) != 1 || got[0].String() != "knee, 3 times in 3 weeks" {
		t.Fatalf("got %v", got)
	}
	ms = append(ms, coach.Mention{At: day(12), Tags: []string{"ill"}}, coach.Mention{At: day(15), Text: "back pain"})
	got = coach.Recurring(ms, end)
	var s []string
	for _, c := range got {
		s = append(s, c.String())
	}
	if strings.Join(s, "; ") != "feeling ill, 3 times in 3 weeks; knee, 3 times in 3 weeks; lower back, 3 times in 3 weeks" {
		t.Fatalf("got %v", s)
	}
}
