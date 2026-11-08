package api

import (
	"context"
	"slices"
	"strings"
	"time"

	"domestique/internal/coach"
	"domestique/internal/store"
	"domestique/internal/sweep"
)

// complaints finds what the athlete kept writing about in the weeks before
// end: a body part, or illness, named on several different days. It counts
// only what the tags pass already read (tag notes, and the athlete's words
// on workouts tagged sore, injury or ill); no model is asked.
func (a *API) complaints(ctx context.Context, end time.Time) ([]string, error) {
	from := end.AddDate(0, 0, -7*coach.RecurWeeks)
	acts, err := a.Store.ListActivities(ctx, store.ActivityQuery{From: from.UnixMilli(), To: end.UnixMilli() - 1})
	if err != nil || len(acts) == 0 {
		return nil, err
	}
	ids := make([]string, len(acts))
	for i, x := range acts {
		ids[i] = x.ID
	}
	srcs, err := a.Store.SourcesForMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	var ms []coach.Mention
	for _, x := range acts {
		for _, src := range srcs[x.ID] {
			t, ok := sweep.SourceTags(ctx, a.Coach, src)
			if !ok {
				continue
			}
			text := t.Note
			if slices.ContainsFunc(t.Tags, func(s string) bool { return s == "sore" || s == "injury" || s == "ill" }) {
				text += "\n" + sweep.Description(src)
			}
			ms = append(ms, coach.Mention{At: time.UnixMilli(x.StartAt).In(a.loc()), Text: strings.TrimSpace(text), Tags: t.Tags})
		}
	}
	var out []string
	for _, c := range coach.Recurring(ms, end) {
		out = append(out, c.String())
	}
	return out, nil
}
