package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"domestique/internal/coach"
	"domestique/internal/coach/coachtest"
	"domestique/internal/gear"
	"domestique/internal/ingest"
	"domestique/internal/store"
	"domestique/internal/sweep"
)

// putDescribed stores an activity with one Strava source carrying a
// description (and route), as the sweep would read it.
func putDescribed(t *testing.T, st *store.Store, id, sport, name string, at time.Time, extra string) store.Source {
	t.Helper()
	ctx := context.Background()
	if err := st.PutActivity(ctx, store.Activity{ID: id, Sport: sport, Name: name, StartAt: at.UnixMilli(), ElapsedS: 3600,
		MovingS: 3600, DistanceM: 30000, Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	src := store.Source{ID: "strava:" + id, ActivityID: id, Source: "strava", Sport: sport, Name: name, StartAt: at.UnixMilli(),
		ElapsedS: 3600, MovingS: 3600, DistanceM: 30000, Extra: []byte(extra), ImportedAt: at.UnixMilli()}
	if err := st.PutSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestGearSuggestions(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	if w := call(t, h, "GET", "/v1/gear/suggestions", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ai":false`) {
		t.Fatalf("no model: %d %s", w.Code, w.Body)
	}
	a.Coach = &coach.Writer{AI: (&coachtest.Fake{}).AI(t), KV: st, Log: a.Log}

	day := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	for _, b := range []store.Bike{{ID: "b_tarmac", Name: "Tarmac SL7", DefaultRide: true}, {ID: "b_crux", Name: "Crux"}} {
		if err := st.PutBike(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	old := store.Component{ID: "c_old", BikeID: "b_tarmac", Kind: "chain", Name: "Chain", InstalledAt: day.AddDate(0, -3, 0).UnixMilli(), ReplaceAtM: 2.5e6}
	if err := st.PutComponent(ctx, old, ""); err != nil {
		t.Fatal(err)
	}
	d := "New chain on the Tarmac today, and new tyres. Legs fine."
	src := putDescribed(t, st, "r1", "ride", "Morning Ride", day, `{"description":"`+d+`"}`)
	// What the sweep read from it (internal/sweep).
	read := coach.GearRead{Events: []coach.GearEvent{
		{Part: "chain", Action: "replaced", Bike: "the Tarmac", Quote: "New chain on the Tarmac today"},
		{Part: "tyres", Action: "replaced", Quote: "new tyres"},
	}}
	if err := a.Coach.Keep(ctx, sweep.GearKey(src.ID), coach.Fingerprint(coach.GearPrompt(gear.Kinds), d), read); err != nil {
		t.Fatal(err)
	}

	list := func() []gearSuggestion {
		var r gearSuggestionsResp
		w := call(t, h, "GET", "/v1/gear/suggestions", "")
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != 200 {
			t.Fatalf("list: %d %s", w.Code, w.Body)
		}
		return r.Suggestions
	}
	ss := list()
	if len(ss) != 2 {
		t.Fatalf("suggestions = %+v", ss)
	}
	chain, tyres := ss[0], ss[1]
	if chain.Kind != "chain" {
		chain, tyres = tyres, chain
	}
	if chain.BikeID != "b_tarmac" || chain.Replaces != "c_old" || chain.Text != "New chain on the Tarmac SL7, 20 Oct" {
		t.Fatalf("chain: %+v", chain)
	}
	// No bike named: the one the ride was on (the default).
	if tyres.BikeID != "b_tarmac" || tyres.Replaces != "" {
		t.Fatalf("tyres: %+v", tyres)
	}

	if w := call(t, h, "POST", "/v1/gear/suggestions/"+chain.ID+"/accept", ""); w.Code != 200 {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	comps, _ := st.ListComponents(ctx)
	var fitted *store.Component
	for i, c := range comps {
		if c.ID == "c_old" && (c.RemovedAt == nil || *c.RemovedAt != day.UnixMilli()) {
			t.Fatalf("old chain not retired: %+v", c)
		}
		if c.ID != "c_old" && c.Kind == "chain" {
			fitted = &comps[i]
		}
	}
	if fitted == nil || fitted.InstalledAt != day.UnixMilli() || fitted.ReplaceAtM != 2.5e6 || fitted.BikeID != "b_tarmac" {
		t.Fatalf("new chain: %+v", fitted)
	}
	if w := call(t, h, "POST", "/v1/gear/suggestions/"+tyres.ID+"/dismiss", ""); w.Code != 204 {
		t.Fatalf("dismiss: %d %s", w.Code, w.Body)
	}
	if ss := list(); len(ss) != 0 {
		t.Fatalf("after accept and dismiss: %+v", ss)
	}
	if w := call(t, h, "POST", "/v1/gear/suggestions/"+chain.ID+"/accept", ""); w.Code != 404 {
		t.Fatalf("accept twice: %d", w.Code)
	}
}

// A part already logged by hand near the date isn't suggested again.
func TestGearSuggestionAlreadyLogged(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	a.Coach = &coach.Writer{AI: (&coachtest.Fake{}).AI(t), KV: st, Log: a.Log}
	day := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	_ = st.PutBike(ctx, store.Bike{ID: "b1", Name: "Tarmac", DefaultRide: true})
	_ = st.PutComponent(ctx, store.Component{ID: "c1", BikeID: "b1", Kind: "chain", Name: "Chain", InstalledAt: day.Add(-20 * time.Hour).UnixMilli()}, "")
	d := "new chain"
	src := putDescribed(t, st, "r1", "ride", "Morning Ride", day, `{"description":"new chain"}`)
	_ = a.Coach.Keep(ctx, sweep.GearKey(src.ID), coach.Fingerprint(coach.GearPrompt(gear.Kinds), d),
		coach.GearRead{Events: []coach.GearEvent{{Part: "chain", Action: "replaced", Quote: "new chain"}}})
	if w := call(t, h, "GET", "/v1/gear/suggestions", ""); !strings.Contains(w.Body.String(), `"suggestions":[]`) {
		t.Fatalf("already logged: %s", w.Body)
	}
}

func TestRenameAndNameSuggestion(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	a.Ingest = &ingest.Ingester{Store: st, Log: a.Log, Loc: time.UTC}
	var names atomic.Int32
	f := &coachtest.Fake{Chat: func(system, _ string) string {
		if !strings.Contains(system, "You name one finished workout") {
			return `{"note": "Easy spin."}` // the activity note
		}
		names.Add(1)
		return `{"name": "Tempus Fugit with Jo"}`
	}}
	day := time.Date(2026, 10, 20, 17, 0, 0, 0, time.UTC)
	putDescribed(t, st, "r1", "virtual_ride", "Zwift", day,
		`{"description":"🗺️ Tempus Fugit in Watopia\neasy spin with Jo","route":"Tempus Fugit"}`)
	if err := a.Ingest.Rebuild(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	detail := func(id string) detailResp {
		var d detailResp
		w := call(t, h, "GET", "/v1/activities/"+id, "")
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
			t.Fatalf("detail: %d %s", w.Code, w.Body)
		}
		return d
	}
	if d := detail("r1"); d.NameSuggestion != nil {
		t.Fatalf("no model, yet a suggestion: %+v", d.NameSuggestion)
	}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: st, Log: a.Log}
	if d := detail("r1"); d.NameSuggestion == nil || !d.NameSuggestion.Pending {
		t.Fatalf("first: %+v", d.NameSuggestion)
	}
	var d detailResp
	for range 200 {
		if d = detail("r1"); d.NameSuggestion != nil && d.NameSuggestion.Text != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if d.NameSuggestion == nil || d.NameSuggestion.Text != "Tempus Fugit with Jo" {
		t.Fatalf("suggestion: %+v", d.NameSuggestion)
	}

	if w := call(t, h, "PUT", "/v1/activities/r1/name", `{"name":"  Tempus Fugit   with Jo "}`); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if d := detail("r1"); d.Activity.Name != "Tempus Fugit with Jo" || d.NameSuggestion != nil {
		t.Fatalf("after rename: %q %+v", d.Activity.Name, d.NameSuggestion)
	}
	// A rebuild (a merge, a new recording) keeps the athlete's name.
	if err := a.Ingest.Rebuild(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if act, _ := st.GetActivity(ctx, "r1"); act.Name != "Tempus Fugit with Jo" {
		t.Fatalf("after rebuild: %q", act.Name)
	}
	if w := call(t, h, "PUT", "/v1/activities/r1/name", `{"name":"  "}`); w.Code != 400 {
		t.Fatalf("empty name: %d", w.Code)
	}

	// Dismissed: gone for good. With nothing to build from: never asked.
	putDescribed(t, st, "r2", "ride", "Afternoon Ride", day.AddDate(0, 0, 1), `{"description":"coffee at the harbour"}`)
	putDescribed(t, st, "r3", "ride", "Evening Ride", day.AddDate(0, 0, 2), `{}`)
	if w := call(t, h, "POST", "/v1/activities/r2/name-suggestion/dismiss", ""); w.Code != 204 {
		t.Fatalf("dismiss: %d", w.Code)
	}
	n := names.Load()
	if d2, d3 := detail("r2"), detail("r3"); d2.NameSuggestion != nil || d3.NameSuggestion != nil {
		t.Fatalf("r2 %+v, r3 %+v", d2.NameSuggestion, d3.NameSuggestion)
	}
	time.Sleep(20 * time.Millisecond)
	if names.Load() != n {
		t.Fatal("asked about a dismissed or empty activity")
	}
}

func TestWeekNoteKeepsComingUp(t *testing.T) {
	a, st, h := testAPI(t)
	ctx := context.Background()
	var mu sync.Mutex
	var facts string
	f := &coachtest.Fake{Chat: func(_, user string) string {
		mu.Lock()
		defer mu.Unlock()
		facts = user
		return `{"note":"A steady week. Your knee keeps coming up: worth watching."}`
	}}
	a.Coach = &coach.Writer{AI: f.AI(t), KV: st, Log: a.Log}
	now := time.Now().UTC()
	mon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	mon = mon.AddDate(0, 0, -((int(mon.Weekday())+6)%7)-7)
	for i, d := range []string{"knee sore on the climb", "knee again", "slow, knee"} {
		at := mon.AddDate(0, 0, -2*i).Add(30 * time.Hour)
		src := putDescribed(t, st, "r"+string(rune('1'+i)), "ride", "Ride", at, `{"description":"`+d+`"}`)
		_ = a.Coach.Keep(ctx, sweep.TagsKey(src.ID), coach.Fingerprint(coach.TagsPrompt(), d), coach.Tags{Tags: []string{"sore"}, Note: "knee pain"})
	}
	var r weekNoteResp
	_ = json.Unmarshal(call(t, h, "GET", "/v1/week-note", "").Body.Bytes(), &r)
	if len(r.KeepsComingUp) != 1 || r.KeepsComingUp[0] != "knee, 3 times in 3 weeks" {
		t.Fatalf("keeps coming up: %+v", r)
	}
	sent := func() string {
		mu.Lock()
		defer mu.Unlock()
		return facts
	}
	waitFor := func() {
		for range 200 {
			if sent() != "" {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor()
	if s := sent(); !strings.Contains(s, `"keeps_coming_up"`) || !strings.Contains(s, "knee, 3 times in 3 weeks") {
		t.Fatalf("facts: %s", s)
	}
}
