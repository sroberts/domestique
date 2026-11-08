package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"domestique/internal/activity"
	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/store"
	"domestique/internal/strength"
)

func (a *API) registerActivities(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/activities", a.listActivities)
	mux.HandleFunc("GET /v1/activities/{id}", a.getActivity)
	mux.HandleFunc("GET /v1/load", a.getLoad)
	mux.HandleFunc("GET /v1/trends", a.getTrends)
	mux.HandleFunc("GET /v1/athlete", a.getAthlete)
	mux.HandleFunc("PUT /v1/athlete", a.putAthlete)
	mux.HandleFunc("POST /v1/hr", a.postHR)
	mux.HandleFunc("GET /v1/recovery", a.getRecovery)
}

func (a *API) getRecovery(w http.ResponseWriter, r *http.Request) {
	days := 14
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 365 {
		days = v
	}
	now := time.Now().In(a.loc())
	from, to := now.AddDate(0, 0, -days+1).Format("2006-01-02"), now.Format("2006-01-02")
	rec, err := a.Store.RecoveryRange(r.Context(), from, to)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	sl, err := a.Store.SleepRange(r.Context(), from, to)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery": rec, "sleep": sl})
}

// Badge is a compact description of one source, for list rows.
type Badge struct {
	Source   string `json:"source"`
	HasPower bool   `json:"has_power"`
	HasHR    bool   `json:"has_hr"`
	HasGPS   bool   `json:"has_gps"`
}

// ActivityRow is an activity as lists show it.
type ActivityRow struct {
	store.Activity
	Sources []Badge    `json:"sources"`
	Summary rowMetrics `json:"summary"`
	// Progress is set for sessions logged in the app.
	Progress *strength.Progress `json:"progress,omitempty"`
}

type rowMetrics struct {
	AvgPower int     `json:"avg_power"`
	NP       int     `json:"np"`
	AvgHR    int     `json:"avg_hr"`
	MaxHR    int     `json:"max_hr"`
	AscentM  float64 `json:"ascent_m"`
	KJ       float64 `json:"kj"`
}

func (a *API) rows(r *http.Request, acts []store.Activity) ([]ActivityRow, error) {
	ids := make([]string, len(acts))
	for i, x := range acts {
		ids[i] = x.ID
	}
	srcs, err := a.Store.SourcesForMany(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	out := make([]ActivityRow, len(acts))
	for i, x := range acts {
		row := ActivityRow{Activity: x, Sources: []Badge{}}
		_ = json.Unmarshal(x.Metrics, &row.Summary)
		seen := map[string]bool{}
		for _, s := range srcs[x.ID] {
			if seen[s.Source] {
				continue
			}
			seen[s.Source] = true
			row.Sources = append(row.Sources, Badge{s.Source, s.HasPower, s.HasHR, s.HasGPS})
		}
		row.Metrics = nil // lists don't need curves and histograms
		if x.SessionID != nil {
			sess, err := a.Store.GetSession(r.Context(), *x.SessionID)
			if err == nil {
				sets, err := a.Store.SessionSets(r.Context(), sess.ID)
				if err != nil {
					return nil, err
				}
				p := strength.ProgressOf(sess.Plan, sets)
				row.Progress = &p
			} else if !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		}
		out[i] = row
	}
	return out, nil
}

func (a *API) listActivities(w http.ResponseWriter, r *http.Request) {
	q := store.ActivityQuery{Limit: 40}
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		q.Limit = v
	}
	if v, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil {
		q.Before = v
	}
	if v, err := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64); err == nil {
		q.From = v
	}
	switch r.URL.Query().Get("sport") {
	case "ride":
		q.Sports = []string{activity.SportRide, activity.SportVirtualRide}
	case "strength":
		q.Sports = []string{activity.SportStrength}
	case "other":
		q.Sports = []string{activity.SportRun, activity.SportWalk, activity.SportHike, activity.SportSwim, activity.SportOther}
	}
	acts, err := a.Store.ListActivities(r.Context(), q)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rows, err := a.rows(r, acts)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var next int64
	if len(acts) == q.Limit {
		next = acts[len(acts)-1].StartAt
	}
	writeJSON(w, http.StatusOK, map[string]any{"activities": rows, "next_before": next})
}

// ---- Detail -----------------------------------------------------------------------

