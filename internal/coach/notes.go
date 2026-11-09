package coach

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ---- One activity --------------------------------------------------------------

// ActivityFacts is what an activity's note is written from.
type ActivityFacts struct {
	Name        string   `json:"name"`
	Sport       string   `json:"sport"`
	Indoor      bool     `json:"indoor,omitempty"`
	Minutes     int      `json:"minutes"`
	KM          *float64 `json:"distance_km,omitempty"`
	AscentM     *int     `json:"ascent_m,omitempty"`
	AvgPower    *int     `json:"avg_power_w,omitempty"`
	NP          *int     `json:"normalized_power_w,omitempty"`
	FTP         *int     `json:"ftp_w,omitempty"`
	IF          *float64 `json:"intensity_factor,omitempty"`
	TSS         *int     `json:"tss,omitempty"`
	AvgHR       *int     `json:"avg_hr,omitempty"`
	MaxHR       *int     `json:"max_hr,omitempty"`
	HRDrift     *int     `json:"hr_drift_pct,omitempty"` // second half's HR per watt over the first's
	Strain      *float64 `json:"whoop_strain,omitempty"`
	ZoneMins    []int    `json:"minutes_in_hr_zones_1_to_5,omitempty"`
	BestEver    []string `json:"new_power_bests,omitempty"` // "5 min: 312 W"
	Sets        int      `json:"sets,omitempty"`
	VolumeKg    *int     `json:"volume_kg,omitempty"`
	AthleteNote string   `json:"athletes_own_notes,omitempty"`
}

const activityPrompt = `You are the directeur sportif for one athlete, a cyclist who also lifts. Write one or two sentences about one finished workout: what kind of session it was, then the one thing worth noticing (how hard, how steady, a new best, time in a zone). If the athlete wrote their own notes, pick up on them in a few words. Don't recite figures: at most two numbers.

` + voice + `

An example of the voice, not of this workout:
{"note": "Long endurance day with the club, mostly zone 2. Your legs went late, as you said; heart rate stayed level, so it was fuelling, not fitness."}

Reply with only a JSON object, no other text:
{"note": "<one or two sentences, at most 240 characters>"}`

// ActivityNote returns the note for an activity, or starts writing it.
func (w *Writer) ActivityNote(ctx context.Context, id string, f ActivityFacts) (n Note, ok, pending bool) {
	ok, pending = w.Do(ctx, "ai:note:activity:"+id, Fingerprint(activityPrompt, f), &n,
		func(ctx context.Context) (any, error) { return w.note(ctx, activityPrompt, f, 240, 2) })
	return n, ok, pending
}

// ---- One strength session ------------------------------------------------------

// SessionFacts is what a lifting session's note is written from.
type SessionFacts struct {
	Name      string         `json:"name"`
	Minutes   int            `json:"minutes"`
	VolumeKg  int            `json:"volume_kg"`
	SetsDone  int            `json:"sets_done"`
	SetsPlan  int            `json:"sets_planned"`
	PeakHR    *int           `json:"peak_hr,omitempty"`
	Exercises []ExerciseFact `json:"exercises"`
}

// ExerciseFact is one exercise as lifted.
type ExerciseFact struct {
	Name   string    `json:"name"`
	Sets   []SetFact `json:"sets"`
	Change string    `json:"change_from_last_time,omitempty"`
}

// SetFact is one set: what was asked, what was done, and the heart rate.
type SetFact struct {
	Reps       int     `json:"reps"`
	TargetReps int     `json:"target_reps"`
	Kg         float64 `json:"kg"`
	PeakHR     *int    `json:"peak_hr,omitempty"`
	DropHR     *int    `json:"hr_drop_after_bpm,omitempty"`
	RestS      *int    `json:"rest_s,omitempty"`
	Effort     string  `json:"effort,omitempty"`
}

const sessionPrompt = `You are the directeur sportif for one athlete, a cyclist who also lifts. Write a short debrief of one finished lifting session, two or three sentences: what went up or held, any missed reps (reps below target_reps), and how heart rate came down between sets when it's given. Name exercises. Don't recite every set: at most three numbers.

` + voice + `

An example of the voice, not of this session:
{"note": "Squat went up and every set hit 5. Bench held, but the last set stopped at 6 of 8. Heart rate came down well between sets."}

Reply with only a JSON object, no other text:
{"note": "<two or three sentences, at most 320 characters>"}`

