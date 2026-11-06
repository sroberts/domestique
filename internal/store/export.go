package store

import (
	"context"
	"database/sql"
	"errors"
)

// RawFile is an original file as received, archived under the storage dir.
type RawFile struct {
	SHA256 string
	Path   string // relative to the storage dir
	Size   int64
	Format string
	Origin string
}

// GetRaw looks up an archived original by its hash.
func (s *Store) GetRaw(ctx context.Context, sha string) (RawFile, error) {
	var f RawFile
	err := s.db.QueryRowContext(ctx, `SELECT sha256, path, size, format, origin FROM raw_files WHERE sha256 = ?`, sha).
		Scan(&f.SHA256, &f.Path, &f.Size, &f.Format, &f.Origin)
	if errors.Is(err, sql.ErrNoRows) {
		return RawFile{}, ErrNotFound
	}
	return f, err
}

// SessionsBetween returns finished sessions started in [from, to] (epoch ms),
// oldest first.
func (s *Store) SessionsBetween(ctx context.Context, from, to int64) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM sessions
		WHERE finished_at IS NOT NULL AND started_at BETWEEN ? AND ? ORDER BY started_at`, from, to)
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
