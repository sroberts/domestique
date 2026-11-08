package coach

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ---- A finished workout written as text ------------------------------------------

// LoggedWorkout is a session the athlete already did, read from text.
type LoggedWorkout struct {
	Name      string           `json:"name"`
	Date      string           `json:"date"`    // YYYY-MM-DD, or "" for today
	Time      string           `json:"time"`    // HH:MM local start, or "" when not said
	Minutes   int              `json:"minutes"` // stated duration, or 0
	Exercises []ParsedExercise `json:"exercises"`
	// NotLogged is what the text mentions that isn't lifting ("20 min
	// walk"): shown to the athlete, never turned into an activity.
	NotLogged []string `json:"not_logged"`
}

const loggedPrompt = `The athlete describes a strength workout they have ALREADY DONE. Turn it into JSON. One entry per exercise, in order, with one entry per set as actually done. "3x10 push-ups" is three sets of 10 reps; "squats 5x5 at 80" is five sets of 5 reps at 80 kg; "bench 60x8, 70x6" is two sets. If they say a set fell short ("last set only 3"), write that set's real reps. Weights are kilograms; convert pounds (lb, lbs) to kg rounded to 0.5. Bodyweight or no weight given is 0. Give exercises their common name in Title Case ("Back Squat", "Push-Up").

Anything that isn't lifting (a walk, a run, a ride, stretching, a sauna) goes in not_logged as a short phrase, not in exercises.

Today is %s. date is the day it was done as YYYY-MM-DD ("yesterday" is the day before today), or empty if not said. time is when it started as 24-hour HH:MM if said ("this morning at 7" is 07:00), else empty. minutes is how long the lifting took if said, else 0.

Reply with only a JSON object, no other text:
{"name": "<short workout name, or empty>", "date": "", "time": "", "minutes": 0, "exercises": [{"name": "...", "rest_seconds": 0, "sets": [{"reps": 10, "weight_kg": 0}]}], "not_logged": []}`

var (
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	clockRe = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d$`)
)

// ParseLog reads a finished workout from text. today is the athlete's
// local now, for "yesterday" and the like. The athlete is waiting, so this
// is synchronous; the caller checks the result before anything is saved.
func (w *Writer) ParseLog(ctx context.Context, text string, today time.Time) (LoggedWorkout, error) {
	reply, model, err := w.AI.Chat(ctx, fmt.Sprintf(loggedPrompt, today.Format("Monday 2006-01-02")), text)
	if err != nil {
		return LoggedWorkout{}, err
	}
	var out LoggedWorkout
	if err := parseObject(reply, &out); err != nil {
		return LoggedWorkout{}, fmt.Errorf("%w (model %s)", err, model)
	}
	if len(out.Exercises) == 0 {
		return LoggedWorkout{}, fmt.Errorf("no exercises found (model %s)", model)
	}
	out.Name = strings.TrimSpace(out.Name)
	// When the model gets the day or time wrong, it's dropped, not guessed at.
	if !dateRe.MatchString(out.Date) {
		out.Date = ""
	} else if d, err := time.ParseInLocation("2006-01-02", out.Date, today.Location()); err != nil || d.After(today) {
		out.Date = ""
	}
	if !clockRe.MatchString(out.Time) {
		out.Time = ""
	}
	if out.Minutes < 0 || out.Minutes > 300 {
		out.Minutes = 0
	}
	kept := []string{}
	for _, s := range out.NotLogged {
		if s = strings.TrimSpace(s); s != "" && len(kept) < 5 {
			kept = append(kept, clip(s))
		}
	}
	out.NotLogged = kept
	return out, nil
}