// ChartStream is the merged stream reduced for drawing.
type ChartStream struct {
	T        []int32      `json:"t"`
	Power    []int32      `json:"power,omitempty"`
	HR       []int32      `json:"hr,omitempty"`
	Cadence  []int32      `json:"cadence,omitempty"`
	Speed    []float32    `json:"speed,omitempty"` // km/h
	Altitude []float32    `json:"altitude,omitempty"`
	Distance []float32    `json:"distance,omitempty"` // km
	Route    [][2]float64 `json:"route,omitempty"`    // [lat, lng]
}

type detailResp struct {
	Activity   store.Activity    `json:"activity"`
	Metrics    activity.Metrics  `json:"metrics"`
	Sources    []store.Source    `json:"sources"`
	Channels   map[string]string `json:"channels"`
	Stream     ChartStream       `json:"stream"`
	PowerZones [7]int            `json:"power_zones"`
	HRZones    [6]int            `json:"hr_zones"`
	// HRZonesFrom is "whoop" when there's no heart rate stream and the zone
	// times are Whoop's own summary instead.
	HRZonesFrom string `json:"hr_zones_from,omitempty"`
	MaxHR       int    `json:"max_hr"`
	// HRZoneBounds are the lower edges of zones 1-5 in bpm, for the zones
	// shown: shares of max HR, or Whoop's shares of heart rate reserve.
	HRZoneBounds [5]int            `json:"hr_zone_bounds"`
	Curve        []int             `json:"curve_durations"`
	BestCurve    []int             `json:"best_curve"` // all-time best at each duration, before this ride
	Strength     *strength.Summary `json:"strength,omitempty"`
	// Versions is each recording's own numbers, when there's more than one.
	Versions []versionResp `json:"versions,omitempty"`
	// View is the source shown alone (?source=), or "" for the merged view.
	View string `json:"view,omitempty"`

	// From the operator's models (internal/coach, internal/sweep), merged view
	// only: a note on the workout, tags and a fact read from the athlete's
	// own descriptions, and whether System 1 judged it a stray entry that
	// carries no load.
	Note    *strength.AINote `json:"note,omitempty"`
	Tags    []string         `json:"tags,omitempty"`
	TagNote string           `json:"tag_note,omitempty"`
	Stray   bool             `json:"stray,omitempty"`
	// BadData is channels System 1 judged broken and left out ("hr",
	// "power"), each with a few words on why; RideType is the ride's type.
	BadData  map[string]string `json:"bad_data,omitempty"`
	RideType string            `json:"ride_type,omitempty"`
	// NameSuggestion is the LLM's name for an activity still called by its
	// default ("Afternoon Ride"); see names.go.
	NameSuggestion *strength.AINote `json:"name_suggestion,omitempty"`
}

// versionResp is one source's own account of a workout, before merging.
type versionResp struct {
	ID         string   `json:"id"`
	Source     string   `json:"source"`
	Device     string   `json:"device"`
	File       bool     `json:"file"` // came from a file rather than an API
	StartAt    int64    `json:"start_at"`
	ElapsedS   int      `json:"elapsed_s"`
	MovingS    int      `json:"moving_s"`
	DistanceM  float64  `json:"distance_m"`
	AvgHR      int      `json:"avg_hr"`
	MaxHR      int      `json:"max_hr"`
	AvgPower   int      `json:"avg_power"`
	NP         int      `json:"np"`
	AvgCadence int      `json:"avg_cadence"`
	Strain     *float64 `json:"strain"`
	TSS        float64  `json:"tss"`
	LoadMethod string   `json:"load_method"`
	HasPower   bool     `json:"has_power"`
	HasHR      bool     `json:"has_hr"`
	HasGPS     bool     `json:"has_gps"`
	Sets       int      `json:"sets,omitempty"`
	VolumeKg   float64  `json:"volume_kg,omitempty"`
}

// sourceExtra is what a source's summary says about itself, for sources
// (Whoop's API) that have no stream to compute from.
type sourceExtra struct {
	Strain *float64 `json:"strain"`
	AvgHR  int      `json:"avg_hr"`
	MaxHR  int      `json:"max_hr"`
}

