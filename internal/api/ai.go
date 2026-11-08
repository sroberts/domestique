package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"domestique/internal/activity"
	"domestique/internal/coach"
	"domestique/internal/load"
	"domestique/internal/store"
	"domestique/internal/strength"
	"domestique/internal/sweep"
)

// Everything here is an enhancement from the operator's models (Outpost
// service spec §15): with no models, demo data, or a model that's down, each
// response is exactly what it was without them.

func (a *API) registerAI(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/week-note", a.weekNote)
	mux.HandleFunc("POST /v1/templates/parse", a.parseTemplate)
	mux.HandleFunc("POST /v1/activities/{id}/keep", a.keepActivity)
	mux.HandleFunc("POST /v1/ask", a.ask)
	mux.HandleFunc("POST /v1/plans/{date}/ease", a.easeDay)
	mux.HandleFunc("DELETE /v1/plans/{date}/ease", a.unEaseDay)
	mux.HandleFunc("POST /v1/run-down/{date}/dismiss", a.dismissRunDown)
	mux.HandleFunc("POST /v1/activities/{id}/data-ok", a.dataOK)
	a.registerStrengthAI(mux)
}

func (a *API) aiOn(r *http.Request) bool { return a.Coach != nil && !a.demoOn(r) }

func ip(v float64) *int { n := int(math.Round(v)); return &n }

func r1p(v float64) *float64 { x := math.Round(v*10) / 10; return &x }

// ---- Activity detail -----------------------------------------------------------

