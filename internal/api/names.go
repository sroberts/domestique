package api

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"domestique/internal/coach"
	"domestique/internal/store"
	"domestique/internal/strength"
	"domestique/internal/sweep"
)

// Names: the athlete renames a workout in Domestique only (never on Strava),
// and the LLM suggests a name for one still called "Afternoon Ride".

func nameDismissedKey(id string) string { return "ai:name-dismissed:" + id }

// putActivityName renames an activity. A name chosen ends any suggestion.
func (a *API) putActivityName(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	name := strings.Join(strings.Fields(body.Name), " ")
	if name == "" || utf8.RuneCountInString(name) > 100 {
		writeErr(w, http.StatusBadRequest, "name is 1 to 100 characters")
		return
	}
	if _, err := a.Store.GetActivity(ctx, id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such activity")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Ingest.Rename(ctx, id, name); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Store.SetSyncState(ctx, nameDismissedKey(id), "renamed"); err != nil {
		a.fail(w, r, err)
		return
	}
	act, err := a.Store.GetActivity(ctx, id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, act)
}

// dismissNameSuggestion hides an activity's suggested name for good.
func (a *API) dismissNameSuggestion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if _, err := a.Store.GetActivity(ctx, id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such activity")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Store.SetSyncState(ctx, nameDismissedKey(id), "dismissed"); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// aiName adds a suggested name to an activity still called by its default,
// when its route or the athlete's notes give something to build one from.
func (a *API) aiName(r *http.Request, resp *detailResp) {
	act := resp.Activity
	if !coach.GenericName(act.Name) {
		return
	}
	ctx := r.Context()
	if v, err := a.Store.SyncState(ctx, nameDismissedKey(act.ID)); err == nil && v != "" {
		return
	}
	f := coach.NameFacts{Name: act.Name, Sport: act.Sport, Route: act.Route}
	for _, src := range resp.Sources {
		if d := sweep.Description(src); d != "" {
			f.Notes = d
			break
		}
	}
	if f.Route == "" && f.Notes == "" {
		return // nothing specific to name it from
	}
	t := time.UnixMilli(act.StartAt).In(a.loc())
	f.When = t.Format("Monday") + " " + partOfDay(t)
	secs := act.MovingS
	if secs <= 0 {
		secs = act.ElapsedS
	}
	f.Minutes = int(math.Round(float64(secs) / 60))
	if act.DistanceM > 0 {
		f.KM = r1p(act.DistanceM / 1000)
	}
	if resp.Metrics.AscentM >= 10 {
		f.AscentM = ip(resp.Metrics.AscentM)
	}
	n, ok, pending := a.Coach.NameSuggestion(ctx, act.ID, f)
	if ok && n.Text != "" {
		resp.NameSuggestion = &strength.AINote{Text: n.Text}
	} else if pending {
		resp.NameSuggestion = &strength.AINote{Pending: true}
	}
}

func partOfDay(t time.Time) string {
	switch h := t.Hour(); {
	case h >= 5 && h < 12:
		return "morning"
	case h >= 12 && h < 17:
		return "afternoon"
	case h >= 17 && h < 21:
		return "evening"
	}
	return "night"
}
