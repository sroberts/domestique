package provider

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"domestique/internal/ingest"
	"domestique/internal/store"
)

// WhoopConfig is Whoop's OAuth application shape. `offline` is required for a
// refresh token.
var WhoopConfig = Config{
	Name:     "whoop",
	AuthURL:  "https://api.prod.whoop.com/oauth/oauth2/auth",
	TokenURL: "https://api.prod.whoop.com/oauth/oauth2/token",
	Scopes:   []string{"offline", "read:recovery", "read:cycles", "read:workout", "read:sleep", "read:profile", "read:body_measurement"},
}

const whoopAPI = "https://api.prod.whoop.com/developer"

// Whoop syncs recovery, sleep, workouts and max heart rate. The developer API
// has no heart rate time series: per-set heart rate needs another HR source
// (POST /v1/hr, e.g. from an Apple Health shortcut, or a chest strap).
type Whoop struct {
	C  *Client
	In *ingest.Ingester
	// Loc is the athlete's time zone, for assigning recoveries to days.
	Loc *time.Location
	// After runs when a webhook changed activities (training load recalc).
	After func(context.Context)
}

type whoopPage[T any] struct {
	Records   []T    `json:"records"`
	NextToken string `json:"next_token"`
}

type whoopRecovery struct {
	CycleID    int64     `json:"cycle_id"`
	CreatedAt  time.Time `json:"created_at"`
	ScoreState string    `json:"score_state"`
	Score      *struct {
		RecoveryScore    float64 `json:"recovery_score"`
		RestingHeartRate float64 `json:"resting_heart_rate"`
		HRVRmssdMilli    float64 `json:"hrv_rmssd_milli"`
	} `json:"score"`
}

type whoopSleep struct {
	ID         string    `json:"id"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Nap        bool      `json:"nap"`
	ScoreState string    `json:"score_state"`
	Score      *struct {
		StageSummary struct {
			Light int64 `json:"total_light_sleep_time_milli"`
			Deep  int64 `json:"total_slow_wave_sleep_time_milli"`
			REM   int64 `json:"total_rem_sleep_time_milli"`
		} `json:"stage_summary"`
		Performance *float64 `json:"sleep_performance_percentage"`
	} `json:"score"`
}

type whoopWorkout struct {
	ID         string    `json:"id"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	SportName  string    `json:"sport_name"`
	ScoreState string    `json:"score_state"`
	Score      *struct {
		Strain   float64 `json:"strain"`
		AvgHR    int     `json:"average_heart_rate"`
		MaxHR    int     `json:"max_heart_rate"`
		Distance float64 `json:"distance_meter"`
		Zones    struct {
			Z0 int64 `json:"zone_zero_milli"`
			Z1 int64 `json:"zone_one_milli"`
			Z2 int64 `json:"zone_two_milli"`
			Z3 int64 `json:"zone_three_milli"`
			Z4 int64 `json:"zone_four_milli"`
			Z5 int64 `json:"zone_five_milli"`
		} `json:"zone_durations"`
	} `json:"score"`
}

// whoopPages caps one collection walk; a walk that hits it is incomplete.
const whoopPages = 400

// errIncomplete means a collection had more pages than one walk reads.
var errIncomplete = errors.New("whoop: collection has more pages than one sync reads")

// collect pages through a Whoop collection since start. It returns
// errIncomplete, after handing over every record it read, if there were
// more pages than it reads in one go.
func collect[T any](ctx context.Context, c *Client, path string, start time.Time, each func(T) error) error {
	next := ""
	for page := 0; page < whoopPages; page++ {
		q := url.Values{"limit": {"25"}, "start": {start.UTC().Format(time.RFC3339)}}
		if next != "" {
			q.Set("nextToken", next)
		}
		var p whoopPage[T]
		if err := c.GetJSON(ctx, whoopAPI+path+"?"+q.Encode(), &p); err != nil {
			return err
		}
		for _, r := range p.Records {
			if err := each(r); err != nil {
				return err
			}
		}
		if p.NextToken == "" {
			return nil
		}
		next = p.NextToken
	}
	return errIncomplete
}

// since returns where to resume: two days before the last sync (scores are
// revised after the fact), or two years back on first sync.
func (w *Whoop) since(ctx context.Context, key string) time.Time {
	if v, err := w.C.Store.SyncState(ctx, "whoop:"+key); err == nil {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.UnixMilli(ms).Add(-48 * time.Hour)
		}
	}
	return time.Now().AddDate(-2, 0, 0)
}

func (w *Whoop) mark(ctx context.Context, key string) {
	_ = w.C.Store.SetSyncState(ctx, "whoop:"+key, strconv.FormatInt(time.Now().UnixMilli(), 10))
}

