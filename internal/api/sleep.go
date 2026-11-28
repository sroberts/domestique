package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"domestique/internal/load"
	"domestique/internal/sleep"
	"domestique/internal/store"
)

func (a *API) registerSleep(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/sleep", a.getSleep)
	mux.HandleFunc("GET /v1/sleep/trends", a.getSleepTrends)
	a.registerEvenings(mux)
}

// sleepYear is how far back the comparisons look: enough nights to say
// something, recent enough to still be this athlete.
const sleepYear = 365

// getSleep lists nights and naps that ended between from and to (default
// the last 14 days; at most 400).
func (a *API) getSleep(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(a.loc())
	to := now.Format("2006-01-02")
	from := now.AddDate(0, 0, -13).Format("2006-01-02")
	q := r.URL.Query()
	if v := q.Get("to"); v != "" {
		to = v
	}
	if v := q.Get("from"); v != "" {
		from = v
	}
	f, err1 := time.Parse("2006-01-02", from)
	t, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil || t.Before(f) || t.Sub(f) > 400*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "from and to must be dates (YYYY-MM-DD), from first, at most 400 days apart")
		return
	}
	nights, err := a.Store.SleepRange(r.Context(), from, to)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	naps, err := a.Store.NapRange(r.Context(), from, to)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "nights": nights, "naps": naps})
}

// getSleepTrends is the Sleep screen: last night against the norm, the
// last 2, 4 or 12 weeks, the week's debt, and the comparisons over a year.
func (a *API) getSleepTrends(w http.ResponseWriter, r *http.Request) {
	weeks := 2
	if v, err := strconv.Atoi(r.URL.Query().Get("weeks")); err == nil {
		if v != 2 && v != 4 && v != 12 {
			writeErr(w, http.StatusBadRequest, "weeks must be 2, 4 or 12")
			return
		}
		weeks = v
	}
	ctx := r.Context()
	loc := a.loc()
	now := time.Now().In(loc)
	today := now.Format("2006-01-02")
	from := now.AddDate(0, 0, -sleepYear)
	nights, err := a.Store.SleepRange(ctx, from.Format("2006-01-02"), today)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	naps, err := a.Store.NapRange(ctx, now.AddDate(0, 0, -7*weeks).Format("2006-01-02"), today)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rec, err := a.Store.RecoveryRange(ctx, from.Format("2006-01-02"), today)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	days, err := load.Daily(ctx, a.Store, from, now, loc)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	evs, err := a.Store.EveningRange(ctx, from.Format("2006-01-02"), today)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	acts, err := a.Store.ListActivities(ctx, store.ActivityQuery{From: from.UnixMilli()})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	workouts := make([]sleep.Workout, 0, len(acts))
	for _, x := range acts {
		workouts = append(workouts, sleep.Workout{EndAt: x.StartAt + int64(x.ElapsedS)*1000})
	}
	writeJSON(w, http.StatusOK, sleep.Build(sleep.Input{Today: today, Weeks: weeks, Nights: nights, Naps: naps,
		Recovery: rec, Load: days, Evenings: evs, Workouts: workouts, Loc: loc}))
}

func (a *API) registerEvenings(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/evenings/{date}", a.getEvening)
	mux.HandleFunc("PUT /v1/evenings/{date}", a.putEvening)
	mux.HandleFunc("DELETE /v1/evenings/{date}", a.deleteEvening)
}

// eveningDate is a valid evening: a date, and not in the future.
func (a *API) eveningDate(w http.ResponseWriter, r *http.Request) (string, bool) {
	d := r.PathValue("date")
	t, err := time.ParseInLocation("2006-01-02", d, a.loc())
	if err != nil || t.After(time.Now().In(a.loc())) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD, today or before")
		return "", false
	}
	return d, true
}

type eveningResp struct {
	Date    string              `json:"date"`
	Logged  bool                `json:"logged"` // false: unknown, which isn't the same as clear
	Items   []string            `json:"items"`
	Choices []sleep.EveningItem `json:"choices"`
}

func (a *API) getEvening(w http.ResponseWriter, r *http.Request) {
	d, ok := a.eveningDate(w, r)
	if !ok {
		return
	}
	e, err := a.Store.GetEvening(r.Context(), d)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	resp := eveningResp{Date: d, Logged: err == nil, Items: []string{}, Choices: sleep.EveningItems}
	if err == nil {
		resp.Items = e.Items
	}
	writeJSON(w, http.StatusOK, resp)
}

// putEvening logs an evening: the items done, or none for a clear evening.
// Each tap on the card sends the whole list.
func (a *API) putEvening(w http.ResponseWriter, r *http.Request) {
	d, ok := a.eveningDate(w, r)
	if !ok {
		return
	}
	var body struct {
		Items []string `json:"items"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	seen := map[string]bool{}
	items := []string{}
	for _, it := range body.Items {
		if !sleep.IsEveningItem(it) {
			writeErr(w, http.StatusBadRequest, "unknown item "+strconv.Quote(it))
			return
		}
		if !seen[it] {
			seen[it] = true
			items = append(items, it)
		}
	}
	if err := a.Store.PutEvening(r.Context(), store.Evening{Date: d, Items: items, UpdatedAt: time.Now().UnixMilli()}); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, eveningResp{Date: d, Logged: true, Items: items, Choices: sleep.EveningItems})
}

func (a *API) deleteEvening(w http.ResponseWriter, r *http.Request) {
	d, ok := a.eveningDate(w, r)
	if !ok {
		return
	}
	if err := a.Store.DeleteEvening(r.Context(), d); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
