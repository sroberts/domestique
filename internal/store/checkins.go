package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// CheckIn is the athlete's own word on how a workout went: a few one-tap
// answers, each a fixed value ("" when not answered). See internal/checkin
// for the values and which questions a workout gets.
type CheckIn struct {
	Key     string   `json:"-"`
	Effort  string   `json:"effort,omitempty"`
	Legs    string   `json:"legs,omitempty"`
	Plan    string   `json:"plan,omitempty"`
	Pain    string   `json:"pain,omitempty"`
	Where   []string `json:"where,omitempty"` // body parts, with a niggle or pain
	Fuel    string   `json:"fuel,omitempty"`
	Notes   string   `json:"notes,omitempty"`
	Skipped bool     `json:"skipped,omitempty"`
	// WorkoutAt is when the workout started (epoch ms).
	WorkoutAt int64 `json:"workout_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// SessionKey is the check-in key of a session logged in the app.
func SessionKey(sessionID string) string { return "session:" + sessionID }

// CheckInKey is the check-in key of an activity: its session's, if it is
// one logged in the app, else its own ID.
func CheckInKey(a Activity) string {
	if a.SessionID != nil && *a.SessionID != "" {
		return SessionKey(*a.SessionID)
	}
	return a.ID
}

const checkInCols = `key, effort, legs, plan, pain, pain_where, fuel, notes, skipped, workout_at, updated_at`

func scanCheckIn(sc interface{ Scan(...any) error }) (CheckIn, error) {
	var c CheckIn
	var where string
	var skipped int
	err := sc.Scan(&c.Key, &c.Effort, &c.Legs, &c.Plan, &c.Pain, &where, &c.Fuel, &c.Notes, &skipped, &c.WorkoutAt, &c.UpdatedAt)
	if err == nil {
		c.Skipped = skipped != 0
		_ = json.Unmarshal([]byte(where), &c.Where)
	}
	return c, err
}

func (s *Store) GetCheckIn(ctx context.Context, key string) (CheckIn, error) {
	c, err := scanCheckIn(s.db.QueryRowContext(ctx, `SELECT `+checkInCols+` FROM checkins WHERE key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (s *Store) PutCheckIn(ctx context.Context, c CheckIn) error {
	where, _ := json.Marshal(append([]string{}, c.Where...))
	skipped := 0
	if c.Skipped {
		skipped = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO checkins (`+checkInCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET effort = excluded.effort, legs = excluded.legs, plan = excluded.plan,
			pain = excluded.pain, pain_where = excluded.pain_where, fuel = excluded.fuel, notes = excluded.notes,
			skipped = excluded.skipped, workout_at = excluded.workout_at, updated_at = excluded.updated_at`,
		c.Key, c.Effort, c.Legs, c.Plan, c.Pain, string(where), c.Fuel, c.Notes, skipped, c.WorkoutAt, c.UpdatedAt)
	return err
}

// CheckInsBetween lists check-ins on workouts started from one time to
// another (epoch ms, inclusive), oldest first.
func (s *Store) CheckInsBetween(ctx context.Context, from, to int64) ([]CheckIn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+checkInCols+` FROM checkins WHERE workout_at >= ? AND workout_at <= ? ORDER BY workout_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CheckIn{}
	for rows.Next() {
		c, err := scanCheckIn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CheckInKeys says which of keys have a check-in, answered or skipped.
func (s *Store) CheckInKeys(ctx context.Context, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM checkins WHERE key IN (?`+repeat(",?", len(keys)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

func moveCheckIn(ctx context.Context, tx *sql.Tx, from, to string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE checkins SET key = ?2 WHERE key = ?1
		AND NOT EXISTS (SELECT 1 FROM checkins WHERE key = ?2)`, from, to); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM checkins WHERE key = ?`, from)
	return err
}
