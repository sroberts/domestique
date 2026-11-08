package coach

import (
	"context"
	"fmt"
)

// The System 1 model answers typed questions with probabilities in one fast
// pass. Domestique acts on an answer only past a confidence bar, and treats
// anything nearer the middle, or a refusal, as "don't know".

// Sure is the probability an answer must reach before Domestique changes
// data on it, for a question with no bar of its own in Bars.
const Sure = 0.85

// Bars are the per-question bars, where Sure doesn't fit. Each is set from
// `make eval` runs: high enough that no case in the eval set is acted on
// wrongly by the models tried.
var Bars = map[string]float64{
	// d1 put one walk on two devices at 0.85 and a brick at 0.27, Laya both
	// at about 0.7. A merge is hard to undo, so it needs more than Sure.
	"same": 0.9,
	// Laya called a typed-in yoga class and a recovery spin strays at 0.90
	// and 0.91; its true strays scored 0.92 to 1.00.
	"stray": 0.93,
	// d1 put one bad night, the morning after a 220 TSS day, and the second
	// day after a race at 0.85 to 0.91; its illness cases at 0.92 to 0.93.
	// Laya stayed between 0.56 and 0.73 on every case.
	"sick": 0.92,
	// Neither model can judge broken data: on clear cases (strap dropouts,
	// 230 bpm, zeros while pedalling) Laya gave 0.08 to 0.38 and d1 0.06 to
	// 0.63, and Laya put a trainer's steady ERG power at 0.90. Only channels
	// Go finds suspect are asked about (Suspect); this bar keeps a margin
	// over that 0.90 too.
	"bad_data": 0.92,
	// Laya leans "endurance" for nearly every ride, up to 0.56 when wrong;
	// d1 leaned to the right type on 8 of 11 cases, with tempo at 0.92,
	// intervals 0.77 and endurance 0.68. Display only, and in the week note.
	"ride_type": 0.65,
}

// Bar is the probability question q's answer must reach before Domestique
// acts on it.
func Bar(q string) float64 {
	if b, ok := Bars[q]; ok {
		return b
	}
	return Sure
}

// WorkoutFacts describes one recording or activity for System 1.
type WorkoutFacts struct {
	Name     string   `json:"name"`
	Source   string   `json:"source,omitempty"`
	Device   string   `json:"device,omitempty"`
	Start    string   `json:"local_start"` // "Tue 18:05"
	Minutes  int      `json:"minutes"`
	KM       *float64 `json:"distance_km,omitempty"`
	HasGPS   bool     `json:"has_gps"`
	HasPower bool     `json:"has_power"`
	HasHR    bool     `json:"has_heart_rate"`
	AvgHR    *int     `json:"avg_hr,omitempty"`
	AvgSpeed *float64 `json:"avg_speed_kmh,omitempty"`
	Cadence  *int     `json:"avg_cadence,omitempty"`
	Strain   *float64 `json:"whoop_strain,omitempty"`
	Notes    string   `json:"description,omitempty"`
	Manual   bool     `json:"entered_by_hand,omitempty"` // no file behind it
}

// Sports System 1 may pick between when a workout's own label says "other".
var sportCriteria = map[string]string{
	"ride":         "cycling outdoors on a road, gravel or mountain bike",
	"virtual_ride": "cycling indoors on a trainer, Zwift or a spin bike",
	"run":          "running or jogging",
	"walk":         "walking",
	"hike":         "hiking on trails",
	"swim":         "swimming",
	"strength":     "weight training, lifting, gym strength work",
	"other":        "anything else: rowing, yoga, sports, mobility, or not enough to tell",
}

// Sport asks which sport a workout labelled "other" was. ok is false unless
// the answer clears Bar("sport") and names a sport other than "other".
func (a *AI) Sport(ctx context.Context, f WorkoutFacts) (sport string, ok bool, err error) {
	s, p, err := a.SportGuess(ctx, f)
	if err != nil || s == "" || p < Bar("sport") {
		return "", false, err
	}
	return s, true, nil
}

// SportGuess is System 1's likeliest sport and its probability, with no
// bar: "" when it leans to "other", refuses, or answers out of shape.
func (a *AI) SportGuess(ctx context.Context, f WorkoutFacts) (string, float64, error) {
	ans, _, err := a.SystemOne(ctx, f, map[string]Question{
		"sport": {Type: "choice", Instructions: "Which sport was this workout?", Criteria: sportCriteria},
	})
	if err != nil {
		return "", 0, err
	}
	s := ans["sport"]
	if s.Type != "choice" || s.Choice == "other" {
		return "", 0, nil
	}
	if _, known := sportCriteria[s.Choice]; !known {
		return "", 0, fmt.Errorf("system 1 answered an unknown sport %q", s.Choice)
	}
	return s.Choice, s.Probabilities[s.Choice], nil
}