// asVersion is act as one source alone recorded it: that source's timing,
// distance, strain and metrics, and the load those would score.
func asVersion(act store.Activity, v ingest.Version, ath load.Athlete) (store.Activity, activity.Metrics) {
	src, m := v.Source, v.Metrics
	var ex sourceExtra
	_ = json.Unmarshal(src.Extra, &ex)
	if m.AvgHR == 0 {
		m.AvgHR, m.MaxHR = ex.AvgHR, ex.MaxHR
	}
	act.StartAt, act.ElapsedS, act.MovingS, act.DistanceM = src.StartAt, src.ElapsedS, src.MovingS, src.DistanceM
	if act.MovingS <= 0 {
		act.MovingS = src.ElapsedS
	}
	act.Strain = ex.Strain
	scored := m
	if len(scored.HRHist) == 0 {
		if z, ok := store.WhoopZones([]store.Source{src}); ok {
			scored.HRHist = activity.ZoneHRHist(z, ath.MaxHR, ath.RestHR)
		}
	}
	act.TSS, act.Intensity, act.LoadMethod = load.Score(act, scored, ath)
	return act, m
}

func (a *API) getActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	act, err := a.Store.GetActivity(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such activity")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	only := r.URL.Query().Get("source")
	m, vs, err := a.Ingest.Versions(ctx, act.ID, only)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no such version of this activity")
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	ath := load.Current(ctx, a.Store, a.MaxHR)
	srcs := make([]store.Source, len(vs))
	for i, v := range vs {
		srcs[i] = v.Source
	}
	var sum *strength.Summary
	if act.SessionID != nil {
		if s, err := a.strengthSummary(r, *act.SessionID); err == nil {
			sum = s
		}
	}

	resp := detailResp{Activity: act, Sources: srcs, Channels: m.Channels, MaxHR: ath.MaxHR,
		Curve: activity.CurveDurations, View: only}
	_ = json.Unmarshal(act.Metrics, &resp.Metrics)
	shown := srcs // the sources behind what's on screen
	for _, v := range vs {
		va, vm := asVersion(act, v, ath)
		if len(vs) > 1 {
			vr := versionResp{ID: v.Source.ID, Source: v.Source.Source, Device: v.Source.Device,
				File: v.Source.RawSHA256 != nil, StartAt: va.StartAt, ElapsedS: va.ElapsedS, MovingS: va.MovingS,
				DistanceM: va.DistanceM, AvgHR: vm.AvgHR, MaxHR: vm.MaxHR, AvgPower: vm.AvgPower, NP: vm.NP,
				AvgCadence: vm.AvgCadence, Strain: va.Strain, TSS: va.TSS, LoadMethod: va.LoadMethod,
				HasPower: v.Source.HasPower, HasHR: v.Source.HasHR, HasGPS: v.Source.HasGPS}
			if v.Source.Source == "app" && sum != nil {
				vr.Sets, vr.VolumeKg = len(sum.Sets), sum.VolumeKg
			}
			resp.Versions = append(resp.Versions, vr)
		}
		if v.Source.ID == only {
			resp.Activity, resp.Metrics = va, vm
			shown = []store.Source{v.Source}
		}
	}
	resp.Activity.Metrics = nil
	resp.Stream = downsample(&m.Stream, 1200)
	resp.PowerZones = activity.PowerZoneSeconds(&m.Stream, act.FTP)
	resp.HRZones = activity.HRZoneSeconds(resp.Metrics.HRHist, ath.MaxHR)
	resp.Metrics.HRHist = nil
	for i, p := range []float64{0.5, 0.6, 0.7, 0.8, 0.9} {
		resp.HRZoneBounds[i] = int(math.Round(p * float64(ath.MaxHR)))
	}
	if resp.HRZones == ([6]int{}) {
		if z, ok := store.WhoopZones(shown); ok {
			resp.HRZones, resp.HRZonesFrom = z, "whoop"
			resp.HRZoneBounds = activity.WhoopZoneBounds(ath.MaxHR, ath.RestHR)
		}
	}
	if activity.IsRide(act.Sport) && m.Stream.HasPower() {
		best, err := a.bestCurve(r, 0, act.StartAt, act.ID)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		resp.BestCurve = make([]int, len(best))
		for i, b := range best {
			resp.BestCurve[i] = b.Watts
		}
	}
	// The lifting itself belongs to the merged view and to Domestique's own.
	if sum != nil && (only == "" || shown[0].Source == "app") {
		resp.Strength = sum
	}
	if only == "" {
		a.aiDetail(r, &resp)
	}
	writeJSON(w, http.StatusOK, resp)
}

