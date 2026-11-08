package api

import (
	"context"
	"errors"
	"net/http"

	"domestique/internal/coach"
	"domestique/internal/ingest"
	"domestique/internal/store"
	"domestique/internal/sweep"
)

// System 1's verdicts on the data itself (internal/sweep/quality.go):
// channels judged broken and left out, and each ride's type.

// qualityDetail adds broken channels and the ride's type to an activity's
// merged view. Broken channels show whether AI is on or not: the data has
// already changed on them.
func (a *API) qualityDetail(ctx context.Context, resp *detailResp) {
	for _, src := range resp.Sources {
		for ch, why := range ingest.BadChannels(src.Extra) {
			if resp.BadData == nil {
				resp.BadData = map[string]string{}
			}
			if resp.BadData[ch] == "" {
				resp.BadData[ch] = why
			}
		}
	}
	if t, err := a.Store.SyncState(ctx, sweep.RideTypePrefix+resp.Activity.ID); err == nil {
		resp.RideType = t
	}
}

// dataOK is the athlete saying an activity's data is fine after all: every
// channel System 1 left out goes back in, and it isn't asked about again.
func (a *API) dataOK(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	srcs, err := a.Store.SourcesFor(ctx, id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if len(srcs) == 0 {
		if _, err := a.Store.GetActivity(ctx, id); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "no such activity")
			return
		}
	}
	touched := false
	for _, src := range srcs {
		if len(ingest.BadChannels(src.Extra)) == 0 {
			continue
		}
		if err := a.Store.SetSyncState(ctx, sweep.BadKeptPrefix+src.ID, "kept"); err != nil {
			a.fail(w, r, err)
			return
		}
		if err := a.Store.SetSourceExtra(ctx, src.ID, ingest.BadExtra, nil); err != nil {
			a.fail(w, r, err)
			return
		}
		touched = true
	}
	if touched && a.Ingest != nil {
		if err := a.Ingest.Rebuild(ctx, id); err != nil {
			a.fail(w, r, err)
			return
		}
		a.kickRecalc()
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "bad_data": nil})
}

// rideMix is the week's ride minutes by type, for the rides System 1 was
// sure of.
func (a *API) rideMix(ctx context.Context, acts []store.Activity, f *coach.WeekFacts) {
	types, err := a.Store.SyncStatePrefix(ctx, sweep.RideTypePrefix)
	if err != nil || len(types) == 0 {
		return
	}
	for _, x := range acts {
		t := types[x.ID]
		if t == "" {
			continue
		}
		if f.RideMix == nil {
			f.RideMix = map[string]int{}
		}
		f.RideMix[t] += (max(x.MovingS, 0) + 59) / 60
	}
}
