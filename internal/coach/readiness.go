package coach

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
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

// KV is where written calls are kept, so a restart doesn't pay for them twice.
type KV interface {
	SyncState(ctx context.Context, key string) (string, error)
	SetSyncState(ctx context.Context, key, value string) error
}

// Writer has the LLM write each day's call in the background. Today never
// waits on it: until a call is written, the rule-based one is shown.
type Writer struct {
	AI  *AI
	KV  KV
	Log *slog.Logger
	// Background outlives requests; cancelled on shutdown.
	Background context.Context

	mu       sync.Mutex
	inflight map[string]bool
	failed   map[string]time.Time // fingerprint -> when it last failed
}

// retryAfter is how long a failed fingerprint waits before another try:
// hosted models cost money, and an unavailable one stays so for a while.
const retryAfter = 15 * time.Minute

// Readiness returns the written call for these facts if there is one. If not,
// it starts writing one and reports pending, so the app knows to look again.
func (w *Writer) Readiness(ctx context.Context, f Facts) (call Call, ok, pending bool) {
	key := "ai:readiness:" + f.Date
	fp := fingerprint(f)
	if v, err := w.KV.SyncState(ctx, key); err == nil && v != "" {
		var c struct {
			Call
			FP string `json:"fp"`
		}
		if json.Unmarshal([]byte(v), &c) == nil && c.FP == fp {
			return c.Call, true, false
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inflight == nil {
		w.inflight, w.failed = map[string]bool{}, map[string]time.Time{}
	}
	if w.inflight[fp] {
		return Call{}, false, true
	}
	if t, bad := w.failed[fp]; bad && time.Since(t) < retryAfter {
		return Call{}, false, false
	}
	w.inflight[fp] = true
	bg := w.Background
	if bg == nil {
		bg = context.Background()
	}
	go w.write(bg, key, fp, f)
	return Call{}, false, true
}

func (w *Writer) write(ctx context.Context, key, fp string, f Facts) {
	start := time.Now()
	c, err := w.compose(ctx, f)
	if err == nil {
		// Keep it before clearing inflight, so no request in between starts
		// writing the same call again.
		v, _ := json.Marshal(struct {
			Call
			FP string `json:"fp"`
		}{c, fp})
		if err = w.KV.SetSyncState(ctx, key, string(v)); err != nil {
			err = fmt.Errorf("keep readiness: %w", err)
		}
	}
	w.mu.Lock()
	delete(w.inflight, fp)
	if err != nil {
		w.failed[fp] = time.Now()
	} else {
		delete(w.failed, fp)
	}
	w.mu.Unlock()
	if err != nil {
		lvl := slog.LevelWarn
		if Unavailable(err) {
			lvl = slog.LevelInfo // the operator's choice, not a fault
		}
		w.Log.Log(ctx, lvl, "coach: readiness not written; showing the rule-based call", "date", f.Date, "err", err.Error())
		return
	}
	w.Log.Info("coach: readiness written", "date", f.Date, "model", c.Model, "took", time.Since(start).Round(time.Millisecond).String())
}

const system = `You are the directeur sportif for one athlete, a cyclist who also lifts, speaking in their ear from the team car. Write today's call: what to do today, and the one reason that matters.

The decision is already made and is given to you as "level", "title" and "detail". Do not change it: never suggest harder or easier training than it says, and keep any instruction it gives (hold the weights, drop a set, ride easy). Your job is to say it better, using the facts. Never mention "level", or the field names.

Say what to do first, then why. Use one or two numbers at most, the ones behind the call, and never list figures. Name the planned session or yesterday's effort when it helps. With no planned_session there is no plan: don't speak of one. Keep a number's sign as given (form +7, form -12).

Voice: terse, plain, calm. Short sentences. No exclamation marks, no emoji, no praise, no hype, no greetings. Speak to the athlete as "you".

Use only numbers that appear in the facts; never invent a number, a goal or an event. Don't explain the terms: fitness, fatigue and form are known.

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

var (
	thinkRe  = regexp.MustCompile(`(?s)<think>.*?</think>`)
	numberRe = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

// parseCall reads the model's JSON and rejects anything that doesn't fit the
// slab: wrong shape, too long, or a number the facts don't contain.
func parseCall(text string, f Facts) (Call, error) {
	text = thinkRe.ReplaceAllString(text, "")
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j <= i {
		return Call{}, fmt.Errorf("reply is not JSON: %q", clip(text))
	}
	var c Call
	if err := json.Unmarshal([]byte(text[i:j+1]), &c); err != nil {
		return Call{}, fmt.Errorf("reply is not the expected JSON: %q", clip(text))
	}
	c.Title = strings.TrimRight(strings.TrimSpace(c.Title), ".")
	c.Detail = strings.TrimSpace(c.Detail)
	switch {
	case c.Title == "" || c.Detail == "":
		return Call{}, fmt.Errorf("reply is missing a title or detail: %q", clip(text))
	case utf8.RuneCountInString(c.Title) > 48 || len(strings.Fields(c.Title)) > 7:
		return Call{}, fmt.Errorf("title too long: %q", c.Title)
	case utf8.RuneCountInString(c.Detail) > 280:
		return Call{}, fmt.Errorf("detail too long: %q", clip(c.Detail))
	case strings.ContainsAny(c.Title+c.Detail, "!\n"):
		return Call{}, fmt.Errorf("reply breaks the voice: %q", clip(c.Title+" / "+c.Detail))
	}
	known := knownNumbers(f)
	for _, n := range numberRe.FindAllString(c.Title+" "+c.Detail, -1) {
		if !known[n] {
			return Call{}, fmt.Errorf("reply has a number not in the facts (%s): %q", n, clip(c.Detail))
		}
	}
	return c, nil
}

// knownNumbers is every number the model was shown, plus the small counts
// ("zone 2", "one or 2 sets") that need no source.
func knownNumbers(f Facts) map[string]bool {
	in, _ := json.Marshal(f)
	m := map[string]bool{}
	for _, n := range numberRe.FindAllString(string(in), -1) {
		m[n] = true
	}
	for i := 0; i <= 10; i++ {
		m[fmt.Sprint(i)] = true
	}
	return m
}

// fingerprint names the facts and the prompt they were written with, so a
// changed prompt rewrites the call too.
func fingerprint(f Facts) string {
	b, _ := json.Marshal(f)
	sum := sha256.Sum256(append([]byte(system), b...))
	return hex.EncodeToString(sum[:8])
}

func clip(s string) string {
	if utf8.RuneCountInString(s) > 120 {
		r := []rune(s)
		return string(r[:120]) + "…"
	}
	return s
}
