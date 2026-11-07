package coach

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Facts is everything the call for a day is written from. Values are rounded
// so that small changes during the day don't ask for a new message.
type Facts struct {
	Date  string `json:"date"`
	Level string `json:"level"` // push | normal | hold | easy: decided by rules, never by the model
	// The rule-based call, which the model rewrites and must not contradict.
	Title  string `json:"title"`
	Detail string `json:"detail"`

	Recovery  *int     `json:"recovery_pct,omitempty"`
	HRVms     *int     `json:"hrv_ms,omitempty"`
	RestingHR *int     `json:"resting_hr,omitempty"`
	SleepH    *float64 `json:"sleep_hours,omitempty"` // to 0.1 h
	SleepPerf *int     `json:"sleep_performance_pct,omitempty"`
	Fitness   *int     `json:"fitness_ctl,omitempty"`
	Fatigue   *int     `json:"fatigue_atl,omitempty"`
	Form      *int     `json:"form_tsb,omitempty"`
	Ramp      *int     `json:"fitness_change_7d,omitempty"`
	Plan      string   `json:"planned_session,omitempty"`
	PlanSets  int      `json:"planned_sets,omitempty"`
	Recent    []Event  `json:"recent_activities,omitempty"`
}

// Event is one recent activity.
type Event struct {
	When string `json:"when"` // "today", "yesterday", "2 days ago"
	What string `json:"what"` // name, or sport
	Min  int    `json:"minutes"`
	TSS  int    `json:"tss,omitempty"`
}

// Call is the message shown for the day.
type Call struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Model  string `json:"model,omitempty"`
}

// Readiness returns the written call for these facts if there is one. If not,
// it starts writing one and reports pending, so the app knows to look again.
func (w *Writer) Readiness(ctx context.Context, f Facts) (call Call, ok, pending bool) {
	ok, pending = w.Do(ctx, "ai:readiness:"+f.Date, Fingerprint(system, f), &call,
		func(ctx context.Context) (any, error) { return w.compose(ctx, f) })
	return call, ok, pending
}

const system = `You are the directeur sportif for one athlete, a cyclist who also lifts, speaking in their ear from the team car. Write today's call: what to do today, and the one reason that matters.

The decision is already made and is given to you as "level", "title" and "detail". Do not change it: never suggest harder or easier training than it says, and keep any instruction it gives (hold the weights, drop a set, ride easy). Your job is to say it better, using the facts. Never mention "level", or the field names.

Say what to do first, then why. Use one or two numbers at most, the ones behind the call, and never list figures. Name the planned session or yesterday's effort when it helps. With no planned_session there is no plan: don't speak of one. Keep a number's sign as given (form +7, form -12).

` + voice + `

Examples of the voice, not of today:
{"title": "Hold the weights", "detail": "Recovery is 48% after yesterday's 90 minutes on the bike. Do Lower A as written, no added load."}
{"title": "Easy spin only", "detail": "Recovery is 22%. Skip the intervals; an hour in zone 2 at most."}

Reply with only a JSON object, no other text:
{"title": "<2 to 6 words>", "detail": "<one or two sentences, at most 220 characters>"}`

// compose asks the LLM for the call and checks what comes back.
func (w *Writer) compose(ctx context.Context, f Facts) (Call, error) {
	in, _ := json.MarshalIndent(f, "", "  ")
	text, model, err := w.AI.Chat(ctx, system, "Facts:\n"+string(in))
	if err != nil {
		return Call{}, err
	}
	c, err := parseCall(text, f)
	if err != nil {
		return Call{}, fmt.Errorf("%w (model %s)", err, model)
	}
	c.Model = model
	return c, nil
}

// parseCall reads the model's JSON and rejects anything that doesn't fit the
// slab: wrong shape, too long, or a number the facts don't contain.
func parseCall(text string, f Facts) (Call, error) {
	var c Call
	if err := parseObject(text, &c); err != nil {
		return Call{}, err
	}
	c.Title = strings.TrimRight(strings.TrimSpace(c.Title), ".")
	c.Detail = strings.TrimSpace(c.Detail)
	switch {
	case c.Title == "":
		return Call{}, fmt.Errorf("reply has no title: %q", clip(text))
	case utf8.RuneCountInString(c.Title) > 48 || len(strings.Fields(c.Title)) > 7:
		return Call{}, fmt.Errorf("title too long: %q", c.Title)
	case strings.Contains(c.Title, "\n") || strings.Contains(c.Detail, "\n"):
		return Call{}, fmt.Errorf("reply has line breaks: %q", clip(c.Title+" / "+c.Detail))
	}
	if err := checkProse(c.Title, 48, f); err != nil {
		return Call{}, err
	}
	if err := checkProse(c.Detail, 280, f); err != nil {
		return Call{}, err
	}
	return c, nil
}
