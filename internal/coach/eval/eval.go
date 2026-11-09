// Package eval puts a fixed set of cases to the operator's models and
// scores what Domestique would do with each answer. Run it with `make eval`
// whenever Outpost's models change, to see which questions are safe to act
// on and where each bar (coach.Bar) belongs.
//
// A case either wants an action ("yes", a sport, an effort level) or wants
// nothing done ("no", or ""). What matters most is WRONG: an action taken
// that the case didn't want. A miss, where the model wasn't sure enough to
// act, costs nothing but the feature.
//
// Questions register themselves (Register) from files in this package, and
// their cases live in cases/<question>.json.
package eval

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"domestique/internal/coach"
)

//go:embed cases/*.json
var files embed.FS

// Case is one input and the answer it wants.
type Case struct {
	Name  string          `json:"name"`
	Facts json.RawMessage `json:"facts"`
	// Want is the action wanted: "yes", a label, or a level; "no" or ""
	// for nothing. A case covering several items (sets in one session)
	// writes "id=level,id=level"; an item left out wants nothing.
	Want string `json:"want"`
}

// Lean is what the model leaned to for one item: the action Domestique
// would take past the bar, and the model's probability (or confidence) for
// it. Value "" is no action at any bar.
type Lean struct {
	Value string
	P     float64
}

// Question is one thing Domestique asks a model.
type Question struct {
	Name string
	// Bar is the probability an answer must reach to be acted on.
	Bar float64
	// Ask puts one case to the model, and returns a Lean per item: "" for
	// a single-item case.
	Ask func(ctx context.Context, ai *coach.AI, facts json.RawMessage) (map[string]Lean, error)
}

var questions = map[string]Question{}

// Register adds a question. Its cases are read from cases/<name>.json.
func Register(q Question) { questions[q.Name] = q }

// Names lists the registered questions.
func Names() []string {
	n := make([]string, 0, len(questions))
	for k := range questions {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Cases reads a question's cases.
func Cases(name string) ([]Case, error) {
	b, err := files.ReadFile("cases/" + name + ".json")
	if err != nil {
		return nil, fmt.Errorf("%s: no cases: %w", name, err)
	}
	var cs []Case
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("cases/%s.json: %w", name, err)
	}
	return cs, nil
}

// Verdicts on one item.
const (
	Hit     = "hit"     // acted, as wanted
	Correct = "correct" // left alone, as wanted
	Miss    = "miss"    // wanted an action, not sure enough to take it
	Wrong   = "WRONG"   // acted, and not as wanted
)

// Item is one scored item.
type Item struct {
	Case, ID   string
	Want, Lean string
	P          float64
	Verdict    string
}

// Score is one question's results.
type Score struct {
	Question string
	Bar      float64
	Items    []Item
	Counts   map[string]int
	Errors   []string
	// SafeBar is the lowest bar with no WRONG item, and SafeHits the hits
	// at that bar. SafeBar above 1 means some wrong answer was certain.
	SafeBar  float64
	SafeHits int
}

func wants(c Case) map[string]string {
	w := map[string]string{}
	if !strings.Contains(c.Want, "=") {
		w[""] = c.Want
		return w
	}
	for _, kv := range strings.Split(c.Want, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(kv), "=")
		w[k] = v
	}
	return w
}

func verdict(want string, l Lean, bar float64) string {
	acts := l.Value != "" && l.P >= bar
	should := want != "" && want != "no"
	switch {
	case acts && should && l.Value == want:
		return Hit
	case acts:
		return Wrong
	case should:
		return Miss
	}
	return Correct
}

// Run scores one question.
func Run(ctx context.Context, ai *coach.AI, name string) (Score, error) {
	q, ok := questions[name]
	if !ok {
		return Score{}, fmt.Errorf("no question %q; have %s", name, strings.Join(Names(), ", "))
	}
	cs, err := Cases(name)
	if err != nil {
		return Score{}, err
	}
	sc := Score{Question: name, Bar: q.Bar, Counts: map[string]int{}}
	for _, c := range cs {
		leans, err := q.Ask(ctx, ai, c.Facts)
		if err != nil {
			if ctx.Err() != nil {
				return sc, ctx.Err()
			}
			sc.Errors = append(sc.Errors, c.Name+": "+err.Error())
			continue
		}
		w := wants(c)
		ids := make([]string, 0, len(leans))
		for id := range leans {
			ids = append(ids, id)
		}
		for id := range w {
			if _, ok := leans[id]; !ok {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range slices.Compact(ids) {
			l := leans[id]
			v := verdict(w[id], l, q.Bar)
			sc.Items = append(sc.Items, Item{Case: c.Name, ID: id, Want: w[id], Lean: l.Value, P: l.P, Verdict: v})
			sc.Counts[v]++
		}
	}
	// The lowest bar no wrong lean reaches.
	sc.SafeBar = 0
	for _, it := range sc.Items {
		if it.Lean != "" && verdict(it.Want, Lean{it.Lean, it.P}, 0) == Wrong {
			sc.SafeBar = max(sc.SafeBar, it.P+0.01)
		}
	}
	for _, it := range sc.Items {
		if verdict(it.Want, Lean{it.Lean, it.P}, sc.SafeBar) == Hit {
			sc.SafeHits++
		}
	}
	return sc, nil
}

// Print writes a score as a table and a summary line.
func Print(w io.Writer, sc Score, verbose bool) {
	fmt.Fprintf(w, "\n== %s (bar %.2f)\n", sc.Question, sc.Bar)
	for _, it := range sc.Items {
		if !verbose && it.Verdict == Correct {
			continue
		}
		name := it.Case
		if it.ID != "" {
			name += " / " + it.ID
		}
		want := it.Want
		if want == "" {
			want = "-"
		}
		lean := it.Lean
		if lean == "" {
			lean = "-"
		}
		fmt.Fprintf(w, "  %-7s %-52s want %-9s leans %-12s %.2f\n", it.Verdict, clip(name, 52), want, lean, it.P)
	}
	for _, e := range sc.Errors {
		fmt.Fprintf(w, "  error   %s\n", e)
	}
	total := len(sc.Items)
	fmt.Fprintf(w, "  %d items: %d hit, %d correct, %d miss, %d WRONG", total,
		sc.Counts[Hit], sc.Counts[Correct], sc.Counts[Miss], sc.Counts[Wrong])
	if sc.SafeBar <= 1 {
		fmt.Fprintf(w, "; lowest safe bar %.2f (%d hits there)\n", sc.SafeBar, sc.SafeHits)
	} else {
		fmt.Fprintf(w, "; no safe bar: a wrong answer was certain\n")
	}
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
