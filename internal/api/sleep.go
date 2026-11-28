package api

import (
	"net/http"
	"strconv"
	"time"

	"domestique/internal/load"
	"domestique/internal/sleep"
)

func (a *API) registerSleep(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/sleep", a.getSleep)
	mux.HandleFunc("GET /v1/sleep/trends", a.getSleepTrends)
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
	writeJSON(w, http.StatusOK, sleep.Build(sleep.Input{Today: today, Weeks: weeks, Nights: nights, Naps: naps,
		Recovery: rec, Load: days, Loc: loc}))
}