// downsample reduces a stream to at most n points by bucket means, and the
// GPS route by stride, so the phone draws a 5-hour ride without stalling.
func downsample(s *activity.Stream, n int) ChartStream {
	c := ChartStream{T: []int32{}} // always a list: the page reads its length
	total := s.Len()
	if total == 0 {
		return c
	}
	step := int(math.Ceil(float64(total) / float64(n)))
	if step < 1 {
		step = 1
	}
	meanI := func(v []int32, lo, hi int) int32 {
		var sum, k int64
		for i := lo; i < hi; i++ {
			if v[i] >= 0 {
				sum += int64(v[i])
				k++
			}
		}
		if k == 0 {
			return -1
		}
		return int32(sum / k)
	}
	meanF := func(v []float32, lo, hi int) float32 {
		var sum float64
		for i := lo; i < hi; i++ {
			sum += float64(v[i])
		}
		return float32(sum / float64(hi-lo))
	}
	for lo := 0; lo < total; lo += step {
		hi := min(lo+step, total)
		c.T = append(c.T, s.T[lo])
		if s.Power != nil {
			c.Power = append(c.Power, meanI(s.Power, lo, hi))
		}
		if s.HR != nil {
			c.HR = append(c.HR, meanI(s.HR, lo, hi))
		}
		if s.Cadence != nil {
			c.Cadence = append(c.Cadence, meanI(s.Cadence, lo, hi))
		}
		if s.Speed != nil {
			c.Speed = append(c.Speed, float32(math.Round(float64(meanF(s.Speed, lo, hi))*36)/10))
		}
		if s.Altitude != nil {
			c.Altitude = append(c.Altitude, float32(math.Round(float64(meanF(s.Altitude, lo, hi))*10)/10))
		}
		if s.Distance != nil {
			c.Distance = append(c.Distance, float32(math.Round(float64(s.Distance[hi-1])/10)/100))
		}
	}
	if s.HasGPS() {
		stride := max(1, total/2500)
		for i := 0; i < total; i += stride {
			if s.Lat[i] != 0 || s.Lng[i] != 0 {
				c.Route = append(c.Route, [2]float64{math.Round(s.Lat[i]*1e5) / 1e5, math.Round(s.Lng[i]*1e5) / 1e5})
			}
		}
	}
	return c
}

// ---- Power curve ---------------------------------------------------------------------

type curvePoint struct {
	Duration   int    `json:"duration"`
	Watts      int    `json:"watts"`
	ActivityID string `json:"activity_id,omitempty"`
	At         int64  `json:"at,omitempty"`
}

// bestCurve is the mean-maximal power across rides in [from, to), excluding one.
func (a *API) bestCurve(r *http.Request, from, to int64, exclude string) ([]curvePoint, error) {
	acts, err := a.Store.ListActivities(r.Context(), store.ActivityQuery{From: from,
		Sports: []string{activity.SportRide, activity.SportVirtualRide}})
	if err != nil {
		return nil, err
	}
	out := make([]curvePoint, len(activity.CurveDurations))
	for i, d := range activity.CurveDurations {
		out[i].Duration = d
	}
	for _, x := range acts {
		if x.ID == exclude || (to > 0 && x.StartAt >= to) {
			continue
		}
		var m struct {
			Curve []int `json:"power_curve"`
		}
		_ = json.Unmarshal(x.Metrics, &m)
		for i, wv := range m.Curve {
			if i < len(out) && wv > out[i].Watts {
				out[i] = curvePoint{Duration: out[i].Duration, Watts: wv, ActivityID: x.ID, At: x.StartAt}
			}
		}
	}
	return out, nil
}

// ---- Load and trends -------------------------------------------------------------------

func (a *API) getLoad(w http.ResponseWriter, r *http.Request) {
	days := 90
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 3650 {
		days = v
	}
	now := time.Now().In(a.loc())
	series, err := load.Daily(r.Context(), a.Store, now.AddDate(0, 0, -days+1), now, a.loc())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": series, "athlete": load.Current(r.Context(), a.Store, a.MaxHR)})
}

type week struct {
	Start       string  `json:"start"` // Monday
	RideTSS     float64 `json:"ride_tss"`
	StrengthTSS float64 `json:"strength_tss"`
	OtherTSS    float64 `json:"other_tss"`
	Hours       float64 `json:"hours"`
	RideKm      float64 `json:"ride_km"`
	Count       int     `json:"count"`
}

