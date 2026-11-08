package sweep

import (
	"context"
	"fmt"

	"domestique/internal/coach"
	"domestique/internal/gear"
	"domestique/internal/store"
)

// GearKey keys the parts read from one source's description.
func GearKey(sourceID string) string { return "ai:gear:" + sourceID }

// gearParts has the LLM read the parts the athlete wrote about fitting,
// from each description that names one. They become suggestions on the
// Gear page (internal/api); nothing changes here.
func (s *Sweeper) gearParts(ctx context.Context) (bool, error) {
	all, err := s.Store.AllSources(ctx)
	if err != nil {
		return false, err
	}
	for _, src := range all {
		d := Description(src)
		if d == "" || !coach.MentionsGear(d) {
			continue
		}
		key, fp := GearKey(src.ID), coach.Fingerprint(coach.GearPrompt(gear.Kinds), d)
		var g coach.GearRead
		if s.W.Cached(ctx, key, fp, &g) {
			continue
		}
		g, err := s.W.GearEvents(ctx, d, gear.Kinds)
		if err != nil {
			if s.stop(ctx, err) {
				return false, fmt.Errorf("gear for %s: %w", src.ID, err)
			}
			g = coach.GearRead{} // an unreadable reply: nothing, and don't ask again
		}
		if err := s.W.Keep(ctx, key, fp, g); err != nil {
			return false, err
		}
	}
	return false, nil // suggestions only
}

// SourceGear returns the parts kept for a source's description, if any.
func SourceGear(ctx context.Context, w *coach.Writer, src store.Source) []coach.GearEvent {
	d := Description(src)
	if d == "" || w == nil || !coach.MentionsGear(d) {
		return nil
	}
	var g coach.GearRead
	if !w.Cached(ctx, GearKey(src.ID), coach.Fingerprint(coach.GearPrompt(gear.Kinds), d), &g) {
		return nil
	}
	return g.Events
}
