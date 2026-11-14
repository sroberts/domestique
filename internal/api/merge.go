package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"domestique/internal/store"
	"domestique/internal/strength"
)

// One workout sometimes ends up saved as two sessions: a circuit stopped
// after a round, then started again for the rest. Merging puts them back
// together as the earlier session, so history, progression, load and heart
// rate see one workout. There's no unmerge; the app confirms first.

// suggestWithin is how soon after one session ends a second of the same
// workout reads as its continuation.
const suggestWithin = 2 * time.Hour

func (a *API) registerMerges(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/sessions/{id}/merge", a.mergeSessions)
	mux.HandleFunc("GET /v1/sessions/merged", a.listMerges)
}

// mergeSessions merges the session in the path with {"with": "<id>"}. The
// earlier one is kept; the response names it. With ?dry_run=1 nothing is
// saved, and the response previews the merged session's name and plan.
func (a *API) mergeSessions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		With string `json:"with"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	switch {
	case !idRe.MatchString(id) || !idRe.MatchString(body.With):
		writeErr(w, http.StatusBadRequest, "bad session id")
		return
	case id == body.With:
		writeErr(w, http.StatusBadRequest, "a session can't be merged with itself")
		return
	}
	a.mergeMu.Lock()
	defer a.mergeMu.Unlock()
	ctx := r.Context()
	dry := r.URL.Query().Get("dry_run") == "1"
	var keep, gone, merged store.Session
	var moved []store.Set
	keep, err := a.Store.GetSession(ctx, id)
	if err == nil {
		if gone, err = a.Store.GetSession(ctx, body.With); err == nil {
			if gone.StartedAt < keep.StartedAt {
				keep, gone = gone, keep
			}
			if merged, moved, err = a.mergeOf(ctx, keep, gone); err == nil && !dry {
				err = a.applyMerge(ctx, merged, gone.ID, moved)
			}
		}
	}
	var refused refusal
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "no such session")
	case errors.As(err, &refused):
		writeErr(w, http.StatusConflict, string(refused))
	case err != nil:
		a.fail(w, r, err)
	case dry:
		kept, _ := a.Store.SessionSets(ctx, keep.ID)
		writeJSON(w, http.StatusOK, map[string]any{"id": keep.ID, "merged": gone.ID, "name": merged.Name,
			"plan": merged.Plan, "sets": len(kept) + len(moved), "started_at": merged.StartedAt, "finished_at": merged.FinishedAt})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"id": keep.ID, "merged": gone.ID})
	}
}

type refusal string

func (r refusal) Error() string { return string(r) }

// mergeOf works out keep with gone merged in (keep started first): the
// session to save and gone's sets moved onto it.
func (a *API) mergeOf(ctx context.Context, keep, gone store.Session) (store.Session, []store.Set, error) {
	if keep.FinishedAt == nil || gone.FinishedAt == nil {
		return keep, nil, refusal("only finished sessions can be merged")
	}
	if a.dayOf(keep.StartedAt) != a.dayOf(gone.StartedAt) {
		return keep, nil, refusal("only sessions on the same day can be merged")
	}
	keepSets, err := a.Store.SessionSets(ctx, keep.ID)
	if err != nil {
		return keep, nil, err
	}
	goneSets, err := a.Store.SessionSets(ctx, gone.ID)
	if err != nil {
		return keep, nil, err
	}
	canon, err := a.Store.Canon(ctx)
	if err != nil {
		return keep, nil, err
	}
	plan, moved := strength.MergePlan(keep, keepSets, gone, goneSets, canon)
	now := time.Now().UnixMilli()
	for i := range moved {
		moved[i].UpdatedAt = max(now, moved[i].UpdatedAt+1) // newer than any copy a phone holds
	}
	keep.Plan = plan
	fin := max(*keep.FinishedAt, *gone.FinishedAt)
	keep.FinishedAt = &fin
	keep.UpdatedAt = max(now, keep.UpdatedAt+1)
	if keep.TemplateID != gone.TemplateID && !strings.EqualFold(keep.Name, gone.Name) {
		keep.Name += " + " + gone.Name
	}
	return keep, moved, nil
}

// applyMerge saves a merge worked out by mergeOf, and brings the activity
// store along.
func (a *API) applyMerge(ctx context.Context, keep store.Session, gone string, moved []store.Set) error {
	// The activity the merged-away session hung off: it keeps any Whoop
	// recording, which then joins the kept session's activity.
	goneAct := ""
	if act, err := a.Store.ActivityBySession(ctx, gone); err == nil {
		goneAct = act.ID
	}
	if err := a.Store.MergeSessions(ctx, keep, gone, moved); err != nil {
		return err
	}
	if a.Ingest != nil {
		// The merge itself is done; the activity store catches up, and a
		// failure here is logged rather than undoing it.
		if err := a.Ingest.RemoveSession(ctx, gone); err != nil {
			a.Log.Warn("merge: drop session activity", "session", gone, "err", err.Error())
		}
		if err := a.Ingest.UpsertSession(ctx, keep.ID); err != nil {
			a.Log.Warn("merge: session to activity", "session", keep.ID, "err", err.Error())
		}
		if goneAct != "" {
			if _, err := a.Store.GetActivity(ctx, goneAct); err == nil {
				if kept, err := a.Store.ActivityBySession(ctx, keep.ID); err == nil && kept.ID != goneAct {
					if _, err := a.Ingest.MergePair(ctx, goneAct, kept.ID); err != nil {
						a.Log.Warn("merge: join activities", "from", goneAct, "into", kept.ID, "err", err.Error())
					}
				}
			}
		}
	}
	a.kickRecalc()
	return nil
}

func (a *API) dayOf(ms int64) string {
	return time.UnixMilli(ms).In(a.loc()).Format("2006-01-02")
}

// listMerges lists merges since ?since= (epoch ms), so a phone holding a
// merged-away session can drop it.
func (a *API) listMerges(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	ms, err := a.Store.Merges(r.Context(), since)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ms)
}

// mergeOptions fills in the other finished sessions the same day, which
// this one can be merged with, and suggests one that looks like this
// session's first part: the same workout, ended within two hours of this
// one starting.
func (a *API) mergeOptions(ctx context.Context, sum *strength.Summary) error {
	s := sum.Session
	if s.FinishedAt == nil {
		return nil
	}
	day, err := time.ParseInLocation("2006-01-02", a.dayOf(s.StartedAt), a.loc())
	if err != nil {
		return err
	}
	others, err := a.Store.SessionsBetween(ctx, day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()-1)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.ID == s.ID {
			continue
		}
		sets, err := a.Store.SessionSets(ctx, o.ID)
		if err != nil {
			return err
		}
		sum.SameDay = append(sum.SameDay, strength.SessionRef{ID: o.ID, Name: o.Name, StartedAt: o.StartedAt, FinishedAt: *o.FinishedAt, Sets: len(sets)})
		gap := time.Duration(s.StartedAt-*o.FinishedAt) * time.Millisecond
		if o.TemplateID != "" && o.TemplateID == s.TemplateID && o.StartedAt < s.StartedAt && gap <= suggestWithin {
			sum.SuggestMerge = o.ID
		}
	}
	return nil
}

// placeLateSet handles a set uploaded to a session that has since been
// merged into another. One already moved keeps its place in the merged
// session (only reps, weight and times change); one the server never saw is
// added after the merged session's sets of the same exercise.
func (a *API) placeLateSet(ctx context.Context, s *store.Set, into string) error {
	if was, err := a.Store.GetSet(ctx, s.ID); err == nil {
		s.SessionID, s.ExerciseIndex, s.ExerciseName, s.SetIndex = was.SessionID, was.ExerciseIndex, was.ExerciseName, was.SetIndex
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	sess, err := a.Store.GetSession(ctx, into)
	if err != nil {
		return err
	}
	canon, err := a.Store.Canon(ctx)
	if err != nil {
		return err
	}
	idx := -1
	for i, ex := range sess.Plan {
		if strings.EqualFold(canon.Of(ex.Name), canon.Of(s.ExerciseName)) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return refusal("this session was merged into another, which has no " + s.ExerciseName)
	}
	sets, err := a.Store.SessionSets(ctx, into)
	if err != nil {
		return err
	}
	next := 0
	for _, x := range sets {
		if x.ExerciseIndex == idx {
			next = max(next, x.SetIndex+1)
		}
	}
	s.SessionID, s.ExerciseIndex, s.SetIndex = into, idx, next
	if len(sess.Plan[idx].Sets) <= next {
		for len(sess.Plan[idx].Sets) <= next {
			sess.Plan[idx].Sets = append(sess.Plan[idx].Sets, store.TargetSet{Reps: s.TargetReps, WeightKg: s.TargetWeightKg})
		}
		sess.UpdatedAt = max(time.Now().UnixMilli(), sess.UpdatedAt+1)
		return a.Store.PutSession(ctx, sess)
	}
	return nil
}