type liftPR struct {
	Exercise   string       `json:"exercise"`
	BestE1RM   float64      `json:"best_e1rm"`
	BestSet    string       `json:"best_set"`
	BestAt     int64        `json:"best_at"`
	LatestE1RM float64      `json:"latest_e1rm"`
	LatestAt   int64        `json:"latest_at"`
	Sessions   int          `json:"sessions"`
	History    [][2]float64 `json:"history"` // [epoch ms, best e1RM that session]
}

type ftpPoint struct {
	Month string `json:"month"`
	FTP   int    `json:"ftp"`
}

func (a *API) getTrends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc := a.loc()
	now := time.Now().In(loc)

	allTime, err := a.bestCurve(r, 0, 0, "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	recent, err := a.bestCurve(r, now.AddDate(0, 0, -90).UnixMilli(), 0, "")
	if err != nil {
		a.fail(w, r, err)
		return
	}

	// Weekly volume, last 26 weeks, Monday-based.
	monday := func(t time.Time) time.Time {
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
	}
	first := monday(now).AddDate(0, 0, -7*25)
	acts, err := a.Store.ListActivities(ctx, store.ActivityQuery{From: first.UnixMilli()})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	weeks := make([]week, 26)
	for i := range weeks {
		weeks[i].Start = first.AddDate(0, 0, 7*i).Format("2006-01-02")
	}
	for _, x := range acts {
		// Round to whole days: a week spanning a DST change is 167 or 169 hours.
		i := int(math.Round(monday(time.UnixMilli(x.StartAt).In(loc)).Sub(first).Hours()/24)) / 7
		if i < 0 || i >= len(weeks) {
			continue
		}
		wk := &weeks[i]
		wk.Count++
		wk.Hours += float64(x.MovingS) / 3600
		switch {
		case activity.IsRide(x.Sport):
			wk.RideTSS += x.TSS
			wk.RideKm += x.DistanceM / 1000
		case x.Sport == activity.SportStrength:
			wk.StrengthTSS += x.TSS
		default:
			wk.OtherTSS += x.TSS
		}
	}
	for i := range weeks {
		weeks[i].Hours = math.Round(weeks[i].Hours*10) / 10
		weeks[i].RideKm = math.Round(weeks[i].RideKm)
		weeks[i].RideTSS = math.Round(weeks[i].RideTSS)
		weeks[i].StrengthTSS = math.Round(weeks[i].StrengthTSS)
		weeks[i].OtherTSS = math.Round(weeks[i].OtherTSS)
	}

	// FTP history by month.
	all, err := a.Store.ListActivities(ctx, store.ActivityQuery{Sports: []string{activity.SportRide, activity.SportVirtualRide}})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	byMonth := map[string]int{}
	for _, x := range all {
		if x.FTP > 0 {
			k := time.UnixMilli(x.StartAt).In(loc).Format("2006-01")
			byMonth[k] = max(byMonth[k], x.FTP)
		}
	}
	ftp := []ftpPoint{}
	for k, v := range byMonth {
		ftp = append(ftp, ftpPoint{k, v})
	}
	sort.Slice(ftp, func(i, j int) bool { return ftp[i].Month < ftp[j].Month })

	// Lift PRs from logged sets (estimated one-rep max).
	sets, err := a.Store.AllSets(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	lifts := map[string]*liftPR{}
	lastSession := map[string]string{}
	for _, s := range sets {
		if s.Reps <= 0 || s.Reps > 12 || s.WeightKg <= 0 || !strength.Real(s) {
			continue // tapped-through sets aren't lifts, and mustn't become PRs
		}
		key := strings.ToLower(s.ExerciseName)
		p := lifts[key]
		if p == nil {
			p = &liftPR{Exercise: s.ExerciseName}
			lifts[key] = p
		}
		e := strength.E1RM(s.Reps, s.WeightKg)
		if lastSession[key] != s.SessionID {
			lastSession[key] = s.SessionID
			p.Sessions++
			p.History = append(p.History, [2]float64{float64(s.StartedAt), e})
			p.LatestE1RM, p.LatestAt = 0, s.StartedAt
		}
		h := &p.History[len(p.History)-1]
		h[1] = math.Max(h[1], e)
		p.LatestE1RM = math.Max(p.LatestE1RM, e)
		if e > p.BestE1RM {
			p.BestE1RM, p.BestAt = e, s.StartedAt
			p.BestSet = strconv.Itoa(s.Reps) + " × " + strconv.FormatFloat(s.WeightKg, 'f', -1, 64) + " kg"
		}
	}
	prs := []liftPR{}
	for _, p := range lifts {
		prs = append(prs, *p)
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].Sessions > prs[j].Sessions })

	writeJSON(w, http.StatusOK, map[string]any{
		"curve_all_time": allTime, "curve_90d": recent, "weeks": weeks, "ftp_history": ftp, "lifts": prs,
		"athlete": load.Current(ctx, a.Store, a.MaxHR),
	})
}

