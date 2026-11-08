package sweep

import (
	"context"
	"errors"
	"time"

	"domestique/internal/activity"
	"domestique/internal/coach"
	"domestique/internal/ingest"
	"domestique/internal/load"
	"domestique/internal/store"
)

const (
	// BadKeptPrefix keys the athlete's word that a source's data is fine,
	// after System 1 called it broken: it isn't asked about again.
	BadKeptPrefix = "ai:bad-kept:"
	// RideTypePrefix keys a ride's type, kept when System 1 was sure.
	RideTypePrefix = "ai:ridetype:"
)

// badData asks System 1 about recordings whose heart rate or power looks
// off, and marks a confident yes on the source (ingest.BadExtra), so the
// merged view, zones and load leave that channel out.
func (s *Sweeper) badData(ctx context.Context) (bool, error) {
	acts, srcs, err := s.activities(ctx, func(store.Activity) bool { return true })
	if err != nil {
		return false, err
	}
	kept, err := s.Store.SyncStatePrefix(ctx, BadKeptPrefix)
	if err != nil {
		return false, err
	}
	ath := load.Current(ctx, s.Store, 0)
	changed := false
	for _, a := range acts {
		touched := false
		for _, src := range srcs[a.ID] {
			if kept[src.ID] != "" || !(src.HasHR || src.HasPower) {
				continue
			}
			type verdict struct {
				HR       float64 `json:"hr"`
				Power    float64 `json:"power"`
				HRWhy    string  `json:"hr_why,omitempty"`
				PowerWhy string  `json:"power_why,omitempty"`
			}
			// The stream doesn't change once imported: these stand for it,
			// so it's read only when they're new.
			id := struct {
				Imported int64
				MaxHR    int
				FTP      int
			}{src.ImportedAt, ath.MaxHR, a.FTP}
			v, fresh, err := asked(ctx, s, "ai:bad-check:"+src.ID, id, func() (verdict, error) {
				none := verdict{HR: -1, Power: -1}
				var st activity.Stream
				if err := s.Store.GetStream(ctx, src.ID, &st); errors.Is(err, store.ErrNotFound) {
					return none, nil
				} else if err != nil {
					return none, err
				}
				f := recordingFacts(src, a.Sport, &st, ath.MaxHR, a.FTP)
				if f = coach.Suspect(f); f.HR == nil && f.Power == nil {
					return none, nil
				}
				v := none
				if f.HR != nil {
					v.HRWhy = hrReason(f.HR)
				}
				if f.Power != nil {
					v.PowerWhy = powerReason(f.Power)
				}
				var err error
				v.HR, v.Power, err = s.W.AI.BadData(ctx, f)
				return v, err
			})
			if err != nil {
				if s.stop(ctx, err) {
					return changed, err
				}
				continue
			}
			if !fresh {
				continue
			}
			bad := map[string]string{}
			if v.HR >= coach.Bar("bad_data") {
				bad["hr"] = v.HRWhy
			}
			if v.Power >= coach.Bar("bad_data") {
				bad["power"] = v.PowerWhy
			}
			if len(bad) == 0 {
				continue
			}
			if err := s.Store.SetSourceExtra(ctx, src.ID, ingest.BadExtra, bad); err != nil {
				return changed, err
			}
			s.Log.Info("sweep: bad data, left out", "activity", a.ID, "source", src.ID, "channels", bad)
			touched = true
		}
		if touched {
			if err := s.Ingest.Rebuild(ctx, a.ID); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}

// rideTypes asks System 1 what kind of ride each ride was, and keeps a
// confident answer under RideTypePrefix. It changes no load.
func (s *Sweeper) rideTypes(ctx context.Context) (bool, error) {
	acts, srcs, err := s.activities(ctx, func(a store.Activity) bool {
		return activity.IsRide(a.Sport) && max(a.MovingS, a.ElapsedS) >= 10*60
	})
	if err != nil {
		return false, err
	}
	loc := s.Loc
	if loc == nil {
		loc = time.Local
	}
	ath := load.Current(ctx, s.Store, 0)
	for _, a := range acts {
		type verdict struct {
			Type string  `json:"type"`
			P    float64 `json:"p"`
		}
		// The ride's stored numbers stand for its facts, so its stream is
		// read only when something changed.
		id := struct {
			Updated     int64
			FTP, MaxHR  int
			Intensity   float64
			Name, Sport string
		}{a.UpdatedAt, a.FTP, ath.MaxHR, a.Intensity, a.Name, a.Sport}
		v, fresh, err := asked(ctx, s, "ai:ridetype-check:"+a.ID, id, func() (verdict, error) {
			m, _, err := s.Ingest.MergedStream(ctx, a.ID)
			if err != nil {
				return verdict{}, err
			}
			notes := ""
			for _, src := range srcs[a.ID] {
				if d := Description(src); d != "" {
					notes = d
					break
				}
			}
			t, p, err := s.W.AI.RideTypeGuess(ctx, rideFacts(a, &m.Stream, ath.MaxHR, loc, notes))
			return verdict{t, p}, err
		})
		if err != nil {
			if s.stop(ctx, err) {
				return false, err
			}
			continue
		}
		if !fresh {
			continue
		}
		t := ""
		if v.P >= coach.Bar("ride_type") {
			t = v.Type
		}
		if err := s.Store.SetSyncState(ctx, RideTypePrefix+a.ID, t); err != nil {
			return false, err
		}
	}
	return false, nil
}
