// Package store owns Domestique's SQLite database. Everything it writes lives
// under the Outpost storage directory, so `outpost backup` captures it and
// `outpost restore` can replace it wholesale between runs.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database under storageDir and runs
// migrations. It must complete before the service reports healthy.
func Open(ctx context.Context, storageDir string) (*Store, error) {
	dir := filepath.Join(storageDir, "db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	path := filepath.Join(dir, "domestique.sqlite")
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// One writer keeps SQLite simple for a single-athlete service.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	// The database holds OAuth tokens: owner-only, whatever the umask.
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(f, 0o600)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close checkpoints the WAL and closes the database.
func (s *Store) Close() error {
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

// ---- Types ------------------------------------------------------------------

// TargetSet is one planned set.
type TargetSet struct {
	Reps     int     `json:"reps"`
	WeightKg float64 `json:"weight_kg"`
}

// Exercise is one planned exercise inside a template.
type Exercise struct {
	Name        string      `json:"name"`
	Sets        []TargetSet `json:"sets"`
	RestSeconds int         `json:"rest_seconds"`
	Notes       string      `json:"notes,omitempty"`
	// Change explains how today's targets differ from the template, when a
	// prescription adjusted them ("Up 2.5 kg: every set hit 6 on Sat").
	Change string `json:"change,omitempty"`
}

// Template is a reusable strength workout.
type Template struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Exercises []Exercise `json:"exercises"`
	UpdatedAt int64      `json:"updated_at"`
}

// Recovery is a morning recovery reading (Whoop).
type Recovery struct {
	Date      string  `json:"date"`
	Score     int     `json:"score"`
	HRVms     float64 `json:"hrv_ms"`
	RestingHR int     `json:"resting_hr"`
	Source    string  `json:"source"`
}

// Session is one strength session as logged on the device.
type Session struct {
	ID         string     `json:"id"`
	TemplateID string     `json:"template_id"`
	Name       string     `json:"name"`
	Plan       []Exercise `json:"plan"`
	StartedAt  int64      `json:"started_at"`
	FinishedAt *int64     `json:"finished_at"`
	Strain     *float64   `json:"strain"`
	UpdatedAt  int64      `json:"updated_at"`
}

// Set is one performed set. Timestamps are epoch milliseconds from the device
// clock at logging time.
type Set struct {
	ID             string  `json:"id"`
	SessionID      string  `json:"session_id"`
	ExerciseIndex  int     `json:"exercise_index"`
	ExerciseName   string  `json:"exercise_name"`
	SetIndex       int     `json:"set_index"`
	TargetReps     int     `json:"target_reps"`
	TargetWeightKg float64 `json:"target_weight_kg"`
	Reps           int     `json:"reps"`
	WeightKg       float64 `json:"weight_kg"`
	StartedAt      int64   `json:"started_at"`
	EndedAt        int64   `json:"ended_at"`
	UpdatedAt      int64   `json:"updated_at"`
}

// HRSample is one heart rate reading.
type HRSample struct {
	T   int64 `json:"t"`
	BPM int   `json:"bpm"`
}

// ---- Templates ----------------------------------------------------------------

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, exercises, updated_at FROM templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTemplate(ctx context.Context, id string) (Template, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, exercises, updated_at FROM templates WHERE id = ?`, id)
	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	return t, err
}

func (s *Store) PutTemplate(ctx context.Context, t Template) error {
	ex, err := json.Marshal(t.Exercises)
	if err != nil {
		return err
	}
	if t.UpdatedAt == 0 {
		t.UpdatedAt = time.Now().UnixMilli()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO templates (id, name, exercises, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, exercises = excluded.exercises, updated_at = excluded.updated_at`,
		t.ID, t.Name, string(ex), t.UpdatedAt)
	return err
}

func (s *Store) CountTemplates(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM templates`).Scan(&n)
	return n, err
}

type scanner interface{ Scan(dest ...any) error }

func scanTemplate(r scanner) (Template, error) {
	var t Template
	var ex string
	if err := r.Scan(&t.ID, &t.Name, &ex, &t.UpdatedAt); err != nil {
		return Template{}, err
	}
	if err := json.Unmarshal([]byte(ex), &t.Exercises); err != nil {
		return Template{}, fmt.Errorf("template %s: %w", t.ID, err)
	}
	return t, nil
}

// ---- Plans ------------------------------------------------------------------------

// GetPlan returns the template planned for date (YYYY-MM-DD).
func (s *Store) GetPlan(ctx context.Context, date string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT template_id FROM plans WHERE date = ?`, date).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) PutPlan(ctx context.Context, date, templateID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO plans (date, template_id) VALUES (?, ?)
		ON CONFLICT(date) DO UPDATE SET template_id = excluded.template_id`, date, templateID)
	return err
}

func (s *Store) DeletePlan(ctx context.Context, date string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plans WHERE date = ?`, date)
	return err
}

