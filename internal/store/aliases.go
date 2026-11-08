package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// Exercise aliases count one exercise name as another ("BB bench" as
// "Bench Press"), so history, progression and PRs stay in one line. They
// live in sync_state; the names in sets and templates are never rewritten,
// so undoing one is only a flag.

// AliasPrefix is the sync_state key prefix, followed by the alias's
// lower-case name.
const AliasPrefix = "exercise-alias:"

// Alias is one name counted as another.
type Alias struct {
	Name   string  `json:"name"` // as the athlete wrote it
	To     string  `json:"to"`   // the name it counts as
	P      float64 `json:"p,omitempty"`
	At     int64   `json:"at"`               // epoch ms
	Undone bool    `json:"undone,omitempty"` // the athlete said no; kept so it isn't asked again
}

func aliasKey(name string) string { return AliasPrefix + strings.ToLower(strings.TrimSpace(name)) }

// PutAlias stores or replaces the alias for a.Name.
func (s *Store) PutAlias(ctx context.Context, a Alias) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return s.SetSyncState(ctx, aliasKey(a.Name), string(b))
}

// GetAlias returns the alias stored for name, undone or not.
func (s *Store) GetAlias(ctx context.Context, name string) (Alias, error) {
	v, err := s.SyncState(ctx, aliasKey(name))
	if err != nil {
		return Alias{}, err
	}
	var a Alias
	return a, json.Unmarshal([]byte(v), &a)
}

// Aliases lists every stored alias, undone ones included, by name.
func (s *Store) Aliases(ctx context.Context) ([]Alias, error) {
	m, err := s.SyncStatePrefix(ctx, AliasPrefix)
	if err != nil {
		return nil, err
	}
	out := []Alias{}
	for _, v := range m {
		var a Alias
		if json.Unmarshal([]byte(v), &a) == nil && a.Name != "" && a.To != "" {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Canon maps lower-case exercise names to the name they count as.
type Canon map[string]string

// Of is the name n counts as: itself unless an alias says otherwise.
func (c Canon) Of(n string) string {
	if to, ok := c[strings.ToLower(strings.TrimSpace(n))]; ok {
		return to
	}
	return n
}

// Names lists every lower-case name that counts as the same as n, n's own
// included.
func (c Canon) Names(n string) []string {
	to := strings.ToLower(c.Of(n))
	out := []string{to}
	for k, v := range c {
		if strings.ToLower(v) == to && k != to {
			out = append(out, k)
		}
	}
	sort.Strings(out[1:])
	return out
}

// Canon returns the athlete's live aliases, followed through chains
// (a → b → c counts a as c) and safe against loops.
func (s *Store) Canon(ctx context.Context) (Canon, error) {
	as, err := s.Aliases(ctx)
	if err != nil {
		return nil, err
	}
	direct := map[string]string{}
	for _, a := range as {
		if !a.Undone {
			direct[strings.ToLower(strings.TrimSpace(a.Name))] = a.To
		}
	}
	c := Canon{}
	for k, to := range direct {
		seen := map[string]bool{k: true}
		for {
			next, ok := direct[strings.ToLower(to)]
			if !ok || seen[strings.ToLower(to)] {
				break
			}
			seen[strings.ToLower(to)] = true
			to = next
		}
		if strings.ToLower(to) != k {
			c[k] = to
		}
	}
	return c, nil
}

// ExerciseName is one name the athlete has used, with when it was first used.
type ExerciseName struct {
	Name  string // as first written
	First int64  // epoch ms of its first set, or of the template it's in
}

// ExerciseNames lists every exercise name in finished sessions and
// templates, case-insensitively distinct, oldest first. Aliases aren't
// applied.
func (s *Store) ExerciseNames(ctx context.Context) ([]ExerciseName, error) {
	byKey := map[string]ExerciseName{}
	add := func(n string, at int64) {
		k := strings.ToLower(strings.TrimSpace(n))
		if k == "" {
			return
		}
		if e, ok := byKey[k]; !ok || at < e.First {
			byKey[k] = ExerciseName{Name: strings.TrimSpace(n), First: at}
		}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT x.exercise_name, MIN(x.started_at) FROM sets x JOIN sessions s ON s.id = x.session_id
		WHERE s.finished_at IS NOT NULL GROUP BY lower(x.exercise_name)`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		var at int64
		if err := rows.Scan(&n, &at); err != nil {
			rows.Close()
			return nil, err
		}
		add(n, at)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	ts, err := s.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		for _, e := range t.Exercises {
			add(e.Name, t.UpdatedAt)
		}
	}
	out := make([]ExerciseName, 0, len(byKey))
	for _, e := range byKey {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].First != out[j].First {
			return out[i].First < out[j].First
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
