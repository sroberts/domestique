package api

import (
	"encoding/json"
	"testing"
	"time"
)

type gearResp struct {
	Bikes []struct {
		ID         string  `json:"id"`
		DistanceM  float64 `json:"distance_m"`
		Rides      int     `json:"rides"`
		Status     string  `json:"status"`
		Components []struct {
			ID        string   `json:"id"`
			DistanceM float64  `json:"distance_m"`
			Wear      *float64 `json:"wear"`
			Status    string   `json:"status"`
		} `json:"components"`
		History []struct {
			ID string `json:"id"`
		} `json:"history"`
	} `json:"bikes"`
	Sensors []struct {
		ID     string `json:"id"`
		UsedS  int    `json:"used_s"`
		Status string `json:"status"`
	} `json:"sensors"`
	Reminders []struct {
		Text string `json:"text"`
	} `json:"reminders"`
	UnmatchedGear []struct {
		Name  string `json:"name"`
		Rides int    `json:"rides"`
	} `json:"unmatched_gear"`
	Rides int `json:"rides"`
}

func (h *harness) gear() gearResp {
	h.t.Helper()
	w := h.do("GET", "/v1/gear", nil)
	if w.Code != 200 {
		h.t.Fatalf("gear: %d %s", w.Code, w.Body)
	}
	var g gearResp
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		h.t.Fatal(err)
	}
	return g
}

func TestGearEndToEnd(t *testing.T) {
	h := newHarness(t)
	rideID := h.rideID()
	rideDist := mustActivityDistance(t, h, rideID)

	if g := h.gear(); len(g.Bikes) != 0 || g.Rides != 2 {
		t.Fatalf("empty: %+v", g)
	}
	// Strava's gear name, matched by alias, assigns the ride. The hire-bike
	// ride matches nothing and is not credited to the default bike.
	if w := h.do("PUT", "/v1/gear/bikes/tarmac", map[string]any{"name": "Tarmac", "aliases": []string{"specialized tarmac"},
		"default_ride": true, "start_distance_m": 1000}); w.Code != 200 {
		t.Fatalf("put bike: %d %s", w.Code, w.Body)
	}
	installed := time.Date(2013, 8, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if w := h.do("PUT", "/v1/gear/components/chain1", map[string]any{"bike_id": "tarmac", "kind": "chain",
		"installed_at": installed, "replace_at_m": rideDist * 1.05}); w.Code != 200 {
		t.Fatalf("put chain: %d %s", w.Code, w.Body)
	}
	g := h.gear()
	b := g.Bikes[0]
	if b.Rides != 1 || b.DistanceM != round(1000+rideDist) || len(g.UnmatchedGear) != 1 || g.UnmatchedGear[0].Name != "Hire bike" {
		t.Fatalf("assigned: %+v unmatched %+v", b, g.UnmatchedGear)
	}
	if c := b.Components[0]; c.Status != "soon" || c.DistanceM != round(rideDist) || len(g.Reminders) != 1 {
		t.Fatalf("chain: %+v reminders %+v", c, g.Reminders)
	}
	w := h.do("GET", "/v1/activities/"+rideID+"/bike", nil)
	var ab activityBikeResp
	json.Unmarshal(w.Body.Bytes(), &ab)
	if ab.How != "strava" || ab.Bike == nil || ab.Bike.ID != "tarmac" || ab.StravaGear != "Specialized  Tarmac" {
		t.Fatalf("activity bike: %+v", ab)
	}

	// Manual "no bike" overrides Strava; totals recompute.
	w = h.do("PUT", "/v1/activities/"+rideID+"/bike", map[string]any{"bike_id": nil})
	json.Unmarshal(w.Body.Bytes(), &ab)
	if w.Code != 200 || ab.How != "manual" || ab.Bike != nil {
		t.Fatalf("override: %d %+v", w.Code, ab)
	}
	if g := h.gear(); g.Bikes[0].DistanceM != 1000 || len(g.Reminders) != 0 {
		t.Fatalf("after override: %+v", g.Bikes[0])
	}
	w = h.do("DELETE", "/v1/activities/"+rideID+"/bike", nil)
	json.Unmarshal(w.Body.Bytes(), &ab)
	if ab.How != "strava" {
		t.Fatalf("back to automatic: %+v", ab)
	}
	if w := h.do("PUT", "/v1/activities/"+rideID+"/bike", map[string]any{"bike_id": "nope"}); w.Code != 400 {
		t.Fatalf("unknown bike: %d", w.Code)
	}

	// Replacing the chain retires the old one with its history.
	swap := time.Date(2013, 8, 17, 0, 0, 0, 0, time.UTC).UnixMilli()
	if w := h.do("PUT", "/v1/gear/components/chain2", map[string]any{"bike_id": "tarmac", "kind": "chain",
		"installed_at": swap, "replace_at_m": 3_000_000, "replaces": "chain1"}); w.Code != 200 {
		t.Fatalf("replace: %d %s", w.Code, w.Body)
	}
	g = h.gear()
	b = g.Bikes[0]
	if len(b.Components) != 1 || b.Components[0].ID != "chain2" || b.Components[0].DistanceM != 0 ||
		len(b.History) != 1 || b.History[0].ID != "chain1" || len(g.Reminders) != 0 {
		t.Fatalf("after replace: %+v", b)
	}
	// Undo the replacement: the old chain is fitted again.
	h.do("DELETE", "/v1/gear/components/chain2", nil)
	if b := h.gear().Bikes[0]; len(b.Components) != 1 || b.Components[0].ID != "chain1" {
		t.Fatalf("after undo: %+v", b)
	}

	// A chest strap: the ride's head-unit heart rate counts, the lift's
	// borrowed heart rate doesn't.
	if w := h.do("PUT", "/v1/gear/sensors/strap", map[string]any{"name": "Chest strap", "battery_at": installed,
		"battery_life_h": 1}); w.Code != 200 {
		t.Fatalf("put sensor: %d %s", w.Code, w.Body)
	}
	g = h.gear()
	if s := g.Sensors[0]; s.UsedS < 3000 || s.Status != "due" {
		t.Fatalf("sensor: %+v", s)
	}

	for _, bad := range []map[string]any{
		{"name": ""},
		{"name": "x", "start_distance_m": -1},
	} {
		if w := h.do("PUT", "/v1/gear/bikes/b2", bad); w.Code != 400 {
			t.Errorf("bad bike %v: %d", bad, w.Code)
		}
	}
	if w := h.do("PUT", "/v1/gear/components/c9", map[string]any{"bike_id": "tarmac", "kind": "spokes", "installed_at": installed}); w.Code != 400 {
		t.Errorf("bad kind: %d", w.Code)
	}
	if w := h.do("PUT", "/v1/gear/components/c9", map[string]any{"bike_id": "ghost", "kind": "chain", "installed_at": installed}); w.Code != 400 {
		t.Errorf("unknown bike: %d", w.Code)
	}

	// Deleting the bike leaves rides unassigned again.
	h.do("DELETE", "/v1/gear/bikes/tarmac", nil)
	if g := h.gear(); len(g.Bikes) != 0 {
		t.Fatalf("after delete: %+v", g.Bikes)
	}
}

func round(v float64) float64 { return float64(int64(v + 0.5)) }

func mustActivityDistance(t *testing.T, h *harness, id string) float64 {
	a, err := h.st.GetActivity(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if a.DistanceM < 1000 {
		t.Fatalf("fixture ride distance %v", a.DistanceM)
	}
	return a.DistanceM
}
