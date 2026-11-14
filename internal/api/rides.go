package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"domestique/internal/activity"
	"domestique/internal/store"
	"domestique/internal/strength"
)

// A planned ride is a day's bike ride, decided ahead the way a lifting
// session is: a name, indoors or out, how long, and how hard. Today shows it
// beside the lift, ticks it off once a ride that day comes in, and on a
// hold-back morning says how to ride it easier.

var intensities = []string{"recovery", "endurance", "tempo", "threshold", "vo2"}

// sessionIF is a typical intensity factor for a whole ride of each kind,
// warm-up and recoveries included, for estimating TSS.
var sessionIF = map[string]float64{"recovery": 0.55, "endurance": 0.68, "tempo": 0.78, "threshold": 0.85, "vo2": 0.88}

const maxRidePlanDays = 62 // the longest list one request returns

func (a *API) registerRides(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/ride-plans", a.listRidePlans)
	mux.HandleFunc("GET /v1/ride-plans/{date}", a.getRidePlan)
	mux.HandleFunc("PUT /v1/ride-plans/{date}", a.putRidePlan)
	mux.HandleFunc("DELETE /v1/ride-plans/{date}", a.deleteRidePlan)
}

// listRidePlans lists planned rides from ?from= (default today) to ?to=
// (default two weeks on), soonest first.
func (a *API) listRidePlans(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if from == "" {
		from = time.Now().In(a.loc()).Format("2006-01-02")
	}
	if !validDate(from) || (to != "" && !validDate(to)) {
		writeErr(w, http.StatusBadRequest, "from and to must be YYYY-MM-DD")
		return
	}
	f, _ := time.Parse("2006-01-02", from)
	if to == "" {
		to = f.AddDate(0, 0, 13).Format("2006-01-02")
	}
	if t, _ := time.Parse("2006-01-02", to); t.Before(f) || t.Sub(f) > maxRidePlanDays*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "to must be on or after from, and at most 62 days on")
		return
	}
	ps, err := a.Store.RidePlansBetween(r.Context(), from, to)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (a *API) getRidePlan(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	p, err := a.Store.GetRidePlan(r.Context(), date)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no ride planned that day")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) putRidePlan(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	var p store.RidePlan
	if !readJSON(w, r, &p) {
		return
	}
	p.Date = date
	p.Name = strings.Join(strings.Fields(p.Name), " ")
	p.Notes = strings.TrimSpace(p.Notes)
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	p.Intensity = strings.ToLower(strings.TrimSpace(p.Intensity))
	if msg := checkRidePlan(p); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	p.UpdatedAt = time.Now().UnixMilli()
	if err := a.Store.PutRidePlan(r.Context(), p); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func checkRidePlan(p store.RidePlan) string {
	switch {
	case p.Name == "" || utf8.RuneCountInString(p.Name) > 80:
		return "name must be 1-80 characters"
	case p.Kind != "indoor" && p.Kind != "outdoor":
		return "kind must be indoor or outdoor"
	case p.DurationMin < 10 || p.DurationMin > 600:
		return "duration_min must be 10-600"
	case p.TSS < 0 || p.TSS > 600:
		return "tss must be 0-600 (0 for no target)"
	case p.Intensity != "" && sessionIF[p.Intensity] == 0:
		return "intensity must be one of " + strings.Join(intensities, ", ") + ", or empty"
	case utf8.RuneCountInString(p.Notes) > 1000:
		return "notes must be at most 1000 characters"
	}
	return ""
}

func (a *API) deleteRidePlan(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if !validDate(date) {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	if err := a.Store.DeleteRidePlan(r.Context(), date); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rideView is the day's planned ride on Today.
type rideView struct {
	store.RidePlan
	// Done is a ride recorded that day, the longest if there were several.
	Done *ActivityRow `json:"done,omitempty"`
	// Ease is how to ride it on a hold-back morning.
	Ease *rideEase `json:"ease,omitempty"`
}

type rideEase struct {
	Intensity   string `json:"intensity"`
	DurationMin int    `json:"duration_min"`
	TSS         int    `json:"tss,omitempty"` // when the plan had a target
	Reason      string `json:"reason"`
}

// todayRide fills in the ride planned for date, if any.
func (a *API) todayRide(r *http.Request, resp *todayResp, day time.Time) error {
	ctx := r.Context()
	p, err := a.Store.GetRidePlan(ctx, resp.Date)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	v := &rideView{RidePlan: p, Ease: easeRide(p, resp.Readiness.Level)}
	acts, err := a.Store.ListActivities(ctx, store.ActivityQuery{From: day.UnixMilli(), To: day.AddDate(0, 0, 1).UnixMilli() - 1,
		Sports: []string{activity.SportRide, activity.SportVirtualRide}})
	if err != nil {
		return err
	}
	if len(acts) > 0 {
		best := acts[0]
		for _, x := range acts[1:] {
			if x.ElapsedS > best.ElapsedS {
				best = x
			}
		}
		rows, err := a.rows(r, []store.Activity{best})
		if err != nil {
			return err
		}
		v.Done = &rows[0]
		v.Ease = nil
	}
	resp.Ride = v
	return nil
}

// easeRide is how to ride p on a morning whose readiness level is level, or
// nil when it can be ridden as planned. "easy" takes it down to endurance
// (or keeps it at recovery) and cuts a third of the time; "hold" takes the
// hardest rides down one step and leaves the time.
func easeRide(p store.RidePlan, level string) *rideEase {
	e := rideEase{Intensity: p.Intensity, DurationMin: p.DurationMin}
	switch level {
	case "easy":
		if p.Intensity != "recovery" {
			e.Intensity = "endurance"
		}
		if p.DurationMin > 45 {
			e.DurationMin = max(45, int(math.Round(float64(p.DurationMin)*2/3/5))*5)
		}
		if e.Intensity == p.Intensity && e.DurationMin == p.DurationMin {
			return nil
		}
		e.Reason = "Recovery says ease off: ride " + strconv.Itoa(e.DurationMin) + " min at " + e.Intensity + " pace instead, no efforts."
	case "hold":
		switch p.Intensity {
		case "vo2":
			e.Intensity = "threshold"
		case "threshold":
			e.Intensity = "tempo"
		default:
			return nil
		}
		e.Reason = "Hold back a little: ride it at " + e.Intensity + " rather than " + intensityWord(p.Intensity) + "."
	default:
		return nil
	}
	if p.TSS > 0 {
		from, to := estTSS(p.DurationMin, p.Intensity), estTSS(e.DurationMin, e.Intensity)
		e.TSS = int(math.Round(float64(p.TSS) * to / from))
	}
	return &e
}

func intensityWord(s string) string {
	if s == "vo2" {
		return "VO2 max"
	}
	return s
}

// estTSS estimates a ride's TSS from its length and kind; an unsaid
// intensity counts as endurance.
func estTSS(minutes int, intensity string) float64 {
	f := sessionIF[intensity]
	if f == 0 {
		f = sessionIF["endurance"]
	}
	return float64(minutes) / 60 * f * f * 100
}

// rideOnlyReadiness rewords the rules' call, written with lifting in mind,
// for a day with a ride planned and no lift.
func rideOnlyReadiness(r *strength.Readiness) {
	switch r.Level {
	case "hold":
		r.Title = "Ride, but hold back a little"
		r.Detail = strings.Replace(r.Detail, "Train as planned, but hold the weights rather than adding load.", "Ride as planned, with the hardest efforts a step easier.", 1)
	case "easy":
		r.Detail = strings.Replace(r.Detail, "Drop the last set of each lift and hold the weights, or swap the session for an easy spin in zone 1–2.", "Keep today's ride to an easy spin in zone 1–2.", 1)
		r.Detail = strings.Replace(r.Detail, "Keep today easy: hold the weights, no intervals.", "Keep today's ride easy, no intervals.", 1)
	}
}

// rideFact is the planned ride in a few words, for the coach.
func rideFact(p store.RidePlan) string {
	parts := []string{p.Name, p.Kind, strconv.Itoa(p.DurationMin) + " min"}
	if p.Intensity != "" {
		parts = append(parts, intensityWord(p.Intensity))
	}
	if p.TSS > 0 {
		parts = append(parts, strconv.Itoa(p.TSS)+" TSS")
	}
	return strings.Join(parts, ", ")
}
