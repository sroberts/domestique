package store

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Limits on a template, shared by REST, MCP and the seed files.
const (
	MaxTemplateName = 80
	MaxExercises    = 30
	MaxSetsPerLift  = 20
	MaxNotes        = 500
	MaxRestSeconds  = 900
	MaxReps         = 100
	MaxWeightKg     = 1000
	WeightStepKg    = 0.25
)

var (
	templateIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	circuitRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)
)

// ValidationError is a template the store refuses, with a message fit to
// show the athlete (or Claude) as is.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{fmt.Sprintf(format, a...)} }

// CleanTemplate trims names and notes, drops prescription notes (Change is
// computed per day, never stored), and checks every limit. Every writer of
// templates goes through it.
func CleanTemplate(t Template) (Template, error) {
	t.Name = strings.TrimSpace(t.Name)
	switch {
	case !templateIDRe.MatchString(t.ID):
		return t, invalid("id must be 1-64 letters, digits, dashes or underscores")
	case t.Name == "":
		return t, invalid("name is required")
	case len([]rune(t.Name)) > MaxTemplateName:
		return t, invalid("name must be at most %d characters", MaxTemplateName)
	case len(t.Exercises) == 0:
		return t, invalid("a workout needs at least one exercise")
	case len(t.Exercises) > MaxExercises:
		return t, invalid("a workout can have at most %d exercises", MaxExercises)
	}
	ex := make([]Exercise, len(t.Exercises))
	for i, e := range t.Exercises {
		e.Name = strings.TrimSpace(e.Name)
		e.Notes = strings.TrimSpace(e.Notes)
		e.Circuit = strings.TrimSpace(e.Circuit)
		e.Change = ""
		n := i + 1
		switch {
		case e.Name == "":
			return t, invalid("exercise %d needs a name", n)
		case len([]rune(e.Name)) > MaxTemplateName:
			return t, invalid("exercise %d: name must be at most %d characters", n, MaxTemplateName)
		case len(e.Sets) == 0:
			return t, invalid("%s needs at least one set", e.Name)
		case len(e.Sets) > MaxSetsPerLift:
			return t, invalid("%s: at most %d sets", e.Name, MaxSetsPerLift)
		case e.RestSeconds < 0 || e.RestSeconds > MaxRestSeconds:
			return t, invalid("%s: rest_seconds must be 0-%d", e.Name, MaxRestSeconds)
		case len([]rune(e.Notes)) > MaxNotes:
			return t, invalid("%s: notes must be at most %d characters", e.Name, MaxNotes)
		case e.Circuit != "" && !circuitRe.MatchString(e.Circuit):
			return t, invalid("%s: circuit must be 1-16 letters, digits, dashes or underscores", e.Name)
		}
		e.Sets = append([]TargetSet(nil), e.Sets...)
		for j, s := range e.Sets {
			if s.Reps < 1 || s.Reps > MaxReps {
				return t, invalid("%s set %d: reps must be 1-%d", e.Name, j+1, MaxReps)
			}
			if err := checkWeight(s.WeightKg); err != nil {
				return t, invalid("%s set %d: %s", e.Name, j+1, err)
			}
		}
		ex[i] = e
	}
	if err := checkCircuits(ex); err != nil {
		return t, err
	}
	t.Exercises = ex
	return t, nil
}

// Circuit is a run of adjacent exercises sharing a circuit key: [Start, End).
type Circuit struct {
	Key        string
	Start, End int
}

// Rounds is how many times the circuit goes round: every member has one
// set per round.
func (c Circuit) Rounds(ex []Exercise) int { return len(ex[c.Start].Sets) }

// Circuits finds the circuits in a list of exercises, in order.
func Circuits(ex []Exercise) []Circuit {
	var out []Circuit
	for i := 0; i < len(ex); {
		if ex[i].Circuit == "" {
			i++
			continue
		}
		j := i + 1
		for j < len(ex) && ex[j].Circuit == ex[i].Circuit {
			j++
		}
		out = append(out, Circuit{ex[i].Circuit, i, j})
		i = j
	}
	return out
}

// checkCircuits requires each circuit to be one unbroken run of at least two
// exercises with the same number of sets.
func checkCircuits(ex []Exercise) error {
	seen := map[string]bool{}
	for _, c := range Circuits(ex) {
		if seen[c.Key] {
			return invalid("circuit %s: its exercises must be next to each other", c.Key)
		}
		seen[c.Key] = true
		if c.End-c.Start < 2 {
			return invalid("circuit %s needs at least two exercises", c.Key)
		}
		n := c.Rounds(ex)
		for _, e := range ex[c.Start+1 : c.End] {
			if len(e.Sets) != n {
				return invalid("circuit %s: every exercise needs the same number of sets (%s has %d, %s has %d)",
					c.Key, ex[c.Start].Name, n, e.Name, len(e.Sets))
			}
		}
	}
	return nil
}

// CircuitOf returns the circuit exercise i belongs to, if any.
func CircuitOf(ex []Exercise, i int) (Circuit, bool) {
	for _, c := range Circuits(ex) {
		if i >= c.Start && i < c.End {
			return c, true
		}
	}
	return Circuit{}, false
}

func checkWeight(w float64) error {
	if math.IsNaN(w) || w < 0 || w > MaxWeightKg {
		return fmt.Errorf("weight_kg must be 0-%d", MaxWeightKg)
	}
	if q := w / WeightStepKg; math.Abs(q-math.Round(q)) > 1e-9 {
		return fmt.Errorf("weight_kg must be in steps of %g kg", WeightStepKg)
	}
	return nil
}

// DeleteTemplate removes a template and every plan that names it (plans
// reference templates, and a plan without its template means nothing).
// Sessions keep their own copy of the plan, so history is untouched.
func (s *Store) DeleteTemplate(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	if _, err := tx.ExecContext(ctx, `DELETE FROM plans WHERE template_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM templates WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// PlannedDates lists the dates on or after from (YYYY-MM-DD) that a template
// is planned for, soonest first.
func (s *Store) PlannedDates(ctx context.Context, templateID, from string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT date FROM plans WHERE template_id = ? AND date >= ? ORDER BY date`, templateID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LastDone maps each template to when a finished session of it last started.
func (s *Store) LastDone(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT template_id, MAX(started_at) FROM sessions WHERE finished_at IS NOT NULL GROUP BY template_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = at
	}
	return out, rows.Err()
}
