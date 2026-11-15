// Package checkin is the short check-in after a workout: a few one-tap
// questions on how it went, which devices can't record. Answers are fixed
// values, never display text, so the app can reword the questions freely.
package checkin

import (
	"slices"
	"strings"
	"unicode/utf8"

	"domestique/internal/coach"
	"domestique/internal/store"
)

// The questions, in the order they're asked.
const (
	Effort = "effort" // how hard was it (session RPE)
	Legs   = "legs"   // how did your legs feel
	Plan   = "plan"   // did it go to plan
	Pain   = "pain"   // anything hurting
	Fuel   = "fuel"   // fuel and energy
)

// Answers to each question, easiest or best first.
var Answers = map[string][]string{
	Effort: {"easy", "moderate", "hard", "very_hard", "all_out"},
	Legs:   {"fresh", "normal", "heavy", "dead"},
	Plan:   {"more", "as_planned", "cut_short", "couldnt"},
	Pain:   {"none", "niggle", "pain"},
	Fuel:   {"good", "low_energy", "fuelled_badly"},
}

// BodyParts are where a niggle or pain can be. Their words match the ones
// coach.Recurring counts in the athlete's notes.
var BodyParts = []string{"knee", "back", "hip", "hamstring", "calf", "quads", "shoulder", "neck", "ankle", "foot", "wrist", "elbow", "other"}

var partWords = map[string]string{"back": "lower back", "other": "somewhere else"}

// PartWord is how a body part reads in prose.
func PartWord(p string) string {
	if w, ok := partWords[p]; ok {
		return w
	}
	return p
}

// MaxNotes is the longest note, in characters.
const MaxNotes = 500

// Workout is what decides which questions fit.
type Workout struct {
	Session  bool // a lifting session logged in the app
	Ride     bool
	Minutes  int
	Planned  bool // a plan was set for it (a lift is always planned; a ride, if one was planned that day)
	Recovery bool // an easy spin: planned as recovery, or ridden very easy
}

// Questions are the ones that fit w, in order: a lifting session gets
// effort, legs, plan and pain; a recovery spin only effort and legs; fuel
// is for rides and anything over an hour.
func Questions(w Workout) []string {
	if w.Ride && w.Recovery {
		return []string{Effort, Legs}
	}
	q := []string{Effort, Legs}
	if w.Session || w.Planned {
		q = append(q, Plan)
	}
	q = append(q, Pain)
	if w.Ride || w.Minutes >= 60 {
		q = append(q, Fuel)
	}
	return q
}

// Check tidies c and says what's wrong with it, if anything: every answer
// one of its question's values or empty, body parts only with a niggle or
// pain.
func Check(c *store.CheckIn) string {
	for q, v := range map[string]*string{Effort: &c.Effort, Legs: &c.Legs, Plan: &c.Plan, Pain: &c.Pain, Fuel: &c.Fuel} {
		*v = strings.ToLower(strings.TrimSpace(*v))
		if *v != "" && !slices.Contains(Answers[q], *v) {
			return q + " must be one of " + strings.Join(Answers[q], ", ") + ", or empty"
		}
	}
	var where []string
	for _, p := range c.Where {
		p = strings.ToLower(strings.TrimSpace(p))
		if !slices.Contains(BodyParts, p) {
			return "where must be among " + strings.Join(BodyParts, ", ")
		}
		if !slices.Contains(where, p) {
			where = append(where, p)
		}
	}
	if len(where) > 4 {
		return "at most 4 body parts"
	}
	if c.Pain != "niggle" && c.Pain != "pain" {
		where = nil
	}
	c.Where = where
	c.Notes = strings.TrimSpace(c.Notes)
	if utf8.RuneCountInString(c.Notes) > MaxNotes {
		return "notes must be at most 500 characters"
	}
	if c.Skipped && Answered(*c) {
		c.Skipped = false // answering un-skips
	}
	return ""
}

// Answered says whether any question has an answer, or there's a note.
func Answered(c store.CheckIn) bool {
	return c.Effort != "" || c.Legs != "" || c.Plan != "" || c.Pain != "" || c.Fuel != "" || c.Notes != ""
}

// rpe is each effort on the CR-10 scale session RPE uses.
var rpe = map[string]int{"easy": 2, "moderate": 4, "hard": 6, "very_hard": 8, "all_out": 10}

// SRPE is Foster's session RPE load: effort on the CR-10 scale times
// minutes. 0 when effort wasn't answered.
func SRPE(c store.CheckIn, minutes int) int {
	return rpe[c.Effort] * minutes
}

var words = map[string]string{
	"very_hard": "very hard", "all_out": "all out",
	"more": "did more than planned", "as_planned": "as planned", "cut_short": "cut short", "couldnt": "couldn't do it",
	"none": "nothing hurting", "low_energy": "low on energy", "fuelled_badly": "fuelled badly",
}

func word(v string) string {
	if w, ok := words[v]; ok {
		return w
	}
	return v
}

// Words is c in plain words, or nil when nothing was answered.
func Words(c store.CheckIn) *coach.Felt {
	if !Answered(c) {
		return nil
	}
	f := &coach.Felt{Effort: word(c.Effort), Legs: word(c.Legs), Plan: word(c.Plan), Fuel: word(c.Fuel), Notes: c.Notes}
	switch c.Pain {
	case "none":
		f.Pain = word("none")
	case "niggle", "pain":
		what := "a niggle"
		if c.Pain == "pain" {
			what = "pain that changed the workout"
		}
		var parts []string
		for _, p := range c.Where {
			parts = append(parts, PartWord(p))
		}
		if len(parts) > 0 {
			what += ": " + strings.Join(parts, ", ")
		}
		f.Pain = what
	}
	return f
}

// Line is a check-in in a few words: "very hard, legs heavy, niggle: knee".
func Line(c store.CheckIn) string {
	f := Words(c)
	if f == nil {
		return ""
	}
	var parts []string
	for _, p := range []struct{ v, pre string }{{f.Effort, ""}, {f.Legs, "legs "}, {f.Plan, ""}, {f.Pain, ""}, {f.Fuel, "fuel "}} {
		if p.v != "" && p.v != "nothing hurting" {
			parts = append(parts, p.pre+p.v)
		}
	}
	return strings.Join(parts, ", ")
}
