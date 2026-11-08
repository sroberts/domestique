package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"domestique/internal/activity"
	"domestique/internal/coach"
	"domestique/internal/gear"
	"domestique/internal/store"
	"domestique/internal/sweep"
)

// Gear suggestions: parts the athlete wrote about fitting ("new chain
// today"), read by the LLM (internal/sweep), offered on the Gear page.
// Accepting one fits the part as the Gear page would, dated to the workout;
// nothing changes until then.

type gearSuggestion struct {
	ID           string `json:"id"`
	ActivityID   string `json:"activity_id"`
	ActivityName string `json:"activity_name"`
	At           int64  `json:"at"`
	BikeID       string `json:"bike_id"`
	BikeName     string `json:"bike_name"`
	Kind         string `json:"kind"`
	Name         string `json:"name"` // the part's name for the new component
	Quote        string `json:"quote"`
	// Replaces is the part fitted at the time, which accepting retires.
	Replaces     string `json:"replaces,omitempty"`
	ReplacesName string `json:"replaces_name,omitempty"`
	Text         string `json:"text"` // "New chain on the Tarmac, 3 Nov"
}

type gearSuggestionsResp struct {
	AI          bool             `json:"ai"`
	Suggestions []gearSuggestion `json:"suggestions"`
}

func gearDoneKey(id string) string { return "ai:gear-done:" + id }

// sameDay is how close a part already fitted must be to count as this one.
const sameDay = 3 * 24 * time.Hour

// maxGearSuggestions is how many are offered at once, newest first.
const maxGearSuggestions = 10

// defaultLifeM are typical replacement distances, as the Gear page offers.
var defaultLifeM = map[string]float64{"chain": 3e6, "tyres": 4e6, "cassette": 1e7, "brake_pads": 2e6}