// SessionNote returns the debrief of a lifting session, or starts writing it.
func (w *Writer) SessionNote(ctx context.Context, id string, f SessionFacts) (n Note, ok, pending bool) {
	ok, pending = w.Do(ctx, "ai:note:session:"+id, Fingerprint(sessionPrompt, f), &n,
		func(ctx context.Context) (any, error) { return w.note(ctx, sessionPrompt, f, 320, 3) })
	return n, ok, pending
}

// ---- One week ------------------------------------------------------------------

// WeekFacts is what a week's note is written from: a finished Monday-Sunday.
type WeekFacts struct {
	Week        string    `json:"week_starting"`
	Hours       float64   `json:"hours"`
	TSS         int       `json:"tss"`
	RideTSS     int       `json:"ride_tss"`
	StrengthTSS int       `json:"strength_tss"`
	PrevTSS     *int      `json:"previous_week_tss,omitempty"`
	Days        []WeekDay `json:"days"`
	FitnessFrom int       `json:"fitness_at_start"`
	FitnessTo   int       `json:"fitness_at_end"`
	FormEnd     int       `json:"form_at_end"`
	Sessions    int       `json:"lifting_sessions_done"`
	Planned     int       `json:"lifting_sessions_planned"`
	Recovery    *int      `json:"avg_recovery_pct,omitempty"`
	SleepH      *float64  `json:"avg_sleep_hours,omitempty"`
	Notes       []string  `json:"athletes_notes,omitempty"`
	// RideMix is ride minutes by type (endurance, tempo, intervals, ...),
	// for rides System 1 classified confidently.
	RideMix   map[string]int `json:"ride_minutes_by_type,omitempty"`
	Recurring []string       `json:"keeps_coming_up,omitempty"` // Complaint.String()s
}

// WeekDay is one day of the week.
type WeekDay struct {
	Day  string   `json:"day"` // Mon..Sun
	TSS  int      `json:"tss"`
	What []string `json:"activities,omitempty"`
}

const weekPrompt = `You are the directeur sportif for one athlete, a cyclist who also lifts. Write the note on their finished training week, three or four sentences: the shape of the week (load, where it came from, how it compared with the week before, and the kind of riding when ride_minutes_by_type is given: all easy, or plenty of intensity), whether the lifting happened as planned, recovery and sleep if given, anything in keeps_coming_up in a few plain words (something the athlete keeps writing about, worth watching, never a diagnosis), and one plain suggestion for next week that follows from the facts. Don't recite figures: at most four numbers.

` + voice + `

Reply with only a JSON object, no other text:
{"note": "<three or four sentences, at most 480 characters>"}`

// WeekNote returns the note for a finished week, or starts writing it.
func (w *Writer) WeekNote(ctx context.Context, f WeekFacts) (n Note, ok, pending bool) {
	ok, pending = w.Do(ctx, "ai:note:week:"+f.Week, Fingerprint(weekPrompt, f), &n,
		func(ctx context.Context) (any, error) { return w.note(ctx, weekPrompt, f, 480, 4) })
	return n, ok, pending
}

// note asks for prose and checks it, allowing one figure over the prompt's
// budget before calling it a recital.
func (w *Writer) note(ctx context.Context, prompt string, facts any, maxRunes, maxNumbers int) (Note, error) {
	in, _ := json.MarshalIndent(facts, "", "  ")
	text, model, err := w.AI.Chat(ctx, prompt, "Facts:\n"+string(in))
	if err != nil {
		return Note{}, err
	}
	var out struct {
		Note string `json:"note"`
	}
	if err := parseObject(text, &out); err != nil {
		return Note{}, fmt.Errorf("%w (model %s)", err, model)
	}
	out.Note = strings.Join(strings.Fields(out.Note), " ")
	if err := checkProse(out.Note, maxRunes, facts); err != nil {
		return Note{}, fmt.Errorf("%w (model %s)", err, model)
	}
	if n := figures(out.Note); n > maxNumbers+1 {
		return Note{}, fmt.Errorf("recites %d numbers (model %s): %q", n, model, clip(out.Note))
	}
	return Note{Text: out.Note, Model: model}, nil
}

// ---- Tags from the athlete's own descriptions ----------------------------------

// TagVocabulary is every tag the LLM may give a description. A fixed list,
// so tags can be counted and searched over time.
var TagVocabulary = []string{
	"race", "group ride", "commute", "intervals", "recovery ride", "long ride", "test",
	"felt strong", "tired legs", "sore", "injury", "ill", "poor sleep", "stress",
	"heat", "cold", "rain", "wind", "travel", "new gear", "bike fit", "mechanical", "crash",
}

