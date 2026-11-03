// Package api serves Domestique's REST endpoints under /v1.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"domestique/internal/auth"
	"domestique/internal/demo"
	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/provider"
	"domestique/internal/store"
	"domestique/internal/strength"
)

// API holds handler dependencies.
type API struct {
	Store     *store.Store
	Ingest    *ingest.Ingester
	Demo      *demo.Demo // nil unless DOMESTIQUE_DEMO=1
	MaxHR     int        // fallback max HR until one is detected
	Log       *slog.Logger
	Loc       *time.Location
	Providers map[string]*provider.Client
	// Recalc asks for training load to be recomputed soon (debounced).
	Recalc  func()
	Uploads *Uploads
	// Gateway is true when the Outpost login gateway is in front (OUTPOST_AUTH=gateway).
	Gateway bool
	// background outlives requests (imports); cancelled on shutdown.
	background context.Context
}

// SetBackground sets the context long-running imports run under.
func (a *API) SetBackground(ctx context.Context) { a.background = ctx }

func (a *API) loc() *time.Location {
	if a.Loc != nil {
		return a.Loc
	}
	return time.Local
}

// demoOn: demo data stands in for Whoop only until Whoop is connected.
func (a *API) demoOn(r *http.Request) bool {
	if a.Demo == nil {
		return false
	}
	if c := a.Providers["whoop"]; c != nil && c.Status(r.Context()).Connected {
		return false
	}
	return true
}

func (a *API) kickRecalc() {
	if a.Recalc != nil {
		a.Recalc()
	}
}

// Register mounts every /v1 route on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/today", a.today)
	mux.HandleFunc("GET /v1/me", a.me)
	mux.HandleFunc("GET /v1/templates", a.listTemplates)
	mux.HandleFunc("GET /v1/templates/{id}", a.getTemplate)
	mux.HandleFunc("PUT /v1/templates/{id}", a.putTemplate)
	mux.HandleFunc("PUT /v1/plans/{date}", a.putPlan)
	mux.HandleFunc("DELETE /v1/plans/{date}", a.deletePlan)
	mux.HandleFunc("PUT /v1/recovery/{date}", a.putRecovery)
	mux.HandleFunc("GET /v1/sessions", a.listSessions)
	mux.HandleFunc("PUT /v1/sessions/{id}", a.putSession)
	mux.HandleFunc("GET /v1/sessions/{id}/summary", a.summary)
	mux.HandleFunc("PUT /v1/sets/{id}", a.putSet)
	a.registerActivities(mux)
	a.registerConnections(mux)
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "no such endpoint")
	})
}

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	idRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

func validDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// me tells the app who is signed in, so it can offer Outpost's sign-out.
func (a *API) me(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"gateway": a.Gateway, "user": map[bool]any{true: u, false: nil}[ok]})
}

// ---- Today ----------------------------------------------------------------------

type todayResp struct {
	Date      string             `json:"date"`
	Recovery  *store.Recovery    `json:"recovery"`
	Sleep     *store.Sleep       `json:"sleep"`
	Plan      *store.Template    `json:"plan"`
	Template  *store.Template    `json:"template"` // the plan before today's adjustments
	Readiness strength.Readiness `json:"readiness"`
	Load      *load.Day          `json:"load"`
	RampCTL   float64            `json:"ramp_ctl"` // fitness change over 7 days
	Week      []load.Day         `json:"week"`     // Monday to today
	Recent    []store.Session    `json:"recent"`
	Latest    []ActivityRow      `json:"latest"`
	Athlete   load.Athlete       `json:"athlete"`
}

