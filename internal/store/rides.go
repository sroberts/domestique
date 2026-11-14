package store

import (
	"context"
	"database/sql"
	"errors"
)

// RidePlan is a bike ride planned for a day: what to ride and how hard. A
// day has at most one, beside any lifting plan.
type RidePlan struct {
	Date        string `json:"date"` // YYYY-MM-DD, athlete's local date
	Name        string `json:"name"`
	Kind        string `json:"kind"` // indoor | outdoor
	DurationMin int    `json:"duration_min"`
	TSS         int    `json:"tss"`       // 0 = no target
	Intensity   string `json:"intensity"` // recovery | endurance | tempo | threshold | vo2; "" = unsaid
	Notes       string `json:"notes"`
	UpdatedAt   int64  `json:"updated_at"`
}

const ridePlanCols = `date, name, kind, duration_min, tss, intensity, notes, updated_at`

func scanRidePlan(sc interface{ Scan(...any) error }) (RidePlan, error) {
	var p RidePlan
	err := sc.Scan(&p.Date, &p.Name, &p.Kind, &p.DurationMin, &p.TSS, &p.Intensity, &p.Notes, &p.UpdatedAt)
	return p, err
}

func (s *Store) GetRidePlan(ctx context.Context, date string) (RidePlan, error) {
	p, err := scanRidePlan(s.db.QueryRowContext(ctx, `SELECT `+ridePlanCols+` FROM ride_plans WHERE date = ?`, date))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) PutRidePlan(ctx context.Context, p RidePlan) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ride_plans (`+ridePlanCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET name = excluded.name, kind = excluded.kind,
			duration_min = excluded.duration_min, tss = excluded.tss, intensity = excluded.intensity,
			notes = excluded.notes, updated_at = excluded.updated_at`,
		p.Date, p.Name, p.Kind, p.DurationMin, p.TSS, p.Intensity, p.Notes, p.UpdatedAt)
	return err
}

func (s *Store) DeleteRidePlan(ctx context.Context, date string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ride_plans WHERE date = ?`, date)
	return err
}

// RidePlansBetween lists the rides planned from one date to another
// (YYYY-MM-DD, inclusive), soonest first.
func (s *Store) RidePlansBetween(ctx context.Context, from, to string) ([]RidePlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+ridePlanCols+` FROM ride_plans WHERE date >= ? AND date <= ? ORDER BY date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RidePlan{}
	for rows.Next() {
		p, err := scanRidePlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
