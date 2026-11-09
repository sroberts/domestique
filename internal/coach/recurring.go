package coach

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// What the athlete keeps writing about: a body part, or illness, named again
// and again in their own notes. No model is asked; this only counts what the
// tags pass already read.

// Mention is one workout's words that may name a complaint.
type Mention struct {
	At   time.Time
	Text string   // the tag note, and the description when tags say sore or injury
	Tags []string // tags read from the description
}

// Complaint is something that keeps coming up.
type Complaint struct {
	What  string    `json:"what"`  // "knee", "lower back", "feeling ill"
	Times int       `json:"times"` // workouts it came up in, on different days
	Last  time.Time `json:"-"`
}

// String is how the week note and Trends put it: "knee, 4 times in 3 weeks".
func (c Complaint) String() string {
	return fmt.Sprintf("%s, %d times in %d weeks", c.What, c.Times, RecurWeeks)
}

// RecurWeeks is the window looked back over, and RecurTimes how many
// different days must mention the same thing before it counts.
const (
	RecurWeeks = 3
	RecurTimes = 3
)

var bodyParts = []struct {
	what string
	re   *regexp.Regexp
}{
	{"knee", regexp.MustCompile(`(?i)\bknees?\b`)},
	{"lower back", regexp.MustCompile(`(?i)\b(lower|upper|my) back\b|\bback (pain|hurt|hurts|hurting|ache|aches|aching|sore|tight|spasms?)\b|\bsore back\b`)},
	{"hip", regexp.MustCompile(`(?i)\bhips?\b`)},
	{"hamstring", regexp.MustCompile(`(?i)\bhamstrings?\b|\bhammies\b`)},
	{"calf", regexp.MustCompile(`(?i)\bcalf\b|\bcalves\b`)},
	{"quads", regexp.MustCompile(`(?i)\bquads?\b`)},
	{"shoulder", regexp.MustCompile(`(?i)\bshoulders?\b`)},
	{"neck", regexp.MustCompile(`(?i)\bneck\b`)},
	{"ankle", regexp.MustCompile(`(?i)\bankles?\b`)},
	{"achilles", regexp.MustCompile(`(?i)\bachilles\b`)},
	{"foot", regexp.MustCompile(`(?i)\bfoot\b|\bfeet\b|\btoes?\b`)},
	{"wrist", regexp.MustCompile(`(?i)\bwrists?\b`)},
	{"elbow", regexp.MustCompile(`(?i)\belbows?\b`)},
	{"hands", regexp.MustCompile(`(?i)\bhands?\b|\bfingers?\b`)},
	{"glutes", regexp.MustCompile(`(?i)\bglutes?\b`)},
	{"IT band", regexp.MustCompile(`(?i)\bit ?band\b|\bitb\b`)},
	{"groin", regexp.MustCompile(`(?i)\bgroin\b`)},
	{"shins", regexp.MustCompile(`(?i)\bshins?\b`)},
}

// Recurring finds what came up on at least RecurTimes different days in the
// RecurWeeks before end, most often first. Illness counts from the "ill" tag.
func Recurring(ms []Mention, end time.Time) []Complaint {
	from := end.AddDate(0, 0, -7*RecurWeeks)
	days := map[string]map[string]bool{}
	last := map[string]time.Time{}
	add := func(what string, at time.Time) {
		if days[what] == nil {
			days[what] = map[string]bool{}
		}
		days[what][at.Format("2006-01-02")] = true
		if at.After(last[what]) {
			last[what] = at
		}
	}
	for _, m := range ms {
		if m.At.Before(from) || !m.At.Before(end) {
			continue
		}
		for _, b := range bodyParts {
			if b.re.MatchString(m.Text) {
				add(b.what, m.At)
			}
		}
		for _, t := range m.Tags {
			if t == "ill" {
				add("feeling ill", m.At)
			}
		}
	}
	var out []Complaint
	for what, d := range days {
		if len(d) >= RecurTimes {
			out = append(out, Complaint{What: what, Times: len(d), Last: last[what]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Times != out[j].Times {
			return out[i].Times > out[j].Times
		}
		return out[i].What < out[j].What
	})
	return out
}
