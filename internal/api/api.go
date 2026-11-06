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
	"strings"
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
	// Whoop handles Whoop webhooks; nil until the store is open.
	Whoop *provider.Whoop
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
	a.registerTemplates(mux)
	mux.HandleFunc("PUT /v1/recovery/{date}", a.putRecovery)
	mux.HandleFunc("GET /v1/sessions", a.listSessions)
	mux.HandleFunc("PUT /v1/sessions/{id}", a.putSession)
	mux.HandleFunc("DELETE /v1/sessions/{id}", a.deleteSession)
	mux.HandleFunc("GET /v1/sessions/{id}/summary", a.summary)
	mux.HandleFunc("PUT /v1/sessions/{id}/hr-shift", a.putHRShift)
	mux.HandleFunc("PUT /v1/sets/{id}", a.putSet)
	a.registerActivities(mux)
	a.registerConnections(mux)
	a.registerWebhooks(mux)
	a.registerExport(mux)
	a.registerGear(mux)
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
	Recent    []sessionRow       `json:"recent"`
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
	rec, series, readiness, err := a.dayReadiness(r, date)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp := todayResp{Date: date, Recovery: rec, Readiness: readiness}
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
	recent, err := a.sessionRows(ctx, 5)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp.Recent = recent
	if resp.Plan == nil && resp.Readiness.Level == "normal" {
		// Nothing planned: "train as planned" would answer a question nobody asked.
		resp.Readiness.Title = "Good to train"
		resp.Readiness.Detail = strings.Replace(resp.Readiness.Detail, "nothing in the way of today's plan.", "nothing in the way of a hard session.", 1)
	}

	// Load: form today, fitness ramp, and this week so far.
	day, _ := time.ParseInLocation("2006-01-02", date, a.loc())
	if n := len(series); n > 0 {
		d := series[n-1]
		resp.Load = &d
		if n >= 8 {
			resp.RampCTL = math.Round((d.CTL-series[n-8].CTL)*10) / 10
		}
		wd := (int(day.Weekday()) + 6) % 7
		if wd+1 <= n {
			resp.Week = series[n-1-wd:]
		}
	}
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
	ss, err := a.sessionRows(r.Context(), limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ss)
}

// sessionRow is a finished session with how much of its plan was lifted.
type sessionRow struct {
	store.Session
	Progress strength.Progress `json:"progress"`
}

func (a *API) sessionRows(ctx context.Context, limit int) ([]sessionRow, error) {
	ss, err := a.Store.RecentSessions(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]sessionRow, len(ss))
	for i, s := range ss {
		sets, err := a.Store.SessionSets(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		out[i] = sessionRow{s, strength.ProgressOf(s.Plan, sets)}
	}
	return out, nil
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
	case s.ClockOffsetMs < -maxClockOffset || s.ClockOffsetMs > maxClockOffset:
		writeErr(w, http.StatusBadRequest, "clock_offset_ms is out of range")
		return
	}
	s.Strain = nil    // strain is Whoop's to set
	s.HRShiftMs = nil // set through /hr-shift, never by a replayed upload
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

// maxClockOffset bounds the phone's measured clock error. A phone more than
// six hours out is misconfigured, not drifting.
const maxClockOffset = 6 * 60 * 60 * 1000

// putHRShift sets a manual heart rate alignment for a session, in ms
// (positive moves heart rate earlier), or clears it with null to go back to
// the automatic estimate.
func (a *API) putHRShift(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad session id")
		return
	}
	var body struct {
		ShiftMs *int64 `json:"shift_ms"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.ShiftMs != nil && (*body.ShiftMs < -5*strength.MaxShift || *body.ShiftMs > 5*strength.MaxShift) {
		writeErr(w, http.StatusBadRequest, "shift_ms is out of range (±10 minutes)")
		return
	}
	err := a.Store.SetHRShift(r.Context(), id, body.ShiftMs)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "shift_ms": body.ShiftMs})
}

// deleteSession discards a session started by mistake: the session, its sets,
// and its place in the activity store and training load.
func (a *API) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad session id")
		return
	}
	if a.Ingest != nil {
		if err := a.Ingest.RemoveSession(r.Context(), id); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if err := a.Store.DeleteSession(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	a.kickRecalc()
	w.WriteHeader(http.StatusNoContent)
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
	// Heart rate lives on true time; fetch wide enough for the clock offset
	// and any shift, line it up with the phone's set times, then trim.
	off, margin := sess.ClockOffsetMs, int64(strength.MaxShift)
	if m := sess.HRShiftMs; m != nil {
		margin = max(margin, *m, -*m)
	}
	raw, err := a.Store.HR(ctx, sess.StartedAt+off-strength.HRPadding-margin, end+off+strength.HRPadding+margin)
	if err != nil {
		return nil, err
	}
	aligned, al := strength.Align(sess, sets, raw)
	hr := aligned[:0:0]
	for _, h := range aligned {
		if h.T >= sess.StartedAt-strength.HRPadding && h.T <= end+strength.HRPadding {
			hr = append(hr, h)
		}
	}
	maxHR := load.Current(ctx, a.Store, a.MaxHR).MaxHR
	sum := strength.Build(sess, sets, hr, maxHR)
	sum.Alignment = al
	if sum.HRStatus == "pending" && !a.demoOn(r) {
		// Only promise heart rate when something is sending it: a source that
		// has posted samples lately, and a session recent enough to still sync.
		latest, err := a.Store.LatestHR(ctx)
		if err != nil {
			return nil, err
		}
		const window = 14 * 24 * time.Hour
		if latest == 0 || time.Since(time.UnixMilli(latest)) > window || time.Since(time.UnixMilli(end)) > 24*time.Hour {
			sum.HRStatus = "none"
		}
	}
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
	// The phone measures its clock against this to line sets up with heart
	// rate (Session.ClockOffsetMs). The Mac Mini keeps NTP time.
	w.Header().Set("X-Server-Time", strconv.FormatInt(time.Now().UnixMilli(), 10))
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
