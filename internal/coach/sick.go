package coach

import "context"

// SickFacts compares the last few mornings with the athlete's own baseline,
// for System 1 to say whether they look like the start of an illness rather
// than ordinary variation or a hard day's training. Deltas are worked out
// here, so the model needn't do arithmetic.
type SickFacts struct {
	BaselineDays int       `json:"baseline_days"`
	RHRBase      *int      `json:"resting_hr_baseline,omitempty"`
	HRVBase      *int      `json:"hrv_ms_baseline,omitempty"`
	RecoveryBase *int      `json:"recovery_pct_baseline,omitempty"`
	SleepBaseH   *float64  `json:"sleep_hours_baseline,omitempty"`
	Days         []SickDay `json:"last_days"` // oldest first, today last
	// Averages over Days, against the baseline.
	RHRDelta    *int `json:"resting_hr_change_bpm,omitempty"`
	HRVDeltaPct *int `json:"hrv_change_pct,omitempty"`
}

// SickDay is one morning, and the training load of the day before it.
type SickDay struct {
	Day          string   `json:"day"` // "today", "yesterday", "2 days ago"
	RestingHR    *int     `json:"resting_hr,omitempty"`
	HRVms        *int     `json:"hrv_ms,omitempty"`
	Recovery     *int     `json:"recovery_pct,omitempty"`
	SleepH       *float64 `json:"sleep_hours,omitempty"`
	TSSDayBefore *int     `json:"training_tss_day_before,omitempty"`
}

// Sick asks whether the athlete looks to be coming down with something. It
// returns the probability of yes, or -1 for "don't know".
func (a *AI) Sick(ctx context.Context, f SickFacts) (float64, error) {
	ans, _, err := a.SystemOne(ctx, f, map[string]Question{
		"sick": {Type: "noul", Instructions: "Do these mornings look like the athlete is coming down with an illness: " +
			"resting heart rate up and HRV down against their own baseline for several days running, not explained by hard training the day before? " +
			"Answer no for one bad night, ordinary day-to-day variation, or a dip that follows a big training day."},
	})
	if err != nil {
		return -1, err
	}
	return yes(ans["sick"]), nil
}