// Tags is what an athlete's description says, as tags and one short note.
type Tags struct {
	Tags  []string `json:"tags"`
	Note  string   `json:"note,omitempty"` // a fact worth keeping, in a few words: "new saddle"
	Model string   `json:"model,omitempty"`
}

const tagsPrompt = `Read an athlete's own description of one workout and tag it. Choose only tags from this list, only when the description clearly says so, and none if nothing fits: %s.

Also give "note": a concrete fact worth remembering months later, in at most 8 words, or "" if there isn't one. Only these count: new or changed equipment, pain or injury and where, illness, a race result or placing. How a ride felt, its route or who came along is not a note; leave it "". Never restate the description and never invent anything not written.

Reply with only a JSON object, no other text:
{"tags": [...], "note": "..."}`

// DescribeTags reads tags out of a description (synchronous; background
// sweeps call it).
func (w *Writer) DescribeTags(ctx context.Context, description string) (Tags, error) {
	prompt := fmt.Sprintf(tagsPrompt, strings.Join(TagVocabulary, ", "))
	text, model, err := w.AI.Chat(ctx, prompt, "Description:\n"+description)
	if err != nil {
		return Tags{}, err
	}
	var t Tags
	if err := parseObject(text, &t); err != nil {
		return Tags{}, fmt.Errorf("%w (model %s)", err, model)
	}
	keep := t.Tags[:0]
	for _, tag := range t.Tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if slices.Contains(TagVocabulary, tag) && !slices.Contains(keep, tag) {
			keep = append(keep, tag)
		}
	}
	t.Tags = keep
	t.Note = strings.TrimRight(strings.TrimSpace(t.Note), ".")
	if len(strings.Fields(t.Note)) > 10 {
		t.Note = ""
	}
	if t.Note != "" {
		// The note must come from the text: its numbers, at least.
		if checkProse(t.Note, 80, description) != nil {
			t.Note = ""
		}
	}
	t.Model = model
	return t, nil
}

// TagsPrompt identifies the prompt the tags were read with.
func TagsPrompt() string { return tagsPrompt + strings.Join(TagVocabulary, ",") }

// ---- A workout written as text -------------------------------------------------

// ParsedExercise is one exercise read from text.
type ParsedExercise struct {
	Name        string      `json:"name"`
	RestSeconds int         `json:"rest_seconds"`
	Sets        []ParsedSet `json:"sets"`
}

// ParsedSet is one set read from text.
type ParsedSet struct {
	Reps     int     `json:"reps"`
	WeightKg float64 `json:"weight_kg"`
}

const workoutPrompt = `Turn a strength workout written as text into JSON. One entry per exercise, in order, with one entry per set. "5x5 squat at 100" is five sets of 5 reps at 100 kg. Weights are kilograms; convert pounds (lb, lbs) to kg rounded to 0.5. Bodyweight or no weight given is 0. rest_seconds is the rest between sets if stated, else 0. Give exercises their common name in Title Case ("Back Squat", "Romanian Deadlift"). Leave out anything that isn't an exercise.

Reply with only a JSON object, no other text:
{"name": "<short workout name, or empty>", "exercises": [{"name": "...", "rest_seconds": 0, "sets": [{"reps": 5, "weight_kg": 100}]}]}`

// ParseWorkout reads a workout from text. The athlete is waiting, so this
// is synchronous; the caller validates the result as it would any template.
func (w *Writer) ParseWorkout(ctx context.Context, text string) (name string, ex []ParsedExercise, err error) {
	reply, model, err := w.AI.Chat(ctx, workoutPrompt, text)
	if err != nil {
		return "", nil, err
	}
	var out struct {
		Name      string           `json:"name"`
		Exercises []ParsedExercise `json:"exercises"`
	}
	if err := parseObject(reply, &out); err != nil {
		return "", nil, fmt.Errorf("%w (model %s)", err, model)
	}
	if len(out.Exercises) == 0 {
		return "", nil, fmt.Errorf("no exercises found (model %s)", model)
	}
	return strings.TrimSpace(out.Name), out.Exercises, nil
}

// figures counts the numbers in s that read as figures: small counts ("zone
// 3", "2 sets", "9 donuts") don't.
func figures(s string) int {
	n := 0
	for _, x := range numberRe.FindAllString(s, -1) {
		if v, err := strconv.ParseFloat(x, 64); err == nil && v > 10 {
			n++
		}
	}
	return n
}
