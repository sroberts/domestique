package api

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"domestique/internal/load"
	"domestique/internal/store"
	"domestique/internal/strength"
)

func (a *API) registerTemplates(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/templates", a.listTemplates)
	mux.HandleFunc("GET /v1/templates/{id}", a.getTemplate)
	mux.HandleFunc("PUT /v1/templates/{id}", a.putTemplate)
	mux.HandleFunc("DELETE /v1/templates/{id}", a.deleteTemplate)
	mux.HandleFunc("GET /v1/templates/{id}/prescribed", a.prescribed)
	mux.HandleFunc("GET /v1/exercises", a.exercises)
	mux.HandleFunc("GET /v1/plans/{date}", a.getPlan)
	mux.HandleFunc("PUT /v1/plans/{date}", a.putPlan)
	mux.HandleFunc("DELETE /v1/plans/{date}", a.deletePlan)
}

// dateParam is the athlete's local date from ?date= (the device knows it;
// the Mini's zone may differ), defaulting to today where the Mini is.
func (a *API) dateParam(r *http.Request) (string, bool) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().In(a.loc()).Format("2006-01-02")
	}
	return date, validDate(date)
}

// dayReadiness is the recovery, recent load and readiness call for a date:
// what both Today and any prescription are judged against.
func (a *API) dayReadiness(r *http.Request, date string) (*store.Recovery, []load.Day, strength.Readiness, error) {
	ctx := r.Context()
	if a.demoOn(r) {
		if err := a.Demo.EnsureDay(ctx, date); err != nil {
			return nil, nil, strength.Readiness{}, err
		}
	}
	var rec *store.Recovery
	if x, err := a.Store.GetRecovery(ctx, date); err == nil {
		rec = &x
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, nil, strength.Readiness{}, err
	}
	day, _ := time.ParseInLocation("2006-01-02", date, a.loc())
	series, err := load.Daily(ctx, a.Store, day.AddDate(0, 0, -13), day, a.loc())
	if err != nil {
		return nil, nil, strength.Readiness{}, err
	}
	tsb, hasLoad := 0.0, false
	if n := len(series); n > 0 {
		tsb = series[n-1].TSB
		hasLoad = series[n-1].CTL > 5
	}
	return rec, series, strength.AssessReadiness(rec, tsb, hasLoad), nil
}

// templateRow is a template with when it was last done, for the list.
type templateRow struct {
	store.Template
	LastDoneAt *int64 `json:"last_done_at"`
}

