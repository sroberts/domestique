package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"time"

	"domestique/internal/coach"
	"domestique/internal/load"
	"domestique/internal/store"
	"domestique/internal/strength"
)

// Two things on Today come from the operator's models: an easier version of
// a planned lifting session on a day readiness says hold back (the LLM),
// and "feeling run down?" when the last mornings look like an illness
// coming on (System 1). Both are offers; neither changes the readiness call.

// easeView is the proposed easier session.
type easeView struct {
	Reason    string           `json:"reason"`
	Exercises []store.Exercise `json:"exercises"`
	Model     string           `json:"model,omitempty"`
}

// easeChoice is what the athlete chose for a day, kept at easeKey(date).
type easeChoice struct {
	Kept       bool             `json:"kept,omitempty"`
	TemplateID string           `json:"template_id,omitempty"`
	Exercises  []store.Exercise `json:"exercises,omitempty"`
}

func easeKey(date string) string    { return "ease:" + date }
func runDownKey(date string) string { return "run-down:dismissed:" + date }

// easedNote marks each exercise of a session the athlete eased for the day.
const easedNote = "Eased for today"

// todayAI adds the eased session, the proposal and the run-down prompt to
// Today. It runs after the prescription; an eased session the athlete took
// applies even when the models are off, since it's their choice.
func (a *API) todayAI(r *http.Request, resp *todayResp, series []load.Day, day time.Time) {
	ctx := r.Context()
	var ch easeChoice
	if v, err := a.Store.SyncState(ctx, easeKey(resp.Date)); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &ch)
	}
	if p := resp.Plan; p != nil && ch.TemplateID == p.ID && len(ch.Exercises) > 0 {
		eased := *p
		eased.Exercises = ch.Exercises
		resp.Plan = &eased
		resp.Eased = true
	}
	if !a.aiOn(r) {
		return
	}
	if p := resp.Plan; p != nil && !resp.Eased && !ch.Kept && (resp.Readiness.Level == "hold" || resp.Readiness.Level == "easy") {
		f := a.easeFacts(resp)
		e, ok, pending := a.Coach.Ease(ctx, f)
		resp.aiPending = resp.aiPending || pending
		if ok {
			if ex, err := coach.CheckEase(f.Plan, e.Exercises); err == nil {
				resp.Ease = &easeView{Reason: e.Reason, Exercises: fromEase(p.Exercises, ex), Model: e.Model}
			}
		}
	}
	if d, err := a.Store.SyncState(ctx, runDownKey(resp.Date)); err == nil && d != "" {
		return
	}
	f, ok := a.sickFacts(r, series, day)
	if !ok {
		return
	}
	type verdict struct {
		P float64 `json:"p"`
	}
	var v verdict
	ok, pending := a.Coach.Do(ctx, "ai:sick:"+resp.Date, coach.Fingerprint("sick-v1", f), &v,
		func(ctx context.Context) (any, error) {
			p, err := a.Coach.AI.Sick(ctx, f)
			return verdict{p}, err
		})
	resp.aiPending = resp.aiPending || pending
	resp.RunDown = ok && v.P >= coach.Bar("sick")
}

func toEase(ex []store.Exercise) []coach.EaseExercise {
	out := make([]coach.EaseExercise, len(ex))
	for i, e := range ex {
		out[i] = coach.EaseExercise{Name: e.Name, Sets: make([]coach.EaseSet, len(e.Sets))}
		for j, s := range e.Sets {
			out[i].Sets[j] = coach.EaseSet{Reps: s.Reps, Kg: s.WeightKg}
		}
	}
	return out
}

// fromEase is the plan's exercises with the eased sets; a lift the easier
// session changed says so in place of the prescription's note.
func fromEase(plan []store.Exercise, ex []coach.EaseExercise) []store.Exercise {
	out := make([]store.Exercise, len(plan))
	for i, p := range plan {
		sets := make([]store.TargetSet, len(ex[i].Sets))
		for j, s := range ex[i].Sets {
			sets[j] = store.TargetSet{Reps: s.Reps, WeightKg: s.Kg}
		}
		if !slices.Equal(sets, p.Sets) {
			p.Change = easedNote
		}
		p.Sets = sets
		out[i] = p
	}
	return out
}

func (a *API) easeFacts(t *todayResp) coach.EaseFacts {
	f := coach.EaseFacts{Date: t.Date, Level: t.Readiness.Level, Why: t.Readiness.Detail,
		Session: t.Plan.Name, Plan: toEase(t.Plan.Exercises)}
	if rec := t.Recovery; rec != nil {
		f.Recovery = &rec.Score
		if rec.HRVms > 0 {
			f.HRVms = ip(rec.HRVms)
		}
		if rec.RestingHR > 0 {
			f.RestingHR = &rec.RestingHR
		}
	}
	if sl := t.Sleep; sl != nil && sl.AsleepS > 0 {
		f.SleepH = r1p(float64(sl.AsleepS) / 3600)
	}
	if d := t.Load; d != nil && d.CTL > 5 {
		f.Form = ip(d.TSB)
	}
	return f
}

