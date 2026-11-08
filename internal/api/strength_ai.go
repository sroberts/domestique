package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"domestique/internal/coach"
	"domestique/internal/store"
	"domestique/internal/strength"
)

// A workout already done, logged from text, and exercise names counted as
// one another. Reading text needs the operator's LLM; saving a logged
// workout and the aliases don't.

func (a *API) registerStrengthAI(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/sessions/parse-log", a.parseLog)
	mux.HandleFunc("POST /v1/sessions/logged", a.saveLogged)
	mux.HandleFunc("GET /v1/exercise-aliases", a.listAliases)
	mux.HandleFunc("DELETE /v1/exercise-aliases/{name}", a.undoAlias)
}

// loggedDraft is a finished workout for the athlete to check, and what
// saveLogged takes back.
type loggedDraft struct {
	Name      string           `json:"name"`
	StartedAt int64            `json:"started_at"` // epoch ms
	Minutes   int              `json:"minutes"`
	Exercises []store.Exercise `json:"exercises"`
	NotLogged []string         `json:"not_logged,omitempty"`
}

// defaultRest is the rest assumed between sets when none was given.
const defaultRest = 90

// setSeconds is how long a set of reps is taken to last: no less than a
// set must last to count as lifting (strength.Real).
func setSeconds(reps int) int {
	return max(30, strength.MinSetSeconds, strength.SecondsPerRep*reps)
}

// estimateMinutes is how long the lifting took, when the athlete didn't say.
func estimateMinutes(exs []store.Exercise) int {
	secs := 0
	for _, e := range exs {
		rest := e.RestSeconds
		if rest <= 0 {
			rest = defaultRest
		}
		for _, s := range e.Sets {
			secs += setSeconds(s.Reps) + rest
		}
	}
	return max(10, (secs+59)/60)
}

// parseLog turns "did 3x10 push-ups, then squats 5x5 at 80" into a draft
// of a finished session. Nothing is saved: the athlete checks it first.
func (a *API) parseLog(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || len(in.Text) > 4000 {
		writeErr(w, http.StatusBadRequest, "text must be 1-4000 characters")
		return
	}
	if a.Coach == nil {
		writeErr(w, http.StatusServiceUnavailable, "no AI model is available from Outpost")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	now := time.Now().In(a.loc())
	got, err := a.Coach.ParseLog(ctx, in.Text, now)
	if err != nil {
		if coach.Unavailable(err) || ctx.Err() != nil {
			writeErr(w, http.StatusServiceUnavailable, "the AI model isn't available right now")
			return
		}
		a.Log.Warn("parse logged workout", "err", err.Error())
		writeErr(w, http.StatusUnprocessableEntity, "couldn't read a workout from that text")
		return
	}
	t := store.Template{ID: "logged", Name: got.Name}
	if t.Name == "" {
		t.Name = "Logged workout"
	}
	for _, e := range got.Exercises {
		ex := store.Exercise{Name: e.Name, RestSeconds: e.RestSeconds}
		for _, s := range e.Sets {
			ex.Sets = append(ex.Sets, store.TargetSet{Reps: s.Reps, WeightKg: s.WeightKg})
		}
		t.Exercises = append(t.Exercises, ex)
	}
	if t, err = store.CleanTemplate(t); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "the model's reading didn't make a valid workout: "+err.Error())
		return
	}
	d := loggedDraft{Name: t.Name, Minutes: got.Minutes, Exercises: t.Exercises, NotLogged: got.NotLogged}
	if d.Minutes == 0 {
		d.Minutes = estimateMinutes(t.Exercises)
	}
	d.StartedAt = loggedStart(now, got.Date, got.Time, d.Minutes)
	writeJSON(w, http.StatusOK, d)
}