func (a *API) getGearSuggestions(w http.ResponseWriter, r *http.Request) {
	resp := gearSuggestionsResp{AI: a.aiOn(r), Suggestions: []gearSuggestion{}}
	if resp.AI {
		var err error
		if resp.Suggestions, err = a.gearSuggestions(r.Context()); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// gearSuggestions lists the open suggestions: not accepted or dismissed, on a
// bike that can be told, and not already logged or since superseded.
func (a *API) gearSuggestions(ctx context.Context) ([]gearSuggestion, error) {
	bikes, err := a.Store.ListBikes(ctx)
	if err != nil || len(bikes) == 0 {
		return []gearSuggestion{}, err
	}
	comps, err := a.Store.ListComponents(ctx)
	if err != nil {
		return nil, err
	}
	over, err := a.Store.BikeOverrides(ctx)
	if err != nil {
		return nil, err
	}
	done, err := a.Store.SyncStatePrefix(ctx, "ai:gear-done:")
	if err != nil {
		return nil, err
	}
	srcs, err := a.Store.AllSources(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]store.Bike{}
	var active []store.Bike
	for _, b := range bikes {
		byID[b.ID] = b
		if !b.Retired {
			active = append(active, b)
		}
	}
	acts := map[string]*store.Activity{}
	out := []gearSuggestion{}
	for _, src := range srcs {
		evs := sweep.SourceGear(ctx, a.Coach, src)
		if len(evs) == 0 {
			continue
		}
		act, ok := acts[src.ActivityID]
		if !ok {
			x, err := a.Store.GetActivity(ctx, src.ActivityID)
			if errors.Is(err, store.ErrNotFound) {
				acts[src.ActivityID] = nil
				continue
			} else if err != nil {
				return nil, err
			}
			act = &x
			acts[src.ActivityID] = act
		}
		if act == nil {
			continue
		}
		for i, e := range evs {
			id := fmt.Sprintf("%s.%d", src.ID, i)
			if done[id] != "" {
				continue
			}
			bike := a.suggestedBike(e, *act, src, bikes, active, over)
			b, ok := byID[bike]
			if !ok || b.Retired {
				continue
			}
			s := gearSuggestion{ID: id, ActivityID: act.ID, ActivityName: act.Name, At: act.StartAt, BikeID: b.ID,
				BikeName: b.Name, Kind: e.Part, Name: e.Name, Quote: e.Quote}
			if s.Name == "" {
				s.Name = gear.KindLabel(e.Part)
			}
			if !fitSuggestion(&s, comps) {
				continue
			}
			label := strings.ToLower(gear.KindLabel(e.Part))
			if e.Part == "other" {
				label = s.Name
			}
			s.Text = fmt.Sprintf("New %s on the %s, %s", label, b.Name, time.UnixMilli(act.StartAt).In(a.loc()).Format("2 Jan"))
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > maxGearSuggestions {
		out = out[:maxGearSuggestions]
	}
	return out, nil
}

// suggestedBike is the bike a part went on: the one the note names, else the
// one the workout was ridden on, else the only bike there is.
func (a *API) suggestedBike(e coach.GearEvent, act store.Activity, src store.Source, bikes, active []store.Bike, over map[string]*string) string {
	if e.Bike != "" {
		if id, ok := gear.MatchName(e.Bike, bikes); ok {
			return id
		}
		// "the Tarmac" for "Tarmac SL7": a word of four letters or more in
		// common, with exactly one bike.
		said := map[string]bool{}
		for _, w := range strings.Fields(strings.ToLower(e.Bike)) {
			said[w] = true
		}
		found := ""
		for _, b := range bikes {
			for _, w := range strings.Fields(strings.ToLower(b.Name)) {
				if len(w) >= 4 && said[w] {
					if found != "" && found != b.ID {
						return ""
					}
					found = b.ID
				}
			}
		}
		return found
	}
	if activity.IsRide(act.Sport) {
		ride := store.GearActivity{ID: act.ID, Sport: act.Sport, StartAt: act.StartAt, StravaGear: extraString(src.Extra, "gear")}
		if as := gear.Assign(ride, bikes, over); as.BikeID != "" {
			return as.BikeID
		}
	}
	if len(active) == 1 {
		return active[0].ID
	}
	return ""
}

// fitSuggestion finds the part a suggestion replaces, and reports false when
// it's already logged (a part of the kind fitted within a few days) or
// superseded (one fitted since).
func fitSuggestion(s *gearSuggestion, comps []store.Component) bool {
	for _, c := range comps {
		if c.BikeID != s.BikeID || c.Kind != s.Kind || (s.Kind == "other" && !strings.EqualFold(c.Name, s.Name)) {
			continue
		}
		if c.InstalledAt > s.At-sameDay.Milliseconds() {
			return false
		}
		if c.RemovedAt == nil || *c.RemovedAt > s.At {
			s.Replaces, s.ReplacesName = c.ID, c.Name
		}
	}
	return true
}

func (a *API) findGearSuggestion(w http.ResponseWriter, r *http.Request) (gearSuggestion, bool) {
	if !a.aiOn(r) {
		writeErr(w, http.StatusNotFound, "no such suggestion")
		return gearSuggestion{}, false
	}
	all, err := a.gearSuggestions(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return gearSuggestion{}, false
	}
	for _, s := range all {
		if s.ID == r.PathValue("id") {
			return s, true
		}
	}
	writeErr(w, http.StatusNotFound, "no such suggestion")
	return gearSuggestion{}, false
}

// acceptGearSuggestion fits the part, replacing the one fitted then.
func (a *API) acceptGearSuggestion(w http.ResponseWriter, r *http.Request) {
	s, ok := a.findGearSuggestion(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	c := store.Component{ID: "c_" + hex.EncodeToString(b), BikeID: s.BikeID, Kind: s.Kind, Name: s.Name,
		InstalledAt: s.At, ReplaceAtM: defaultLifeM[s.Kind]}
	if s.Replaces != "" {
		if old, err := a.Store.GetComponent(ctx, s.Replaces); err == nil && old.ReplaceAtM > 0 {
			c.ReplaceAtM = old.ReplaceAtM
		}
	}
	if err := a.Store.PutComponent(ctx, c, s.Replaces); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Store.SetSyncState(ctx, gearDoneKey(s.ID), "accepted"); err != nil {
		a.fail(w, r, err)
		return
	}
	c, _ = a.Store.GetComponent(ctx, c.ID)
	writeJSON(w, http.StatusOK, c)
}

func (a *API) dismissGearSuggestion(w http.ResponseWriter, r *http.Request) {
	s, ok := a.findGearSuggestion(w, r)
	if !ok {
		return
	}
	if err := a.Store.SetSyncState(r.Context(), gearDoneKey(s.ID), "dismissed"); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
