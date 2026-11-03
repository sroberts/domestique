// Package demo stands in for the Whoop importer until it exists. It fills the
// same tables the importer will (recovery, hr_samples, session strain), so the
// rest of the service and the PWA run their real code paths against it.
//
// Enabled with DOMESTIQUE_DEMO=1. Everything it generates is deterministic for
// a given date or session, so a restart never changes what you already saw.
package demo

import (
	"context"
	"errors"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"

	"domestique/internal/store"
	"domestique/internal/strength"
)

const Source = "demo-whoop"

// Demo generates stand-in Whoop data.
type Demo struct {
	Store *store.Store
	MaxHR int
	// HRDelay is how long after a session finishes before its heart rate
	// "arrives", mimicking a Whoop sync.
	HRDelay time.Duration
}

func seed(s string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(s))
	v := h.Sum64()
	return rand.New(rand.NewPCG(v, v^0x9e3779b97f4a7c15))
}

// EnsureDay fills recovery and a plan for date when missing.
func (d *Demo) EnsureDay(ctx context.Context, date string) error {
	if _, err := d.Store.GetRecovery(ctx, date); errors.Is(err, store.ErrNotFound) {
		r := seed("recovery:" + date)
		score := 30 + r.IntN(66) // 30..95: mostly green and yellow, occasionally red
		if r.IntN(6) == 0 {
			score = 14 + r.IntN(18) // a rough night now and then
		}
		rec := store.Recovery{
			Date:      date,
			Score:     score,
			HRVms:     math.Round((38+float64(score)*0.55+r.Float64()*6)*10) / 10,
			RestingHR: 60 - score/9 + r.IntN(3),
			Source:    Source,
		}
		if err := d.Store.PutRecovery(ctx, rec); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if _, err := d.Store.GetPlan(ctx, date); errors.Is(err, store.ErrNotFound) {
		ts, err := d.Store.ListTemplates(ctx)
		if err != nil || len(ts) == 0 {
			return err
		}
		day, err := time.Parse("2006-01-02", date)
		if err != nil {
			return err
		}
		t := ts[(day.YearDay())%len(ts)]
		return d.Store.PutPlan(ctx, date, t.ID)
	} else if err != nil {
		return err
	}
	return nil
}

// EnsureHR synthesizes heart rate and strain for a finished session once its
// "sync" delay has passed. It reports whether data now exists.
func (d *Demo) EnsureHR(ctx context.Context, sess store.Session, sets []store.Set) (bool, error) {
	if sess.FinishedAt == nil || len(sets) == 0 {
		return false, nil
	}
	if time.Since(time.UnixMilli(*sess.FinishedAt)) < d.HRDelay {
		return false, nil
	}
	from := sess.StartedAt - strength.HRPadding
	to := *sess.FinishedAt + strength.HRPadding
	have, err := d.Store.HR(ctx, from, to)
	if err != nil {
		return false, err
	}
	if len(have) == 0 {
		samples := synth(sess, sets, from, to, d.MaxHR)
		if err := d.Store.PutHR(ctx, Source, samples); err != nil {
			return false, err
		}
		have = samples
	}
	if sess.Strain == nil {
		if err := d.Store.SetStrain(ctx, sess.ID, strain(have, sess, d.MaxHR)); err != nil {
			return false, err
		}
	}
	return true, nil
}

// synth models heart rate as first-order dynamics: it climbs toward an effort
// target while lifting and decays toward an elevated gym baseline at rest.
func synth(sess store.Session, sets []store.Set, from, to int64, maxHR int) []store.HRSample {
	r := seed("hr:" + sess.ID)
	resting := 62.0 + r.Float64()*6
	gymBase := resting + 34
	hr := resting + 8
	drift := 0.0
	si := 0
	out := make([]store.HRSample, 0, (to-from)/1000+1)

	// Per-exercise character: bigger compound lifts push harder.
	effort := map[int]float64{}
	for _, s := range sets {
		if _, ok := effort[s.ExerciseIndex]; !ok {
			effort[s.ExerciseIndex] = 0.76 + r.Float64()*0.08 - float64(s.ExerciseIndex)*0.03
		}
	}

	for t := from; t <= to; t += 1000 {
		for si < len(sets) && t > sets[si].EndedAt {
			si++
		}
		inSet := si < len(sets) && t >= sets[si].StartedAt && t <= sets[si].EndedAt
		target := resting + 4
		tau := 45.0
		if t >= sess.StartedAt && (sess.FinishedAt == nil || t <= *sess.FinishedAt) {
			target = gymBase
		}
		if inSet {
			s := sets[si]
			load := effort[s.ExerciseIndex] + math.Min(float64(s.Reps), 15)*0.006
			target = math.Min(float64(maxHR)*load, float64(maxHR)-4)
			tau = 11
		}
		hr += (target - hr) / tau
		drift = drift*0.96 + r.NormFloat64()*0.5
		bpm := int(math.Round(hr + drift))
		out = append(out, store.HRSample{T: t, BPM: bpm})
	}
	return out
}

// strain is a Whoop-like 0..21 score from zone-weighted minutes.
func strain(hr []store.HRSample, sess store.Session, maxHR int) float64 {
	z := strength.ZonesFor(maxHR)
	var load float64
	for _, h := range hr {
		if h.T < sess.StartedAt || (sess.FinishedAt != nil && h.T > *sess.FinishedAt) {
			continue
		}
		load += float64(z.Of(h.BPM)) / 60
	}
	return math.Round(21*(1-math.Exp(-load/160))*10) / 10
}