// ---- Recovery ----------------------------------------------------------------------

func (s *Store) GetRecovery(ctx context.Context, date string) (Recovery, error) {
	var r Recovery
	err := s.db.QueryRowContext(ctx,
		`SELECT date, score, hrv_ms, resting_hr, source FROM recovery WHERE date = ?`, date).
		Scan(&r.Date, &r.Score, &r.HRVms, &r.RestingHR, &r.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return Recovery{}, ErrNotFound
	}
	return r, err
}

func (s *Store) PutRecovery(ctx context.Context, r Recovery) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO recovery (date, score, hrv_ms, resting_hr, source) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET score = excluded.score, hrv_ms = excluded.hrv_ms,
			resting_hr = excluded.resting_hr, source = excluded.source`,
		r.Date, r.Score, r.HRVms, r.RestingHR, r.Source)
	return err
}

// ---- Sessions ----------------------------------------------------------------------

// PutSession upserts a session. Older writes (by updated_at) never overwrite
// newer ones, so a replayed offline queue is harmless.
func (s *Store) PutSession(ctx context.Context, x Session) error {
	plan, err := json.Marshal(x.Plan)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, template_id, name, plan, started_at, finished_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			template_id = excluded.template_id, name = excluded.name, plan = excluded.plan,
			started_at = excluded.started_at, finished_at = excluded.finished_at,
			updated_at = excluded.updated_at
		WHERE excluded.updated_at >= sessions.updated_at`,
		x.ID, x.TemplateID, x.Name, string(plan), x.StartedAt, x.FinishedAt, x.UpdatedAt)
	return err
}

func (s *Store) SetStrain(ctx context.Context, sessionID string, strain float64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET strain = ? WHERE id = ?`, strain, sessionID)
	return err
}

const sessionCols = `id, template_id, name, plan, started_at, finished_at, strain, updated_at`

func scanSession(r scanner) (Session, error) {
	var x Session
	var plan string
	var fin sql.NullInt64
	var strain sql.NullFloat64
	if err := r.Scan(&x.ID, &x.TemplateID, &x.Name, &plan, &x.StartedAt, &fin, &strain, &x.UpdatedAt); err != nil {
		return Session{}, err
	}
	if fin.Valid {
		x.FinishedAt = &fin.Int64
	}
	if strain.Valid {
		x.Strain = &strain.Float64
	}
	if err := json.Unmarshal([]byte(plan), &x.Plan); err != nil {
		return Session{}, fmt.Errorf("session %s plan: %w", x.ID, err)
	}
	return x, nil
}

func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	x, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return x, err
}

// RecentSessions returns finished sessions, newest first.
func (s *Store) RecentSessions(ctx context.Context, limit int) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions
		WHERE finished_at IS NOT NULL ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ---- Sets ----------------------------------------------------------------------------

func (s *Store) PutSet(ctx context.Context, x Set) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sets (id, session_id, exercise_index, exercise_name, set_index,
			target_reps, target_weight_kg, reps, weight_kg, started_at, ended_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			exercise_index = excluded.exercise_index, exercise_name = excluded.exercise_name,
			set_index = excluded.set_index, target_reps = excluded.target_reps,
			target_weight_kg = excluded.target_weight_kg, reps = excluded.reps,
			weight_kg = excluded.weight_kg, started_at = excluded.started_at,
			ended_at = excluded.ended_at, updated_at = excluded.updated_at
		WHERE excluded.updated_at >= sets.updated_at`,
		x.ID, x.SessionID, x.ExerciseIndex, x.ExerciseName, x.SetIndex,
		x.TargetReps, x.TargetWeightKg, x.Reps, x.WeightKg, x.StartedAt, x.EndedAt, x.UpdatedAt)
	return err
}

