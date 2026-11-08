package sweep

import (
	"context"
	"strings"
	"time"

	"domestique/internal/coach"
	"domestique/internal/store"
)

// exerciseNames asks System 1 about each exercise name the athlete hasn't
// used before: is it one of the names they already log, written another
// way? A confident yes stores an alias (store.Alias), so the new name's
// sets count with the old one's. Names are taken oldest first, and each is
// only compared with names used before it. It never changes load.
func (s *Sweeper) exerciseNames(ctx context.Context) (bool, error) {
	names, err := s.Store.ExerciseNames(ctx)
	if err != nil {
		return false, err
	}
	stored, err := s.Store.Aliases(ctx)
	if err != nil {
		return false, err
	}
	decided := map[string]bool{} // aliased, or undone by the athlete
	for _, a := range stored {
		decided[strings.ToLower(a.Name)] = true
	}
	var known []string // earlier names that aren't aliases themselves
	for _, n := range names {
		k := strings.ToLower(n.Name)
		if decided[k] {
			continue
		}
		if len(known) == 0 {
			known = append(known, n.Name)
			continue
		}
		near := coach.Nearest(n.Name, known, coach.MaxKnown)
		type verdict struct {
			To string  `json:"to"`
			P  float64 `json:"p"`
		}
		v, fresh, err := asked(ctx, s, "ai:exercise:"+k, struct {
			Name  string   `json:"name"`
			Known []string `json:"known"`
		}{n.Name, near}, func() (verdict, error) {
			to, p, err := s.W.AI.SameExercise(ctx, n.Name, near)
			return verdict{to, p}, err
		})
		if err != nil {
			if s.stop(ctx, err) {
				return false, err
			}
			known = append(known, n.Name)
			continue
		}
		if v.To == "" || v.P < coach.Bar("exercise") {
			known = append(known, n.Name)
			continue
		}
		if !fresh {
			continue // acted on before, and since undone or replaced
		}
		if err := s.Store.PutAlias(ctx, store.Alias{Name: n.Name, To: v.To, P: v.P, At: time.Now().UnixMilli()}); err != nil {
			return false, err
		}
		s.Log.Info("sweep: exercise name counted as another", "name", n.Name, "as", v.To, "p", v.P)
	}
	return false, nil
}