// sickFacts compares the last three mornings with the 28 before them. It
// needs every one of the three and at least two weeks of baseline.
func (a *API) sickFacts(r *http.Request, series []load.Day, day time.Time) (coach.SickFacts, bool) {
	ctx := r.Context()
	const last, base = 3, 28
	from := day.AddDate(0, 0, -(last + base - 1)).Format("2006-01-02")
	to := day.Format("2006-01-02")
	recs, err := a.Store.RecoveryRange(ctx, from, to)
	if err != nil {
		return coach.SickFacts{}, false
	}
	sleeps, _ := a.Store.SleepRange(ctx, from, to)
	sleepH := map[string]float64{}
	for _, s := range sleeps {
		sleepH[s.Date] = math.Max(sleepH[s.Date], float64(s.AsleepS)/3600)
	}
	tss := map[string]float64{}
	for _, d := range series {
		tss[d.Date] = d.TSS
	}
	split := day.AddDate(0, 0, -(last - 1)).Format("2006-01-02")
	var rhr, hrv, score, sl []float64
	byDate := map[string]store.Recovery{}
	for _, x := range recs {
		if x.Date >= split {
			byDate[x.Date] = x
			continue
		}
		if x.RestingHR > 0 && x.HRVms > 0 {
			rhr, hrv, score = append(rhr, float64(x.RestingHR)), append(hrv, x.HRVms), append(score, float64(x.Score))
			if h, ok := sleepH[x.Date]; ok && h > 0 {
				sl = append(sl, h)
			}
		}
	}
	if len(rhr) < 14 {
		return coach.SickFacts{}, false
	}
	f := coach.SickFacts{BaselineDays: len(rhr), RHRBase: ip(median(rhr)), HRVBase: ip(median(hrv)), RecoveryBase: ip(median(score))}
	if len(sl) >= 7 {
		f.SleepBaseH = r1p(median(sl))
	}
	var dr, dh float64
	names := []string{"2 days ago", "yesterday", "today"}
	for i := range last {
		d := day.AddDate(0, 0, -(last - 1 - i))
		x, ok := byDate[d.Format("2006-01-02")]
		if !ok || x.RestingHR <= 0 || x.HRVms <= 0 {
			return coach.SickFacts{}, false
		}
		sd := coach.SickDay{Day: names[i], RestingHR: &x.RestingHR, HRVms: ip(x.HRVms), Recovery: &x.Score}
		if h := sleepH[x.Date]; h > 0 {
			sd.SleepH = r1p(h)
		}
		if t, ok := tss[d.AddDate(0, 0, -1).Format("2006-01-02")]; ok {
			sd.TSSDayBefore = ip(t)
		}
		f.Days = append(f.Days, sd)
		dr += float64(x.RestingHR) - median(rhr)
		dh += (x.HRVms - median(hrv)) / median(hrv) * 100
	}
	f.RHRDelta, f.HRVDeltaPct = ip(dr/last), ip(dh/last)
	return f, true
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// ---- The athlete's answers --------------------------------------------------------

// easeDay takes the proposed easier session for a day, or keeps the plan:
// {"exercises": [...]} or {"keep": true}. The session is checked against the
// day's own prescription, so it can only ever be easier.
func (a *API) easeDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	var in struct {
		Keep      bool             `json:"keep"`
		Exercises []store.Exercise `json:"exercises"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ch := easeChoice{Kept: true}
	if !in.Keep {
		id, err := a.Store.GetPlan(ctx, date)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "nothing is planned that day")
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
		t, err := a.Store.GetTemplate(ctx, id)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		_, _, readiness, err := a.dayReadiness(r, date)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		p, err := strength.Prescribe(ctx, a.Store, t, readiness)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		ex, err := coach.CheckEase(toEase(p.Exercises), toEase(in.Exercises))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		ch = easeChoice{TemplateID: t.ID, Exercises: fromEase(p.Exercises, ex)}
	}
	b, _ := json.Marshal(ch)
	if err := a.Store.SetSyncState(ctx, easeKey(date), string(b)); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unEaseDay forgets the choice for a day: the plan as prescribed comes back,
// and the proposal with it.
func (a *API) unEaseDay(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	if err := a.Store.SetSyncState(r.Context(), easeKey(date), ""); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dismissRunDown hides "feeling run down?" for a day.
func (a *API) dismissRunDown(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	if err := a.Store.SetSyncState(r.Context(), runDownKey(date), "1"); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
