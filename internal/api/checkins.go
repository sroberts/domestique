package api

import (
	"cmp"
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"time"

	"domestique/internal/activity"
	"domestique/internal/checkin"
	"domestique/internal/store"
)

// The check-in after a workout: a few one-tap answers on how it went. A
// lifting session is asked on its summary; a ride or other imported workout
// on Today and its page. See internal/checkin.

// pendingWithin is how far back Today looks for a workout to ask about.
const pendingWithin = 48 * time.Hour

// minCheckInS is the shortest workout worth asking about.
const minCheckInS = 10 * 60

func (a *API) registerCheckIns(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/activities/{id}/checkin", a.getActivityCheckIn)
	mux.HandleFunc("PUT /v1/activities/{id}/checkin", a.putActivityCheckIn)
	mux.HandleFunc("GET /v1/sessions/{id}/checkin", a.getSessionCheckIn)
	mux.HandleFunc("PUT /v1/sessions/{id}/checkin", a.putSessionCheckIn)
}

// checkInResp is a workout's questions, and its check-in if there is one.
type checkInResp struct {
	Questions []string       `json:"questions"`
	CheckIn   *store.CheckIn `json:"checkin"`
	Minutes   int            `json:"minutes"`
	// SRPE is session RPE load (CR-10 effort × minutes), beside TSS and
	// strain; 0 until effort is answered.
	SRPE int `json:"srpe,omitempty"`
}

// checkInSubject is what a check-in is about: its key, when the workout
// started, how long it was, and which questions fit.
type checkInSubject struct {
	key     string
	at      int64
	minutes int
	w       checkin.Workout
}

func (a *API) activitySubject(ctx context.Context, act store.Activity) (checkInSubject, error) {
	secs := act.MovingS
	if secs <= 0 {
		secs = act.ElapsedS
	}
	s := checkInSubject{key: store.CheckInKey(act), at: act.StartAt, minutes: int(math.Round(float64(secs) / 60))}
	s.w = checkin.Workout{Session: act.SessionID != nil, Ride: activity.IsRide(act.Sport), Minutes: s.minutes}
	s.w.Planned = s.w.Session
	if s.w.Ride {
		date := time.UnixMilli(act.StartAt).In(a.loc()).Format("2006-01-02")
		p, err := a.Store.GetRidePlan(ctx, date)
		if err == nil {
			s.w.Planned = true
			s.w.Recovery = p.Intensity == "recovery"
		} else if !errors.Is(err, store.ErrNotFound) {
			return s, err
		}
		if act.Intensity > 0 && act.Intensity < 0.6 {
			s.w.Recovery = true
		}
	}
	return s, nil
}

func (a *API) sessionSubject(ctx context.Context, id string) (checkInSubject, error) {
	sess, err := a.Store.GetSession(ctx, id)
	if err != nil {
		return checkInSubject{}, err
	}
	end := sess.StartedAt
	if sess.FinishedAt != nil {
		end = *sess.FinishedAt
	}
	mins := int(math.Round(float64(end-sess.StartedAt) / 60000))
	return checkInSubject{key: store.SessionKey(id), at: sess.StartedAt, minutes: mins,
		w: checkin.Workout{Session: true, Planned: true, Minutes: mins}}, nil
}

func (a *API) checkInOf(ctx context.Context, s checkInSubject) (checkInResp, error) {
	resp := checkInResp{Questions: checkin.Questions(s.w), Minutes: s.minutes}
	c, err := a.Store.GetCheckIn(ctx, s.key)
	if errors.Is(err, store.ErrNotFound) {
		return resp, nil
	} else if err != nil {
		return resp, err
	}
	resp.CheckIn = &c
	resp.SRPE = checkin.SRPE(c, s.minutes)
	return resp, nil
}

func (a *API) getActivityCheckIn(w http.ResponseWriter, r *http.Request) {
	s, ok := a.activityCheckInSubject(w, r)
	if !ok {
		return
	}
	a.writeCheckIn(w, r, s)
}

func (a *API) putActivityCheckIn(w http.ResponseWriter, r *http.Request) {
	s, ok := a.activityCheckInSubject(w, r)
	if !ok {
		return
	}
	a.saveCheckIn(w, r, s)
}

func (a *API) getSessionCheckIn(w http.ResponseWriter, r *http.Request) {
	s, ok := a.sessionCheckInSubject(w, r)
	if !ok {
		return
	}
	a.writeCheckIn(w, r, s)
}

