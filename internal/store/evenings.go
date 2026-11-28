package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Evening is what the athlete logged for one evening. A row means the
// evening was logged: no items is a clear evening, not an unknown one.
type Evening struct {
	Date      string   `json:"date"`
	Items     []string `json:"items"`
	UpdatedAt int64    `json:"updated_at"`
}

func (s *Store) PutEvening(ctx context.Context, e Evening) error {
	items, _ := json.Marshal(append([]string{}, e.Items...))
	_, err := s.db.ExecContext(ctx, `INSERT INTO evenings (date, items, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(date) DO UPDATE SET items = excluded.items, updated_at = excluded.updated_at`,
		e.Date, string(items), e.UpdatedAt)
	return err
}

// GetEvening is one evening's log, or ErrNotFound if it wasn't logged.
func (s *Store) GetEvening(ctx context.Context, date string) (Evening, error) {
	var e Evening
	var items string
	err := s.db.QueryRowContext(ctx, `SELECT date, items, updated_at FROM evenings WHERE date = ?`, date).Scan(&e.Date, &items, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return e, err
	}
	e.Items = []string{}
	_ = json.Unmarshal([]byte(items), &e.Items)
	return e, nil
}

// DeleteEvening forgets an evening's log: it's unknown again.
func (s *Store) DeleteEvening(ctx context.Context, date string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM evenings WHERE date = ?`, date)
	return err
}

// EveningRange lists the logged evenings from one date to another.
func (s *Store) EveningRange(ctx context.Context, from, to string) ([]Evening, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT date, items, updated_at FROM evenings WHERE date BETWEEN ? AND ? ORDER BY date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evening{}
	for rows.Next() {
		var e Evening
		var items string
		if err := rows.Scan(&e.Date, &items, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Items = []string{}
		_ = json.Unmarshal([]byte(items), &e.Items)
		out = append(out, e)
	}
	return out, rows.Err()
}