func (a *API) listTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := a.Store.ListTemplates(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	last, err := a.Store.LastDone(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]templateRow, len(ts))
	for i, t := range ts {
		out[i] = templateRow{Template: t}
		if at, ok := last[t.ID]; ok {
			out[i].LastDoneAt = &at
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) getTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := a.Store.GetTemplate(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such template")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *API) putTemplate(w http.ResponseWriter, r *http.Request) {
	var t store.Template
	if !readJSON(w, r, &t) {
		return
	}
	t.ID = r.PathValue("id")
	t, err := store.CleanTemplate(t)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t.UpdatedAt = time.Now().UnixMilli()
	if err := a.Store.PutTemplate(r.Context(), t); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// deleteTemplate removes a template. Sessions keep their own copy of the
// plan, so history is untouched. A template planned for today (the device's
// ?date=) or later is refused with 409 and the dates, unless clear_plans=1
// says the athlete agreed to clear those plans too.
func (a *API) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	date, ok := a.dateParam(r)
	if !idRe.MatchString(id) || !ok {
		writeErr(w, http.StatusBadRequest, "bad template id or date")
		return
	}
	ctx := r.Context()
	if _, err := a.Store.GetTemplate(ctx, id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such template")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if q := r.URL.Query().Get("clear_plans"); q != "1" && q != "true" {
		dates, err := a.Store.PlannedDates(ctx, id, date)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if len(dates) > 0 {
			msg := "this workout is planned for " + strings.Join(dates, ", ") + "; delete with clear_plans=1 to clear those plans too"
			if dates[0] == date {
				msg = "this is today's plan; delete with clear_plans=1 to clear it too"
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "plans": dates})
			return
		}
	}
	if err := a.Store.DeleteTemplate(ctx, id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such template")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// prescribed is a template turned into the targets for a date, exactly as
// Today prescribes the planned one: progression from each lift's history,
// held or trimmed by readiness.
func (a *API) prescribed(w http.ResponseWriter, r *http.Request) {
	date, ok := a.dateParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	t, err := a.Store.GetTemplate(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such template")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	_, _, readiness, err := a.dayReadiness(r, date)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := strength.Prescribe(r.Context(), a.Store, t, readiness)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "plan": p, "template": t, "readiness": readiness})
}

// exerciseRow is one exercise name the athlete has used, with a starting
// point for a new workout: the last session's sets for a lift in history,
// else how another template sets it up.
type exerciseRow struct {
	Name        string            `json:"name"`
	Sets        []store.TargetSet `json:"sets"`
	RestSeconds int               `json:"rest_seconds"`
	LastAt      int64             `json:"last_at"` // 0: only in templates
}

func (a *API) exercises(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ts, err := a.Store.ListTemplates(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sets, err := a.Store.AllSets(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	byKey := map[string]*exerciseRow{}
	var order []string
	for _, t := range ts {
		for _, e := range t.Exercises {
			k := strings.ToLower(strings.TrimSpace(e.Name))
			if k == "" || byKey[k] != nil {
				continue
			}
			byKey[k] = &exerciseRow{Name: e.Name, Sets: e.Sets, RestSeconds: e.RestSeconds}
			order = append(order, k)
		}
	}
	// The newest session for each lift: its set count, target reps and top weight.
	type last struct {
		session string
		at      int64
		sets    []store.Set
	}
	latest := map[string]*last{}
	for _, s := range sets { // oldest first
		k := strings.ToLower(strings.TrimSpace(s.ExerciseName))
		if k == "" {
			continue
		}
		l := latest[k]
		if l == nil || l.session != s.SessionID {
			l = &last{session: s.SessionID, at: s.StartedAt}
			latest[k] = l
		}
		l.sets = append(l.sets, s)
	}
	for k, l := range latest {
		row := byKey[k]
		if row == nil {
			row = &exerciseRow{Name: l.sets[0].ExerciseName, RestSeconds: 120}
			byKey[k] = row
			order = append(order, k)
		}
		top := 0.0
		for _, s := range l.sets {
			top = max(top, s.WeightKg)
		}
		reps := l.sets[0].TargetReps
		if reps < 1 {
			reps = max(1, l.sets[0].Reps)
		}
		row.Sets = make([]store.TargetSet, min(len(l.sets), store.MaxSetsPerLift))
		for i := range row.Sets {
			row.Sets[i] = store.TargetSet{Reps: reps, WeightKg: top}
		}
		row.LastAt = l.at
	}
	out := make([]exerciseRow, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastAt != out[j].LastAt {
			return out[i].LastAt > out[j].LastAt
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, out)
}

func (a *API) getPlan(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	var id *string
	if a.demoOn(r) {
		if err := a.Demo.EnsureDay(r.Context(), date); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if x, err := a.Store.GetPlan(r.Context(), date); err == nil {
		id = &x
	} else if !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "template_id": id})
}

func (a *API) putPlan(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	var body struct {
		TemplateID string `json:"template_id"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if _, err := a.Store.GetTemplate(r.Context(), body.TemplateID); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "no such template")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Store.PutPlan(r.Context(), date, body.TemplateID); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"date": date, "template_id": body.TemplateID})
}

func (a *API) deletePlan(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	if err := a.Store.DeletePlan(r.Context(), date); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