// Sync pulls everything new.
func (w *Whoop) Sync(ctx context.Context) error {
	st := w.C.Store
	if _, err := st.GetToken(ctx, "whoop"); errors.Is(err, store.ErrNotFound) {
		return ErrNotConnected
	}
	loc := w.Loc
	if loc == nil {
		loc = time.Local
	}

	var body struct {
		MaxHR int `json:"max_heart_rate"`
	}
	if err := w.C.GetJSON(ctx, whoopAPI+"/v2/user/measurement/body", &body); err == nil && body.MaxHR > 0 {
		_ = st.SetAthlete(ctx, "max_hr_whoop", strconv.Itoa(body.MaxHR))
	} else if err != nil && !strings.Contains(err.Error(), "404") {
		return err
	}

	if err := collect(ctx, w.C, "/v2/recovery", w.since(ctx, "recovery"), func(r whoopRecovery) error {
		if r.ScoreState != "SCORED" || r.Score == nil {
			return nil
		}
		return st.PutRecovery(ctx, store.Recovery{
			Date:      r.CreatedAt.In(loc).Format("2006-01-02"),
			Score:     int(r.Score.RecoveryScore + 0.5),
			HRVms:     float64(int(r.Score.HRVRmssdMilli*10+0.5)) / 10,
			RestingHR: int(r.Score.RestingHeartRate + 0.5),
			Source:    "whoop",
		})
	}); err != nil && !errors.Is(err, errIncomplete) {
		return err
	}
	w.mark(ctx, "recovery")

	sleepSince, sleepsSeen := w.since(ctx, "sleep"), map[string]bool{}
	sleepErr := collect(ctx, w.C, "/v2/activity/sleep", sleepSince, func(s whoopSleep) error {
		sleepsSeen["whoop:"+s.ID] = true
		if s.Nap || s.ScoreState != "SCORED" || s.Score == nil {
			return nil
		}
		ss := s.Score.StageSummary
		x := store.Sleep{ID: "whoop:" + s.ID, Date: s.End.In(loc).Format("2006-01-02"),
			StartAt: s.Start.UnixMilli(), EndAt: s.End.UnixMilli(), AsleepS: int((ss.Light + ss.Deep + ss.REM) / 1000), Source: "whoop"}
		if s.Score.Performance != nil {
			p := int(*s.Score.Performance + 0.5)
			x.Performance = &p
		}
		return st.PutSleep(ctx, x)
	})
	if sleepErr != nil && !errors.Is(sleepErr, errIncomplete) {
		return sleepErr
	}
	if sleepErr == nil {
		if err := w.reconcile(ctx, sleepSince, sleepsSeen, st.SleepIDsSince, func(id string) error {
			_, err := st.DeleteSleep(ctx, id)
			return err
		}); err != nil {
			return err
		}
	}
	w.mark(ctx, "sleep")

	workoutSince, workoutsSeen := w.since(ctx, "workout"), map[string]bool{}
	workoutErr := collect(ctx, w.C, "/v2/activity/workout", workoutSince, func(x whoopWorkout) error {
		workoutsSeen["whoop:"+x.ID] = true
		if x.ScoreState != "SCORED" || x.Score == nil {
			return nil
		}
		strain := x.Score.Strain
		h := ingest.Hint{Source: "whoop", ExternalID: x.ID, Sport: whoopSport(x.SportName), Strain: &strain}
		_, err := w.In.AddWhoopWorkout(ctx, x.ID, x.Start, int(x.End.Sub(x.Start).Seconds()), x.Score.Distance, h,
			x.Score.AvgHR, x.Score.MaxHR, [6]int64{x.Score.Zones.Z0, x.Score.Zones.Z1, x.Score.Zones.Z2,
				x.Score.Zones.Z3, x.Score.Zones.Z4, x.Score.Zones.Z5})
		return err
	})
	if workoutErr != nil && !errors.Is(workoutErr, errIncomplete) {
		return workoutErr
	}
	if workoutErr == nil {
		if err := w.reconcile(ctx, workoutSince, workoutsSeen, st.SourceIDsSince, func(id string) error {
			return w.In.RemoveSource(ctx, id)
		}); err != nil {
			return err
		}
	}
	w.mark(ctx, "workout")
	return nil
}

// reconcile removes what Whoop deleted: records stored from Whoop's API that
// started inside the window a complete sync just read, but that Whoop no
// longer returned. It's the backstop for a deletion webhook that was missed
// or failed. Whoop files from a Strava export have other ids and are never
// touched. An empty answer against several stored records looks like an
// outage, not a mass deletion, so it's left alone.
func (w *Whoop) reconcile(ctx context.Context, since time.Time, seen map[string]bool,
	stored func(context.Context, string, int64) ([]string, error), remove func(string) error) error {
	// A minute inside the window: a record right at its edge may or may not
	// have been returned.
	ids, err := stored(ctx, "whoop:", since.Add(time.Minute).UnixMilli())
	if err != nil {
		return err
	}
	var gone []string
	for _, id := range ids {
		if !seen[id] {
			gone = append(gone, id)
		}
	}
	if len(seen) == 0 && len(gone) > 1 {
		w.C.Log.Warn("whoop: returned nothing for a window with stored records; not deleting", "stored", len(gone))
		return nil
	}
	for _, id := range gone {
		if err := remove(id); err != nil {
			return err
		}
		w.C.Log.Info("whoop: removed what Whoop deleted", "id", id)
	}
	return nil
}

// whoopSport maps Whoop's sport names onto Strava-style types the importer knows.
func whoopSport(s string) string {
	switch strings.ToLower(s) {
	case "cycling", "mountain-biking":
		return "Ride"
	case "spin", "assault-bike":
		return "VirtualRide"
	case "running", "track-and-field":
		return "Run"
	case "walking":
		return "Walk"
	case "hiking/rucking", "hiking":
		return "Hike"
	case "swimming":
		return "Swim"
	case "weightlifting", "powerlifting", "functional-fitness", "strength-trainer", "hiit", "crossfit":
		return "WeightTraining"
	}
	return s
}