// aiDetail adds the LLM's note, the tags read from the athlete's
// descriptions, and the stray verdict to an activity's merged view.
func (a *API) aiDetail(r *http.Request, resp *detailResp) {
	ctx := r.Context()
	a.qualityDetail(ctx, resp)
	if v, err := a.Store.SyncState(ctx, load.StrayPrefix+resp.Activity.ID); err == nil && v == "stray" {
		resp.Stray = true
	}
	if !a.aiOn(r) {
		return
	}
	for _, src := range resp.Sources {
		if t, ok := sweep.SourceTags(ctx, a.Coach, src); ok {
			for _, tag := range t.Tags {
				if !contains(resp.Tags, tag) {
					resp.Tags = append(resp.Tags, tag)
				}
			}
			if resp.TagNote == "" {
				resp.TagNote = t.Note
			}
		}
	}
	// A logged lifting session gets its own debrief in Strength instead.
	if resp.Strength != nil || resp.Activity.ElapsedS < 5*60 {
		return
	}
	n, ok, pending := a.Coach.ActivityNote(ctx, resp.Activity.ID, a.activityFacts(ctx, resp))
	if ok {
		resp.Note = &strength.AINote{Text: n.Text}
	} else if pending {
		resp.Note = &strength.AINote{Pending: true}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (a *API) activityFacts(ctx context.Context, d *detailResp) coach.ActivityFacts {
	act, m := d.Activity, d.Metrics
	secs := act.MovingS
	if secs <= 0 {
		secs = act.ElapsedS
	}
	f := coach.ActivityFacts{Name: act.Name, Sport: act.Sport, Indoor: act.Sport == activity.SportVirtualRide,
		Minutes: int(math.Round(float64(secs) / 60)), Strain: act.Strain}
	if act.DistanceM > 0 {
		f.KM = r1p(act.DistanceM / 1000)
	}
	if m.AscentM >= 10 {
		f.AscentM = ip(m.AscentM)
	}
	if m.AvgPower > 0 {
		f.AvgPower = &m.AvgPower
	}
	if m.NP > 0 {
		f.NP = &m.NP
		if act.FTP > 0 {
			f.FTP = &act.FTP
		}
	}
	if act.Intensity > 0 {
		x := math.Round(act.Intensity*100) / 100
		f.IF = &x
	}
	if act.TSS > 0 {
		f.TSS = ip(act.TSS)
	}
	if m.AvgHR > 0 {
		f.AvgHR, f.MaxHR = &m.AvgHR, &m.MaxHR
	}
	var zone []int
	any := false
	for _, s := range d.HRZones[1:] {
		mins := int(math.Round(float64(s) / 60))
		zone = append(zone, mins)
		any = any || mins > 0
	}
	if any {
		f.ZoneMins = zone
	}
	for i, best := range d.BestCurve {
		if i < len(m.PowerCurve) && best > 0 && m.PowerCurve[i] > best && activity.CurveDurations[i] >= 5 {
			f.BestEver = append(f.BestEver, fmt.Sprintf("%s: %d W", durLabel(activity.CurveDurations[i]), m.PowerCurve[i]))
		}
	}
	if len(f.BestEver) > 3 {
		f.BestEver = f.BestEver[len(f.BestEver)-3:] // the longest say the most
	}
	for _, src := range d.Sources {
		if desc := sweep.Description(src); desc != "" {
			f.AthleteNote = desc
			break
		}
	}
	return f
}

func durLabel(s int) string {
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d min", s/60)
	case s%3600 == 0:
		return fmt.Sprintf("%d h", s/3600)
	}
	return fmt.Sprintf("%d min", s/60)
}

// ---- Strength sessions ---------------------------------------------------------

// aiSession adds System 1's effort per set and the LLM's debrief to a
// finished session's summary.
func (a *API) aiSession(r *http.Request, sum *strength.Summary) {
	if !a.aiOn(r) || sum.Session.FinishedAt == nil || len(sum.Sets) == 0 {
		return
	}
	ctx := r.Context()
	stats := map[string]strength.SetStat{}
	for _, st := range sum.SetStats {
		stats[st.SetID] = st
	}

	// Effort needs heart rate; wait for it rather than judge without.
	if sum.HRStatus == "ready" {
		states := map[string]coach.SetState{}
		count := map[int]int{}
		for _, s := range sum.Sets {
			count[s.ExerciseIndex]++
		}
		prev := map[int]float64{}
		for _, s := range sum.Sets {
			st := stats[s.ID]
			if !st.HasHR || len(states) == 64 {
				continue
			}
			states[s.ID] = coach.SetState{Exercise: s.ExerciseName, Set: s.SetIndex + 1, Of: count[s.ExerciseIndex],
				Reps: s.Reps, TargetReps: s.TargetReps, Kg: s.WeightKg, PrevKg: prev[s.ExerciseIndex],
				PeakHR: st.PeakBPM, AvgHR: st.AvgBPM, Drop60: st.RecoveryDrop, RestS: st.RestS, MaxHR: sum.MaxHR}
			prev[s.ExerciseIndex] = s.WeightKg
		}
		var eff map[string]coach.SetEffort
		ok, pending := a.Coach.Do(ctx, "ai:effort:"+sum.Session.ID, coach.Fingerprint("effort-v1", states), &eff,
			func(ctx context.Context) (any, error) { return a.Coach.AI.SetEfforts(ctx, states) })
		if ok && len(eff) > 0 {
			sum.Efforts = map[string]strength.Effort{}
			for id, e := range eff {
				sum.Efforts[id] = strength.Effort{Level: e.Level, Score: math.Round(e.Score*100) / 100}
			}
		}
		sum.EffortsPending = pending
	}

	f := coach.SessionFacts{Name: sum.Session.Name, Minutes: int(math.Round(float64(sum.DurationS) / 60)),
		VolumeKg: int(math.Round(sum.VolumeKg)), SetsDone: len(sum.Sets)}
	if sum.PeakBPM > 0 {
		f.PeakHR = &sum.PeakBPM
	}
	for _, e := range sum.Session.Plan {
		f.SetsPlan += len(e.Sets)
	}
	byEx := map[int]*coach.ExerciseFact{}
	var order []int
	for _, s := range sum.Sets {
		ex := byEx[s.ExerciseIndex]
		if ex == nil {
			ex = &coach.ExerciseFact{Name: s.ExerciseName}
			if s.ExerciseIndex < len(sum.Session.Plan) {
				ex.Change = sum.Session.Plan[s.ExerciseIndex].Change
			}
			byEx[s.ExerciseIndex] = ex
			order = append(order, s.ExerciseIndex)
		}
		sf := coach.SetFact{Reps: s.Reps, TargetReps: s.TargetReps, Kg: s.WeightKg}
		if st := stats[s.ID]; st.HasHR {
			sf.PeakHR, sf.DropHR = &st.PeakBPM, &st.RecoveryDrop
			if st.RestS > 0 {
				sf.RestS = &st.RestS
			}
		}
		if e, ok := sum.Efforts[s.ID]; ok {
			sf.Effort = e.Level
		}
		ex.Sets = append(ex.Sets, sf)
	}
	sort.Ints(order)
	for _, i := range order {
		f.Exercises = append(f.Exercises, *byEx[i])
	}
	// Heart rate on its way: write once it's here, not twice.
	if sum.HRStatus == "pending" {
		sum.Note = &strength.AINote{Pending: true}
		return
	}
	n, ok, pending := a.Coach.SessionNote(ctx, sum.Session.ID, f)
	if ok {
		sum.Note = &strength.AINote{Text: n.Text}
	} else if pending {
		sum.Note = &strength.AINote{Pending: true}
	}
}

// ---- The week ------------------------------------------------------------------

type weekNoteResp struct {
	Week string           `json:"week"` // the Monday
	AI   bool             `json:"ai"`   // models are available at all
	Note *strength.AINote `json:"note"`
}

// weekNote is the LLM's note on a finished Monday-to-Sunday week: ?week=
// names its Monday, defaulting to last week.
func (a *API) weekNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc := a.loc()
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	thisMon := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	mon := thisMon.AddDate(0, 0, -7)
	if q := r.URL.Query().Get("week"); q != "" {
		t, err := time.ParseInLocation("2006-01-02", q, loc)
		if err != nil || t.Weekday() != time.Monday {
			writeErr(w, http.StatusBadRequest, "week must be a Monday, YYYY-MM-DD")
			return
		}
		if !t.Before(thisMon) {
			writeErr(w, http.StatusBadRequest, "that week isn't finished yet")
			return
		}
		mon = t
	}
	resp := weekNoteResp{Week: mon.Format("2006-01-02"), AI: a.aiOn(r)}
	if !resp.AI {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	f, err := a.weekFacts(ctx, mon)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if f.Hours == 0 && f.Sessions == 0 {
		writeJSON(w, http.StatusOK, resp) // nothing happened; nothing to say
		return
	}
	n, ok, pending := a.Coach.WeekNote(ctx, f)
	if ok {
		resp.Note = &strength.AINote{Text: n.Text}
	} else if pending {
		resp.Note = &strength.AINote{Pending: true}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) weekFacts(ctx context.Context, mon time.Time) (coach.WeekFacts, error) {
	loc := a.loc()
	sun := mon.AddDate(0, 0, 6)
	end := mon.AddDate(0, 0, 7)
	f := coach.WeekFacts{Week: mon.Format("2006-01-02")}
	series, err := load.Daily(ctx, a.Store, mon.AddDate(0, 0, -7), sun, loc)
	if err != nil {
		return f, err
	}
	var prev, cur []load.Day
	for _, d := range series {
		if d.Date < f.Week {
			prev = append(prev, d)
		} else {
			cur = append(cur, d)
		}
	}
	if len(prev) == 7 {
		t := 0.0
		for _, d := range prev {
			t += d.TSS
		}
		f.PrevTSS = ip(t)
	}
	var tss, ride, str, hours float64
	names := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	for i, d := range cur {
		tss, ride, str, hours = tss+d.TSS, ride+d.RideTSS, str+d.StrengthTSS, hours+d.Hours
		if i < 7 {
			f.Days = append(f.Days, coach.WeekDay{Day: names[i], TSS: int(math.Round(d.TSS))})
		}
	}
	f.TSS, f.RideTSS, f.StrengthTSS = int(math.Round(tss)), int(math.Round(ride)), int(math.Round(str))
	f.Hours = math.Round(hours*10) / 10
	if len(prev) > 0 {
		f.FitnessFrom = int(math.Round(prev[len(prev)-1].CTL))
	}
	if len(cur) > 0 {
		f.FitnessTo, f.FormEnd = int(math.Round(cur[len(cur)-1].CTL)), int(math.Round(cur[len(cur)-1].TSB))
	}

	acts, err := a.Store.ListActivities(ctx, store.ActivityQuery{From: mon.UnixMilli(), To: end.UnixMilli() - 1})
	if err != nil {
		return f, err
	}
	sort.Slice(acts, func(i, j int) bool { return acts[i].StartAt < acts[j].StartAt })
	var ids []string
	for _, x := range acts {
		i := int(time.UnixMilli(x.StartAt).In(loc).Sub(mon).Hours() / 24)
		if i >= 0 && i < len(f.Days) {
			f.Days[i].What = append(f.Days[i].What, fmt.Sprintf("%s (%d min)", x.Name, (max(x.MovingS, 0)+59)/60))
		}
		ids = append(ids, x.ID)
	}
	a.rideMix(ctx, acts, &f)
	srcs, err := a.Store.SourcesForMany(ctx, ids)
	if err != nil {
		return f, err
	}
	for _, x := range acts {
		for _, src := range srcs[x.ID] {
			if t, ok := sweep.SourceTags(ctx, a.Coach, src); ok && t.Note != "" {
				f.Notes = append(f.Notes, t.Note)
			}
		}
	}

	sess, err := a.Store.SessionsBetween(ctx, mon.UnixMilli(), end.UnixMilli()-1)
	if err != nil {
		return f, err
	}
	for _, s := range sess {
		if s.FinishedAt != nil {
			f.Sessions++
		}
	}
	for d := mon; d.Before(end); d = d.AddDate(0, 0, 1) {
		if _, err := a.Store.GetPlan(ctx, d.Format("2006-01-02")); err == nil {
			f.Planned++
		} else if !errors.Is(err, store.ErrNotFound) {
			return f, err
		}
	}
	from, to := f.Week, sun.Format("2006-01-02")
	if recs, err := a.Store.RecoveryRange(ctx, from, to); err == nil && len(recs) > 0 {
		t := 0
		for _, x := range recs {
			t += x.Score
		}
		f.Recovery = ip(float64(t) / float64(len(recs)))
	}
	if sl, err := a.Store.SleepRange(ctx, from, to); err == nil && len(sl) > 0 {
		t := 0
		for _, x := range sl {
			t += x.AsleepS
		}
		f.SleepH = r1p(float64(t) / float64(len(sl)) / 3600)
	}
	return f, nil
}

// ---- A workout written as text -------------------------------------------------

// parseTemplate turns "5x5 back squat at 100, 3x8 RDL 80" into a draft
// template for the editor. Nothing is saved: the athlete reviews it first.
func (a *API) parseTemplate(w http.ResponseWriter, r *http.Request) {
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
	name, exs, err := a.Coach.ParseWorkout(ctx, in.Text)
	if err != nil {
		if coach.Unavailable(err) || ctx.Err() != nil {
			writeErr(w, http.StatusServiceUnavailable, "the AI model isn't available right now")
			return
		}
		a.Log.Warn("parse workout", "err", err.Error())
		writeErr(w, http.StatusUnprocessableEntity, "couldn't read a workout from that text")
		return
	}
	t := store.Template{ID: "draft", Name: name}
	if t.Name == "" {
		t.Name = "Workout"
	}
	for _, e := range exs {
		ex := store.Exercise{Name: e.Name, RestSeconds: e.RestSeconds}
		for _, s := range e.Sets {
			ex.Sets = append(ex.Sets, store.TargetSet{Reps: s.Reps, WeightKg: s.WeightKg})
		}
		t.Exercises = append(t.Exercises, ex)
	}
	t, err = store.CleanTemplate(t)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "the model's reading didn't make a valid workout: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": t.Name, "exercises": t.Exercises})
}

// ---- Stray entries -------------------------------------------------------------

// keepActivity overrules System 1's verdict that an activity is a stray
// entry: it counts towards load again, and isn't asked about again.
func (a *API) keepActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.Store.GetActivity(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such activity")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.Store.SetSyncState(r.Context(), load.StrayPrefix+id, "kept"); err != nil {
		a.fail(w, r, err)
		return
	}
	a.kickRecalc()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "stray": false})
}