// loggedStart places a logged workout in time: at the stated day and
// clock time; on a past day with no time, at noon; with neither, ending now.
// It's never in the future.
func loggedStart(now time.Time, date, clock string, minutes int) int64 {
	endedNow := now.Add(-time.Duration(minutes) * time.Minute)
	day := now
	if date != "" {
		if d, err := time.ParseInLocation("2006-01-02", date, now.Location()); err == nil {
			day = d
		}
	}
	hh, mm := 12, 0
	sameDay := day.Year() == now.Year() && day.YearDay() == now.YearDay()
	if clock != "" {
		if t, err := time.Parse("15:04", clock); err == nil {
			hh, mm = t.Hour(), t.Minute()
		}
	} else if sameDay {
		return endedNow.UnixMilli()
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, now.Location())
	if start.After(endedNow) {
		return endedNow.UnixMilli()
	}
	return start.UnixMilli()
}

// saveLogged stores a finished workout the athlete checked: a finished
// session whose sets are spread over its duration, so each counts as
// lifting, mirrored into activities like any session.
func (a *API) saveLogged(w http.ResponseWriter, r *http.Request) {
	var d loggedDraft
	if !readJSON(w, r, &d) {
		return
	}
	now := time.Now()
	t, err := store.CleanTemplate(store.Template{ID: "logged", Name: d.Name, Exercises: d.Exercises})
	switch {
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	case d.StartedAt <= 0 || d.StartedAt > now.Add(5*time.Minute).UnixMilli() || d.StartedAt < now.AddDate(-1, 0, 0).UnixMilli():
		writeErr(w, http.StatusBadRequest, "started_at must be within the last year, and not in the future")
		return
	case d.Minutes < 1 || d.Minutes > 300:
		writeErr(w, http.StatusBadRequest, "minutes must be 1-300")
		return
	}

	id := newID("log_")
	type slot struct {
		ex, set int
		s       store.TargetSet
	}
	var slots []slot
	need := 0
	for i, e := range t.Exercises {
		for j, s := range e.Sets {
			slots = append(slots, slot{i, j, s})
			need = max(need, setSeconds(s.Reps))
		}
	}
	// Even spacing, each set at the start of its share of the time; a
	// duration too short for the sets is stretched to fit them.
	step := max(int64(d.Minutes)*60_000/int64(len(slots)), int64(need)*1000)
	ctx := r.Context()
	at := now.UnixMilli()
	end := d.StartedAt + step*int64(len(slots))
	sess := store.Session{ID: id, TemplateID: "logged", Name: t.Name, Plan: t.Exercises,
		StartedAt: d.StartedAt, FinishedAt: &end, UpdatedAt: at}
	if err := a.Store.PutSession(ctx, sess); err != nil {
		a.fail(w, r, err)
		return
	}
	for k, sl := range slots {
		start := d.StartedAt + step*int64(k)
		set := store.Set{ID: id + "_" + strconv.Itoa(k), SessionID: id, ExerciseIndex: sl.ex,
			ExerciseName: t.Exercises[sl.ex].Name, SetIndex: sl.set,
			TargetReps: sl.s.Reps, TargetWeightKg: sl.s.WeightKg, Reps: sl.s.Reps, WeightKg: sl.s.WeightKg,
			StartedAt: start, EndedAt: start + int64(setSeconds(sl.s.Reps))*1000, UpdatedAt: at}
		if err := a.Store.PutSet(ctx, set); err != nil {
			_ = a.Store.DeleteSession(ctx, id) // no half-logged workout
			a.fail(w, r, err)
			return
		}
	}
	if a.Ingest != nil {
		if err := a.Ingest.UpsertSession(ctx, id); err != nil {
			a.Log.Warn("logged session to activity", "session", id, "err", err.Error())
		}
		a.kickRecalc()
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func newID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// ---- Exercise aliases ------------------------------------------------------------

func (a *API) listAliases(w http.ResponseWriter, r *http.Request) {
	all, err := a.Store.Aliases(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	live := []store.Alias{}
	for _, x := range all {
		if !x.Undone {
			live = append(live, x)
		}
	}
	writeJSON(w, http.StatusOK, live)
}

// undoAlias stops counting a name as another. The alias is kept, marked
// undone, so the name isn't asked about again.
func (a *API) undoAlias(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	x, err := a.Store.GetAlias(ctx, r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && x.Undone) {
		writeErr(w, http.StatusNotFound, "no such alias")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	x.Undone = true
	if err := a.Store.PutAlias(ctx, x); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, x)
}