func (a *API) putSessionCheckIn(w http.ResponseWriter, r *http.Request) {
	s, ok := a.sessionCheckInSubject(w, r)
	if !ok {
		return
	}
	a.saveCheckIn(w, r, s)
}

func (a *API) activityCheckInSubject(w http.ResponseWriter, r *http.Request) (checkInSubject, bool) {
	act, err := a.Store.GetActivity(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such activity")
		return checkInSubject{}, false
	} else if err != nil {
		a.fail(w, r, err)
		return checkInSubject{}, false
	}
	s, err := a.activitySubject(r.Context(), act)
	if err != nil {
		a.fail(w, r, err)
		return s, false
	}
	return s, true
}

func (a *API) sessionCheckInSubject(w http.ResponseWriter, r *http.Request) (checkInSubject, bool) {
	id := r.PathValue("id")
	if into, err := a.Store.MergedInto(r.Context(), id); err == nil && into != "" {
		id = into // merged since: the check-in went with it
	}
	s, err := a.sessionSubject(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such session")
		return s, false
	} else if err != nil {
		a.fail(w, r, err)
		return s, false
	}
	return s, true
}

func (a *API) writeCheckIn(w http.ResponseWriter, r *http.Request, s checkInSubject) {
	resp, err := a.checkInOf(r.Context(), s)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// saveCheckIn replaces the workout's check-in with the body. {"skipped":
// true} records that the athlete passed, so nothing asks again.
func (a *API) saveCheckIn(w http.ResponseWriter, r *http.Request, s checkInSubject) {
	var c store.CheckIn
	if !readJSON(w, r, &c) {
		return
	}
	if msg := checkin.Check(&c); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	c.Key, c.WorkoutAt, c.UpdatedAt = s.key, s.at, time.Now().UnixMilli()
	if err := a.Store.PutCheckIn(r.Context(), c); err != nil {
		a.fail(w, r, err)
		return
	}
	a.writeCheckIn(w, r, s)
}

// pendingCheckIn is the one workout Today asks about.
type pendingCheckIn struct {
	Activity  ActivityRow `json:"activity"`
	Questions []string    `json:"questions"`
}

// pendingCheckIn finds the newest workout of the last two days with no
// check-in, answered or skipped. Never more than one.
func (a *API) pendingCheckIn(r *http.Request) (*pendingCheckIn, error) {
	ctx := r.Context()
	now := time.Now()
	acts, err := a.Store.ListActivities(ctx, store.ActivityQuery{From: now.Add(-pendingWithin).UnixMilli(), To: now.UnixMilli()})
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, x := range acts {
		keys = append(keys, store.CheckInKey(x))
	}
	have, err := a.Store.CheckInKeys(ctx, keys)
	if err != nil {
		return nil, err
	}
	for _, x := range acts { // newest first
		if have[store.CheckInKey(x)] || max(x.MovingS, x.ElapsedS) < minCheckInS {
			continue
		}
		s, err := a.activitySubject(ctx, x)
		if err != nil {
			return nil, err
		}
		rows, err := a.rows(r, []store.Activity{x})
		if err != nil {
			return nil, err
		}
		return &pendingCheckIn{Activity: rows[0], Questions: checkin.Questions(s.w)}, nil
	}
	return nil, nil
}

// feltLines is the check-ins on acts in a few words each, oldest first:
// "Tue, Zwift race: all out, legs dead".
func (a *API) feltLines(ctx context.Context, acts []store.Activity) ([]string, error) {
	keys := make([]string, len(acts))
	for i, x := range acts {
		keys[i] = store.CheckInKey(x)
	}
	have, err := a.Store.CheckInKeys(ctx, keys)
	if err != nil {
		return nil, err
	}
	sorted := append([]store.Activity(nil), acts...)
	slices.SortFunc(sorted, func(x, y store.Activity) int { return cmp.Compare(x.StartAt, y.StartAt) })
	var out []string
	for _, x := range sorted {
		k := store.CheckInKey(x)
		if !have[k] {
			continue
		}
		c, err := a.Store.GetCheckIn(ctx, k)
		if err != nil {
			return nil, err
		}
		if line := checkin.Line(c); line != "" {
			out = append(out, time.UnixMilli(x.StartAt).In(a.loc()).Format("Mon")+", "+x.Name+": "+line)
		}
	}
	return out, nil
}