func (a *API) today(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// The athlete's local date comes from the device; the Mini's zone may differ.
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().In(a.loc()).Format("2006-01-02")
	}
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	if a.demoOn(r) {
		if err := a.Demo.EnsureDay(ctx, date); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	resp := todayResp{Date: date}
	if rec, err := a.Store.GetRecovery(ctx, date); err == nil {
		resp.Recovery = &rec
	} else if !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	if id, err := a.Store.GetPlan(ctx, date); err == nil {
		t, err := a.Store.GetTemplate(ctx, id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			a.fail(w, r, err)
			return
		}
		if err == nil {
			resp.Plan = &t
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	recent, err := a.Store.RecentSessions(ctx, 5)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp.Recent = recent

	// Load: form today, fitness ramp, and this week so far.
	day, _ := time.ParseInLocation("2006-01-02", date, a.loc())
	series, err := load.Daily(ctx, a.Store, day.AddDate(0, 0, -13), day, a.loc())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	hasLoad := false
	if n := len(series); n > 0 {
		d := series[n-1]
		resp.Load = &d
		hasLoad = d.CTL > 5
		if n >= 8 {
			resp.RampCTL = math.Round((d.CTL-series[n-8].CTL)*10) / 10
		}
		wd := (int(day.Weekday()) + 6) % 7
		if wd+1 <= n {
			resp.Week = series[n-1-wd:]
		}
	}
	tsb := 0.0
	if resp.Load != nil {
		tsb = resp.Load.TSB
	}
	resp.Readiness = strength.AssessReadiness(resp.Recovery, tsb, hasLoad)
	if resp.Plan != nil {
		tpl := *resp.Plan
		resp.Template = &tpl
		p, err := strength.Prescribe(ctx, a.Store, tpl, resp.Readiness)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		resp.Plan = &p
	}
	if sl, err := a.Store.SleepRange(ctx, date, date); err == nil && len(sl) > 0 {
		resp.Sleep = &sl[len(sl)-1]
	}
	latest, err := a.Store.ListActivities(ctx, store.ActivityQuery{Limit: 4})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if resp.Latest, err = a.rows(r, latest); err != nil {
		a.fail(w, r, err)
		return
	}
	resp.Athlete = load.Current(ctx, a.Store, a.MaxHR)
	writeJSON(w, http.StatusOK, resp)
}

// ---- Templates and plans ------------------------------------------------------------

func (a *API) listTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := a.Store.ListTemplates(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ts)
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
	if msg := validateTemplate(t); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	t.UpdatedAt = time.Now().UnixMilli()
	if err := a.Store.PutTemplate(r.Context(), t); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func validateTemplate(t store.Template) string {
	switch {
	case !idRe.MatchString(t.ID):
		return "id must be 1-64 letters, digits, dashes or underscores"
	case t.Name == "" || len(t.Name) > 80:
		return "name is required (max 80 characters)"
	case len(t.Exercises) == 0:
		return "a template needs at least one exercise"
	}
	for _, e := range t.Exercises {
		if e.Name == "" || len(e.Sets) == 0 {
			return "every exercise needs a name and at least one set"
		}
		if e.RestSeconds < 0 || e.RestSeconds > 900 {
			return "rest_seconds must be 0-900"
		}
		for _, s := range e.Sets {
			if s.Reps < 1 || s.Reps > 100 || s.WeightKg < 0 || s.WeightKg > 1000 {
				return "set reps must be 1-100 and weight_kg 0-1000"
			}
		}
	}
	return ""
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

func (a *API) putRecovery(w http.ResponseWriter, r *http.Request) {
	var rec store.Recovery
	if !readJSON(w, r, &rec) {
		return
	}
	rec.Date = r.PathValue("date")
	if !validDate(rec.Date) || rec.Score < 0 || rec.Score > 100 {
		writeErr(w, http.StatusBadRequest, "need a YYYY-MM-DD date and a score of 0-100")
		return
	}
	if rec.Source == "" {
		rec.Source = "api"
	}
	if err := a.Store.PutRecovery(r.Context(), rec); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ---- Sessions and sets ------------------------------------------------------------------

func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	ss, err := a.Store.RecentSessions(r.Context(), limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ss)
}

func (a *API) putSession(w http.ResponseWriter, r *http.Request) {
	var s store.Session
	if !readJSON(w, r, &s) {
		return
	}
	s.ID = r.PathValue("id")
	switch {
	case !idRe.MatchString(s.ID):
		writeErr(w, http.StatusBadRequest, "bad session id")
		return
	case s.Name == "" || s.StartedAt <= 0 || s.UpdatedAt <= 0 || len(s.Plan) == 0:
		writeErr(w, http.StatusBadRequest, "session needs name, plan, started_at and updated_at")
		return
	case s.FinishedAt != nil && *s.FinishedAt < s.StartedAt:
		writeErr(w, http.StatusBadRequest, "finished_at is before started_at")
		return
	}
	s.Strain = nil // strain is Whoop's to set
	if err := a.Store.PutSession(r.Context(), s); err != nil {
		a.fail(w, r, err)
		return
	}
	if s.FinishedAt != nil && a.Ingest != nil {
		if err := a.Ingest.UpsertSession(r.Context(), s.ID); err != nil {
			a.Log.Warn("session to activity", "session", s.ID, "err", err.Error())
		}
		a.kickRecalc()
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": s.ID})
}

func (a *API) putSet(w http.ResponseWriter, r *http.Request) {
	var s store.Set
	if !readJSON(w, r, &s) {
		return
	}
	s.ID = r.PathValue("id")
	switch {
	case !idRe.MatchString(s.ID) || !idRe.MatchString(s.SessionID):
		writeErr(w, http.StatusBadRequest, "bad set or session id")
		return
	case s.StartedAt <= 0 || s.EndedAt < s.StartedAt || s.UpdatedAt <= 0:
		writeErr(w, http.StatusBadRequest, "set needs started_at <= ended_at and updated_at")
		return
	case s.Reps < 0 || s.Reps > 100 || s.WeightKg < 0 || s.WeightKg > 1000:
		writeErr(w, http.StatusBadRequest, "reps must be 0-100 and weight_kg 0-1000")
		return
	}
	if err := a.Store.PutSet(r.Context(), s); err != nil {
		a.fail(w, r, err)
		return
	}
	// A set edited (or synced late) after its session finished must reach the
	// activity too, or its volume and set count go stale.
	if a.Ingest != nil {
		if sess, err := a.Store.GetSession(r.Context(), s.SessionID); err == nil && sess.FinishedAt != nil {
			if err := a.Ingest.UpsertSession(r.Context(), sess.ID); err != nil {
				a.Log.Warn("session to activity", "session", sess.ID, "err", err.Error())
			}
			a.kickRecalc()
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": s.ID})
}

func (a *API) summary(w http.ResponseWriter, r *http.Request) {
	sum, err := a.strengthSummary(r, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (a *API) strengthSummary(r *http.Request, id string) (*strength.Summary, error) {
	ctx := r.Context()
	sess, err := a.Store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	sets, err := a.Store.SessionSets(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	if a.demoOn(r) {
		fresh, err := a.Demo.EnsureHR(ctx, sess, sets)
		if err != nil {
			return nil, err
		}
		if sess, err = a.Store.GetSession(ctx, sess.ID); err != nil {
			return nil, err
		}
		if fresh && a.Ingest != nil {
			if act, err := a.Store.ActivityBySession(ctx, sess.ID); err != nil || act.Strain == nil {
				_ = a.Ingest.UpsertSession(ctx, sess.ID)
				a.kickRecalc()
			}
		}
	}
	end := sess.StartedAt
	if sess.FinishedAt != nil {
		end = *sess.FinishedAt
	} else if n := len(sets); n > 0 {
		end = sets[n-1].EndedAt
	}
	hr, err := a.Store.HR(ctx, sess.StartedAt-strength.HRPadding, end+strength.HRPadding)
	if err != nil {
		return nil, err
	}
	maxHR := load.Current(ctx, a.Store, a.MaxHR).MaxHR
	sum := strength.Build(sess, sets, hr, maxHR)
	return &sum, nil
}

// ---- Helpers ------------------------------------------------------------------------------

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	a.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err.Error())
	writeErr(w, http.StatusInternalServerError, "internal error")
}