// Indoor asks whether a ride with no GPS was on a trainer. It returns the
// probability of yes, or -1 for "don't know".
func (a *AI) Indoor(ctx context.Context, f WorkoutFacts) (float64, error) {
	ans, _, err := a.SystemOne(ctx, f, map[string]Question{
		"indoor": {Type: "noul", Instructions: "Was this bike ride ridden indoors, on a trainer, smart bike or Zwift, rather than outside?"},
	})
	if err != nil {
		return -1, err
	}
	return yes(ans["indoor"]), nil
}

// SameWorkout asks whether two overlapping recordings are one workout. It
// returns the probability of yes, or -1 for "don't know".
func (a *AI) SameWorkout(ctx context.Context, x, y WorkoutFacts) (float64, error) {
	ans, _, err := a.SystemOne(ctx, map[string]any{"recording_a": x, "recording_b": y}, map[string]Question{
		"same": {Type: "noul", Instructions: "These two recordings overlap in time and belong to one athlete. " +
			"Are they the same workout recorded twice (by two devices or apps), rather than two different workouts back to back?"},
	})
	if err != nil {
		return -1, err
	}
	return yes(ans["same"]), nil
}

// Stray asks whether an imported entry is not a real workout: an accidental
// start, a test, a duplicate typed in by hand. It returns the probability of
// yes, or -1 for "don't know".
func (a *AI) Stray(ctx context.Context, f WorkoutFacts) (float64, error) {
	ans, _, err := a.SystemOne(ctx, f, map[string]Question{
		"stray": {Type: "noul", Instructions: "Is this entry not a real workout: an accidental or forgotten recording, " +
			"a test, a placeholder, or a duplicate typed in by hand? Answer no if it looks like a genuine session, however short or easy."},
	})
	if err != nil {
		return -1, err
	}
	return yes(ans["stray"]), nil
}

func yes(a Answer) float64 {
	if a.Type != "noul" {
		return -1
	}
	return a.Noul
}

// SetEffort is how hard one set looked, on Effort's levels.
type SetEffort struct {
	Level      string  `json:"level"` // one of EffortLevels
	Score      float64 `json:"score"` // 0..3
	Confidence float64 `json:"confidence"`
}

// EffortLevels is the scale a set is placed on, lowest first.
var EffortLevels = []string{"easy", "working", "hard", "limit"}

// SetState describes one set for System 1.
type SetState struct {
	Exercise   string  `json:"exercise"`
	Set        int     `json:"set_number"`
	Of         int     `json:"sets_of_exercise"`
	Reps       int     `json:"reps"`
	TargetReps int     `json:"target_reps"`
	Kg         float64 `json:"kg"`
	PrevKg     float64 `json:"previous_set_kg,omitempty"`
	PeakHR     int     `json:"peak_hr"`
	AvgHR      int     `json:"avg_hr_during"`
	Drop60     int     `json:"hr_drop_in_60s_after"`
	RestS      int     `json:"rest_after_s,omitempty"`
	MaxHR      int     `json:"athlete_max_hr"`
}

// EffortFloor is the confidence below which a set's effort isn't shown;
// an answer with no confidence isn't shown either. In the eval set, Laya
// and d1 both graded easy curls "hard" or "working" at up to 0.43, so with
// today's models effort mostly stays blank, which beats a wrong word.
const EffortFloor = 0.5

// SetEfforts places each set on EffortLevels, all in one request (at most
// 64, the smallest any provider takes). Sets System 1 isn't sure of are left
// out.
func (a *AI) SetEfforts(ctx context.Context, sets map[string]SetState) (map[string]SetEffort, error) {
	all, err := a.SetEffortsAll(ctx, sets)
	for id, e := range all {
		if e.Confidence < EffortFloor {
			delete(all, id)
		}
	}
	return all, err
}

// SetEffortsAll is SetEfforts with no confidence floor.
func (a *AI) SetEffortsAll(ctx context.Context, sets map[string]SetState) (map[string]SetEffort, error) {
	if len(sets) == 0 {
		return nil, nil
	}
	if len(sets) > 64 {
		return nil, fmt.Errorf("%d sets: at most 64 per request", len(sets))
	}
	qs := make(map[string]Question, len(sets))
	state := make(map[string]SetState, len(sets))
	for id, s := range sets {
		qs[id] = Question{Type: "score", Levels: EffortLevels, Instructions: fmt.Sprintf(
			"How hard was set %q for the athlete, judging by reps against target and how high heart rate rose and how slowly it fell?", id)}
		state[id] = s
	}
	ans, _, err := a.SystemOne(ctx, map[string]any{"sets": state}, qs)
	if err != nil {
		return nil, err
	}
	out := map[string]SetEffort{}
	for id, an := range ans {
		if _, asked := sets[id]; !asked || an.Type != "score" {
			continue
		}
		i := int(an.Score + 0.5)
		if i < 0 || i >= len(EffortLevels) {
			continue
		}
		out[id] = SetEffort{Level: EffortLevels[i], Score: an.Score, Confidence: an.Confidence}
	}
	return out, nil
}