// ---- Athlete settings -----------------------------------------------------------------

func (a *API) getAthlete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := map[string]any{"current": load.Current(ctx, a.Store, a.MaxHR)}
	for _, k := range []string{"ftp_override", "ftp_detected", "max_hr_override", "max_hr_detected", "max_hr_whoop", "rest_hr_detected"} {
		if v, err := a.Store.Athlete(ctx, k); err == nil {
			n, _ := strconv.Atoi(v)
			resp[k] = n
		} else {
			resp[k] = nil
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) putAthlete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FTPOverride   *int `json:"ftp_override"`
		MaxHROverride *int `json:"max_hr_override"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	set := func(key string, v *int, lo, hi int) bool {
		if v == nil {
			return true
		}
		var err error
		if *v == 0 {
			err = a.Store.DeleteAthlete(ctx, key) // 0 clears the override: back to detection
		} else if *v < lo || *v > hi {
			writeErr(w, http.StatusBadRequest, key+" is out of range")
			return false
		} else {
			err = a.Store.SetAthlete(ctx, key, strconv.Itoa(*v))
		}
		if err != nil {
			a.fail(w, r, err) // never leave the request without a response
			return false
		}
		return true
	}
	if !set("ftp_override", body.FTPOverride, 80, 600) || !set("max_hr_override", body.MaxHROverride, 120, 230) {
		return
	}
	if _, err := load.Recalc(ctx, a.Store, a.MaxHR); err != nil {
		a.fail(w, r, err)
		return
	}
	a.getAthlete(w, r)
}

// ---- Heart rate from other sources -------------------------------------------------------

// postHR accepts heart rate samples from any source: an Apple Health
// shortcut (Whoop writes HR there), a chest strap export. Strength sessions
// overlapping the samples pick them up for per-set analysis.
func (a *API) postHR(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source  string `json:"source"`
		Samples []struct {
			T   json.RawMessage `json:"t"` // epoch ms, or an RFC 3339 / ISO 8601 string (Shortcuts)
			BPM float64         `json:"bpm"`
		} `json:"samples"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Source == "" {
		body.Source = "health"
	}
	samples := make([]store.HRSample, 0, len(body.Samples))
	var lo, hi int64
	for _, s := range body.Samples {
		ms, ok := parseSampleTime(s.T)
		if !ok {
			continue
		}
		if s.BPM < 25 || s.BPM > 240 {
			continue
		}
		samples = append(samples, store.HRSample{T: ms, BPM: int(math.Round(s.BPM))})
		if lo == 0 || ms < lo {
			lo = ms
		}
		hi = max(hi, ms)
	}
	if err := a.Store.PutHR(r.Context(), body.Source, samples); err != nil {
		a.fail(w, r, err)
		return
	}
	// Refresh strength sessions this heart rate covers.
	sessions, err := a.Store.RecentSessions(r.Context(), 50)
	if err == nil {
		for _, s := range sessions {
			if s.FinishedAt != nil && *s.FinishedAt >= lo && s.StartedAt <= hi {
				_ = a.Ingest.UpsertSession(r.Context(), s.ID)
			}
		}
		a.kickRecalc()
	}
	writeJSON(w, http.StatusOK, map[string]int{"accepted": len(samples)})
}

func parseSampleTime(raw json.RawMessage) (int64, bool) {
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		if n < 1e11 { // seconds, not milliseconds
			n *= 1000
		}
		return int64(n), true
	}
	var str string
	if json.Unmarshal(raw, &str) != nil {
		return 0, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05 -0700", "2 Jan 2006 at 15:04:05"} {
		if t, err := time.Parse(layout, str); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}
