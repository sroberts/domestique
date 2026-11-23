package strength

import (
	"fmt"
	"math"

	"domestique/internal/store"
)

// Baseline is the athlete's own norm: medians over the mornings before
// today. Whoop's score already weighs HRV, but against Whoop's model of the
// athlete; the gap from their own recent mornings is what tips a borderline
// score one way or the other.
type Baseline struct {
	Days      int     `json:"days"`
	HRVms     float64 `json:"hrv_ms"`
	RestingHR float64 `json:"resting_hr"`
	SleepH    float64 `json:"sleep_h"` // 0 with too few nights known
}

// BaselineDays is how many earlier mornings a baseline needs before it counts.
const BaselineDays = 14

// Morning is everything the day's call is decided from.
type Morning struct {
	Rec     *store.Recovery // nil before Whoop has scored the night
	SleepH  float64         // hours asleep last night; 0 when unknown
	Base    *Baseline       // nil with fewer than BaselineDays mornings
	TSB     float64         // today's form: fitness minus fatigue
	HasLoad bool            // enough history for form to mean something
}

// Gaps from the athlete's norm that count. Whoop's own HRV swings 10% from
// night to night, so smaller gaps are noise.
const (
	hrvLow    = -15 // % under the norm: strained
	rhrHigh   = 5   // bpm over the norm: strained
	shortS    = 5.0 // hours asleep: short for anyone
	shortGap  = 1.5 // hours under the athlete's own norm: short for them
	edge      = 5   // points from a band's edge that count as borderline
	pushScore = 80
	pushForm  = -15 // ordinary training sits at form -5 to -15; deeper is fatigue
)

// HRVPct is today's HRV against the norm, in percent; ok is false without both.
func (m Morning) HRVPct() (pct float64, ok bool) {
	if m.Rec == nil || m.Base == nil || m.Rec.HRVms <= 0 || m.Base.HRVms <= 0 {
		return 0, false
	}
	return (m.Rec.HRVms - m.Base.HRVms) / m.Base.HRVms * 100, true
}

// RHRDelta is today's resting heart rate against the norm, in bpm.
func (m Morning) RHRDelta() (bpm float64, ok bool) {
	if m.Rec == nil || m.Base == nil || m.Rec.RestingHR <= 0 || m.Base.RestingHR <= 0 {
		return 0, false
	}
	return float64(m.Rec.RestingHR) - m.Base.RestingHR, true
}

// strained names what is well off the norm this morning, or "".
func (m Morning) strained() string {
	if p, ok := m.HRVPct(); ok && math.Round(p) <= hrvLow {
		return fmt.Sprintf("HRV is %.0f%% under your norm", -p)
	}
	if d, ok := m.RHRDelta(); ok && math.Round(d) >= rhrHigh {
		return fmt.Sprintf("resting heart rate is %.0f above your norm", d)
	}
	return ""
}

// Short reports a short night: under 5 hours, or well under the athlete's
// own norm. Whoop's score already counts sleep; this only catches a night
// that is short for them, not one that's merely below eight hours.
func (m Morning) Short() bool {
	if m.SleepH <= 0 {
		return false
	}
	return m.SleepH < shortS || (m.Base != nil && m.Base.SleepH > 0 && m.SleepH < m.Base.SleepH-shortGap)
}

// short names a short night, or "".
func (m Morning) short() string {
	if m.Short() {
		return fmt.Sprintf("you slept %.1f h", m.SleepH)
	}
	return ""
}

// atNorm reports HRV at or above the norm with resting heart rate not up.
func (m Morning) atNorm() bool {
	p, ok1 := m.HRVPct()
	d, ok2 := m.RHRDelta()
	return ok1 && ok2 && math.Round(p) >= 0 && math.Round(d) <= 1
}

// AssessReadiness decides how hard today should be. Whoop's recovery score
// sets the band (under 34 ease off, under 67 hold the weights, else train);
// near a band's edge, the athlete's own HRV and resting heart rate, and a
// short night, move it one band. A high score with HRV at the norm is a day
// to push, unless fatigue is deep. Fatigue caps everything.
func AssessReadiness(m Morning) Readiness {
	score := -1
	if m.Rec != nil {
		score = m.Rec.Score
	}
	if score < 0 {
		switch {
		case m.HasLoad && m.TSB < -30:
			return fatigued(m.TSB)
		case m.HasLoad && m.TSB < -20:
			return call("hold", "Train, but hold the weights", fmt.Sprintf("No recovery score yet; form is %.0f. Train as planned, but hold the weights rather than adding load.", m.TSB))
		case m.HasLoad:
			return call("normal", "Train as planned", fmt.Sprintf("No recovery score yet; form is %+.0f.", m.TSB))
		}
		return call("normal", "Train as planned", "No recovery score yet.")
	}

	level := "normal"
	switch {
	case score < 34:
		level = "easy"
	case score < 67:
		level = "hold"
	}
	// Why the band moved, as a clause for the detail.
	var why string
	strained, short := m.strained(), m.short()
	switch {
	case level == "hold" && score < 34+edge && strained != "":
		level, why = "easy", strained
	case level == "normal" && score < 67+edge && (strained != "" || short != ""):
		level, why = "hold", strained
		if why == "" {
			why = short
		}
	case level == "hold" && score >= 67-edge && short == "" && m.atNorm():
		level, why = "normal", "HRV and resting heart rate are at your norm"
	}
	// A day raised to normal has done enough; push needs a green score.
	push := level == "normal" && why == "" && m.HasLoad && short == "" &&
		((score >= 67 && m.TSB > 5) || (score >= pushScore && m.TSB > pushForm && m.atNorm()))
	if push {
		level = "push"
	}

	// Fatigue caps the day whatever the morning says.
	if level != "easy" && m.HasLoad && m.TSB < -30 {
		return fatigued(m.TSB)
	}
	if (level == "normal" || level == "push") && m.HasLoad && m.TSB < -20 {
		return call("hold", "Train, but hold the weights", fmt.Sprintf("Recovery is %d%%, but form is %.0f. Train as planned, but hold the weights rather than adding load.", score, m.TSB))
	}

	lead := fmt.Sprintf("Recovery is %d%%", score)
	switch level {
	case "easy":
		if why != "" {
			lead += " and " + why
		}
		return call("easy", "Ease off today", lead+". Drop the last set of each lift and hold the weights, or swap the session for an easy spin in zone 1–2.")
	case "hold":
		if why != "" {
			lead += ", but " + why
		}
		return call("hold", "Train, but hold the weights", lead+". Train as planned, but hold the weights rather than adding load.")
	case "push":
		if m.TSB > 5 {
			return call("push", "Fresh and recovered", fmt.Sprintf("Recovery %d%%, form %+.0f. A good day for intensity or a heavier top set.", score, m.TSB))
		}
		return call("push", "A day to push", lead+", with HRV at or above your norm. A good day for intensity or a heavier top set.")
	}
	switch {
	case why != "":
		return call("normal", "Train as planned", lead+", and "+why+": nothing in the way of today's plan.")
	case m.HasLoad:
		return call("normal", "Train as planned", fmt.Sprintf("Recovery %d%% and form %+.0f: nothing in the way of today's plan.", score, m.TSB))
	}
	return call("normal", "Train as planned", lead+".")
}

func fatigued(tsb float64) Readiness {
	return call("easy", "Carrying a lot of fatigue", fmt.Sprintf("Form is %.0f. Keep today easy: hold the weights, no intervals.", tsb))
}
