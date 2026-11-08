package coach

import (
	"context"
	"fmt"
)

// System 1's questions about the data itself: whether a recording's heart
// rate or power is broken, and what kind of ride it was.

// RecordingFacts describes one recording's streams, as plain numbers Go
// worked out, for System 1 to judge.
type RecordingFacts struct {
	Sport        string      `json:"sport"`
	Source       string      `json:"source"`
	Device       string      `json:"device,omitempty"`
	Minutes      int         `json:"minutes"`
	AthleteMaxHR int         `json:"athlete_max_hr"`
	FTP          int         `json:"athlete_ftp_w,omitempty"`
	HR           *HRFacts    `json:"heart_rate,omitempty"`
	Power        *PowerFacts `json:"power,omitempty"`
}

// HRFacts are a heart-rate channel's numbers.
type HRFacts struct {
	Coverage      float64 `json:"share_of_ride_with_reading"`       // 0..1
	Dropouts      int     `json:"dropouts_5s_plus"`                 // gaps or zeros mid-ride
	Avg           int     `json:"avg_bpm"`                          //
	Max           int     `json:"max_bpm"`                          //
	OverMaxS      int     `json:"seconds_over_athlete_max"`         // more than 5 bpm over
	LongestFlatS  int     `json:"longest_unchanged_s"`              // same reading, unbroken
	CadenceLocked float64 `json:"share_equal_to_cadence,omitempty"` // within 2 of cadence, while pedalling
	Jumps         int     `json:"jumps_25bpm_within_2s"`            //
	Spread        int     `json:"bpm_spread_p10_to_p90"`            //
	FirstReadingS int     `json:"first_reading_after_s,omitempty"`  // a strap still wetting
	Cadence       bool    `json:"cadence_recorded"`                 //
	Pedalling     float64 `json:"share_pedalling,omitempty"`        //
	Zeros         float64 `json:"share_of_readings_zero,omitempty"` //
}

// PowerFacts are a power channel's numbers.
type PowerFacts struct {
	Coverage         float64 `json:"share_of_ride_with_reading"`
	Dropouts         int     `json:"dropouts_5s_plus"`
	Avg              int     `json:"avg_w"`
	Max              int     `json:"max_w"`
	Over2000         int     `json:"readings_over_2000w"`
	ZeroPedalling    float64 `json:"share_zero_while_pedalling,omitempty"`
	LongestFlatS     int     `json:"longest_unchanged_nonzero_s"`
	LongestZeroPedal int     `json:"longest_zero_while_pedalling_s,omitempty"`
}

// Suspect keeps only the channels that look off enough to ask about. Most
// recordings are fine, and asking about them only invites a false alarm:
// in the eval set, Laya called a smart trainer's steady ERG power broken
// at 0.90.
func Suspect(f RecordingFacts) RecordingFacts {
	if h := f.HR; h != nil && !(h.Coverage < 0.85 || h.Dropouts >= 3 || h.OverMaxS > 0 || h.LongestFlatS >= 120 ||
		h.CadenceLocked >= 0.2 || h.Jumps >= 10 || h.Zeros >= 0.05) {
		f.HR = nil
	}
	if p := f.Power; p != nil && !(p.Coverage < 0.85 || p.Dropouts >= 3 || p.Over2000 > 0 || p.ZeroPedalling >= 0.1 ||
		p.LongestZeroPedal >= 30 || p.LongestFlatS >= 120) {
		f.Power = nil
	}
	return f
}

// BadData asks whether a recording's heart rate and power are broken, in
// one request. Each is the probability of yes, or -1 when that channel
// wasn't asked about (absent) or System 1 didn't answer it.
func (a *AI) BadData(ctx context.Context, f RecordingFacts) (hr, power float64, err error) {
	qs := map[string]Question{}
	if f.HR != nil {
		qs["hr_bad"] = Question{Type: "noul", Instructions: "Is this recording's heart-rate data broken, " +
			"from a strap losing contact, a sensor reading cadence instead of the heart, or impossible values? " +
			"Answer no for real heart rate, even when it swings hard in intervals or the ride is short."}
	}
	if f.Power != nil {
		qs["power_bad"] = Question{Type: "noul", Instructions: "Is this recording's power data broken, " +
			"from a meter dropping out, reading zero while the rider pedals, or impossible spikes? " +
			"Answer no for real power, even sprints or a smart trainer holding a steady target."}
	}
	if len(qs) == 0 {
		return -1, -1, nil
	}
	ans, _, err := a.SystemOne(ctx, f, qs)
	if err != nil {
		return -1, -1, err
	}
	hr, power = -1, -1
	if f.HR != nil {
		hr = yes(ans["hr_bad"])
	}
	if f.Power != nil {
		power = yes(ans["power_bad"])
	}
	return hr, power, nil
}

// RideFacts describe one ride for System 1 to classify.
type RideFacts struct {
	Name        string             `json:"name"`
	Start       string             `json:"local_start"` // "Tue 07:45"
	Minutes     int                `json:"minutes"`
	KM          *float64           `json:"distance_km,omitempty"`
	Indoor      bool               `json:"indoor"`
	Notes       string             `json:"description,omitempty"`
	IF          *float64           `json:"intensity_factor,omitempty"`
	VI          *float64           `json:"variability_index,omitempty"`
	PowerZones  map[string]float64 `json:"share_of_time_in_power_zones,omitempty"`
	HRZones     map[string]float64 `json:"share_of_time_in_hr_zones,omitempty"`
	AvgHRPct    *int               `json:"avg_hr_pct_of_max,omitempty"`
	HardEfforts *int               `json:"hard_efforts,omitempty"` // 30 s+ over threshold power, or 60 s+ over 90% max HR
	Stops       *int               `json:"stops_15s_plus,omitempty"`
}

// RideTypes is what a ride may be classified as.
var RideTypes = map[string]string{
	"endurance": "a steady aerobic ride: mostly zone 2, even pacing (variability index under 1.1), intensity factor about 0.6 to 0.75, usually an hour or more",
	"tempo":     "sustained moderately hard riding: much of it in zone 3 or 4 (tempo, sweet spot), intensity factor about 0.75 to 0.9, few hard spikes",
	"intervals": "a structured workout: several repeated hard efforts over threshold, with easier recoveries between them",
	"race":      "a race: very hard throughout, intensity factor 0.9 or more, uneven with many surges, often named as a race",
	"commute":   "riding to get somewhere: short, outdoors, on a weekday morning or evening, stop-start and easy",
	"recovery":  "a very easy short spin: almost all zone 1, intensity factor under 0.6, under about an hour",
}

// RideTypeGuess is System 1's likeliest ride type and its probability,
// with no bar: "" when it refuses or answers out of shape.
func (a *AI) RideTypeGuess(ctx context.Context, f RideFacts) (string, float64, error) {
	ans, _, err := a.SystemOne(ctx, f, map[string]Question{
		"ride_type": {Type: "choice", Instructions: "What kind of ride was this?", Criteria: RideTypes},
	})
	if err != nil {
		return "", 0, err
	}
	t := ans["ride_type"]
	if t.Type != "choice" {
		return "", 0, nil
	}
	if _, known := RideTypes[t.Choice]; !known {
		return "", 0, fmt.Errorf("system 1 answered an unknown ride type %q", t.Choice)
	}
	return t.Choice, t.Probabilities[t.Choice], nil
}