func (s *Store) SessionSets(ctx context.Context, sessionID string) ([]Set, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, exercise_index, exercise_name, set_index, target_reps,
			target_weight_kg, reps, weight_kg, started_at, ended_at, updated_at
		FROM sets WHERE session_id = ? ORDER BY started_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Set{}
	for rows.Next() {
		var x Set
		if err := rows.Scan(&x.ID, &x.SessionID, &x.ExerciseIndex, &x.ExerciseName, &x.SetIndex,
			&x.TargetReps, &x.TargetWeightKg, &x.Reps, &x.WeightKg, &x.StartedAt, &x.EndedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ---- Heart rate -------------------------------------------------------------------------

// HR returns heart rate samples in [from, to] (epoch ms), oldest first.
func (s *Store) HR(ctx context.Context, from, to int64) ([]HRSample, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, bpm FROM hr_samples WHERE ts BETWEEN ? AND ? ORDER BY ts`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HRSample{}
	for rows.Next() {
		var h HRSample
		if err := rows.Scan(&h.T, &h.BPM); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// PutHR inserts samples from one source, ignoring duplicates.
func (s *Store) PutHR(ctx context.Context, source string, samples []HRSample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO hr_samples (source, ts, bpm) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, h := range samples {
		if _, err := stmt.ExecContext(ctx, source, h.T, h.BPM); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ExerciseHistory returns the sets of the most recent finished sessions that
// included the named exercise, newest session first (up to limit sessions).
func (s *Store) ExerciseHistory(ctx context.Context, name string, limit int) ([][]Set, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT x.id, x.session_id, x.exercise_index, x.exercise_name, x.set_index, x.target_reps,
			x.target_weight_kg, x.reps, x.weight_kg, x.started_at, x.ended_at, x.updated_at
		FROM sets x JOIN sessions s ON s.id = x.session_id
		WHERE s.finished_at IS NOT NULL AND lower(x.exercise_name) = lower(?)
			AND x.session_id IN (
				SELECT s2.id FROM sessions s2 JOIN sets x2 ON x2.session_id = s2.id
				WHERE s2.finished_at IS NOT NULL AND lower(x2.exercise_name) = lower(?)
				GROUP BY s2.id ORDER BY s2.started_at DESC LIMIT ?)
		ORDER BY s.started_at DESC, x.started_at`, name, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]Set
	cur := ""
	for rows.Next() {
		var x Set
		if err := rows.Scan(&x.ID, &x.SessionID, &x.ExerciseIndex, &x.ExerciseName, &x.SetIndex, &x.TargetReps,
			&x.TargetWeightKg, &x.Reps, &x.WeightKg, &x.StartedAt, &x.EndedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		if x.SessionID != cur {
			out = append(out, nil)
			cur = x.SessionID
		}
		out[len(out)-1] = append(out[len(out)-1], x)
	}
	return out, rows.Err()
}

// AllSets returns every logged set, oldest first (for lift PRs).
func (s *Store) AllSets(ctx context.Context) ([]Set, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT x.id, x.session_id, x.exercise_index, x.exercise_name, x.set_index, x.target_reps,
			x.target_weight_kg, x.reps, x.weight_kg, x.started_at, x.ended_at, x.updated_at
		FROM sets x JOIN sessions s ON s.id = x.session_id WHERE s.finished_at IS NOT NULL
		ORDER BY x.started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Set{}
	for rows.Next() {
		var x Set
		if err := rows.Scan(&x.ID, &x.SessionID, &x.ExerciseIndex, &x.ExerciseName, &x.SetIndex, &x.TargetReps,
			&x.TargetWeightKg, &x.Reps, &x.WeightKg, &x.StartedAt, &x.EndedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
