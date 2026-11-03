package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// Activity is one canonical workout, merged from one or more sources.
type Activity struct {
	ID         string          `json:"id"`
	Sport      string          `json:"sport"`
	Name       string          `json:"name"`
	StartAt    int64           `json:"start_at"`
	ElapsedS   int             `json:"elapsed_s"`
	MovingS    int             `json:"moving_s"`
	DistanceM  float64         `json:"distance_m"`
	Metrics    json.RawMessage `json:"metrics"`
	Route      string          `json:"route"`
	Strain     *float64        `json:"strain"`
	SessionID  *string         `json:"session_id"`
	FTP        int             `json:"ftp"`
	TSS        float64         `json:"tss"`
	Intensity  float64         `json:"intensity"`
	LoadMethod string          `json:"load_method"`
	UpdatedAt  int64           `json:"updated_at"`
}

// Source is one record of an activity from one origin.
type Source struct {
	ID         string          `json:"id"`
	ActivityID string          `json:"activity_id"`
	Source     string          `json:"source"`
	ExternalID string          `json:"external_id"`
	RawSHA256  *string         `json:"raw_sha256"`
	Sport      string          `json:"sport"`
	Name       string          `json:"name"`
	StartAt    int64           `json:"start_at"`
	ElapsedS   int             `json:"elapsed_s"`
	MovingS    int             `json:"moving_s"`
	DistanceM  float64         `json:"distance_m"`
	Device     string          `json:"device"`
	HasPower   bool            `json:"has_power"`
	HasHR      bool            `json:"has_hr"`
	HasGPS     bool            `json:"has_gps"`
	Extra      json.RawMessage `json:"extra"`
	ImportedAt int64           `json:"imported_at"`
}

const activityCols = `id, sport, name, start_at, elapsed_s, moving_s, distance_m, metrics, route,
	strain, session_id, ftp, tss, intensity, load_method, updated_at`

func scanActivity(r scanner) (Activity, error) {
	var a Activity
	var metrics string
	var strain sql.NullFloat64
	var sess sql.NullString
	if err := r.Scan(&a.ID, &a.Sport, &a.Name, &a.StartAt, &a.ElapsedS, &a.MovingS, &a.DistanceM, &metrics,
		&a.Route, &strain, &sess, &a.FTP, &a.TSS, &a.Intensity, &a.LoadMethod, &a.UpdatedAt); err != nil {
		return Activity{}, err
	}
	a.Metrics = json.RawMessage(metrics)
	if strain.Valid {
		a.Strain = &strain.Float64
	}
	if sess.Valid {
		a.SessionID = &sess.String
	}
	return a, nil
}

// PutActivity upserts an activity.
func (s *Store) PutActivity(ctx context.Context, a Activity) error {
	if len(a.Metrics) == 0 {
		a.Metrics = json.RawMessage(`{}`)
	}
	if a.UpdatedAt == 0 {
		a.UpdatedAt = time.Now().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO activities (`+activityCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET sport=excluded.sport, name=excluded.name, start_at=excluded.start_at,
			elapsed_s=excluded.elapsed_s, moving_s=excluded.moving_s, distance_m=excluded.distance_m,
			metrics=excluded.metrics, route=excluded.route, strain=excluded.strain, session_id=excluded.session_id,
			ftp=excluded.ftp, tss=excluded.tss, intensity=excluded.intensity, load_method=excluded.load_method,
			updated_at=excluded.updated_at`,
		a.ID, a.Sport, a.Name, a.StartAt, a.ElapsedS, a.MovingS, a.DistanceM, string(a.Metrics), a.Route,
		a.Strain, a.SessionID, a.FTP, a.TSS, a.Intensity, a.LoadMethod, a.UpdatedAt)
	return err
}

// SetLoad writes the FTP-dependent fields of an activity.
func (s *Store) SetLoad(ctx context.Context, id string, ftp int, tss, intensity float64, method string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE activities SET ftp=?, tss=?, intensity=?, load_method=? WHERE id=?`,
		ftp, tss, intensity, method, id)
	return err
}

func (s *Store) GetActivity(ctx context.Context, id string) (Activity, error) {
	a, err := scanActivity(s.db.QueryRowContext(ctx, `SELECT `+activityCols+` FROM activities WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Activity{}, ErrNotFound
	}
	return a, err
}

func (s *Store) ActivityBySession(ctx context.Context, sessionID string) (Activity, error) {
	a, err := scanActivity(s.db.QueryRowContext(ctx, `SELECT `+activityCols+` FROM activities WHERE session_id = ?`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return Activity{}, ErrNotFound
	}
	return a, err
}

// ActivityQuery filters ListActivities.
type ActivityQuery struct {
	From, To int64 // epoch ms, inclusive; 0 = unbounded
	Sports   []string
	Limit    int
	Before   int64 // pagination: start_at < Before
}

func (s *Store) ListActivities(ctx context.Context, q ActivityQuery) ([]Activity, error) {
	sqlq := `SELECT ` + activityCols + ` FROM activities WHERE 1=1`
	var args []any
	if q.From > 0 {
		sqlq += ` AND start_at >= ?`
		args = append(args, q.From)
	}
	if q.To > 0 {
		sqlq += ` AND start_at <= ?`
		args = append(args, q.To)
	}
	if q.Before > 0 {
		sqlq += ` AND start_at < ?`
		args = append(args, q.Before)
	}
	if len(q.Sports) > 0 {
		sqlq += ` AND sport IN (?` + repeat(",?", len(q.Sports)-1) + `)`
		for _, sp := range q.Sports {
			args = append(args, sp)
		}
	}
	sqlq += ` ORDER BY start_at DESC`
	if q.Limit > 0 {
		sqlq += ` LIMIT ?`
		args = append(args, q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

// DeleteActivityIfEmpty removes an activity no source points at any more.
func (s *Store) DeleteActivityIfEmpty(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM activities WHERE id = ? AND session_id IS NULL
		AND NOT EXISTS (SELECT 1 FROM activity_sources WHERE activity_id = ?)`, id, id)
	return err
}

// ---- Sources -------------------------------------------------------------------

const sourceCols = `id, activity_id, source, external_id, raw_sha256, sport, name, start_at, elapsed_s,
	moving_s, distance_m, device, has_power, has_hr, has_gps, extra, imported_at`

func scanSource(r scanner) (Source, error) {
	var x Source
	var raw sql.NullString
	var extra string
	if err := r.Scan(&x.ID, &x.ActivityID, &x.Source, &x.ExternalID, &raw, &x.Sport, &x.Name, &x.StartAt,
		&x.ElapsedS, &x.MovingS, &x.DistanceM, &x.Device, &x.HasPower, &x.HasHR, &x.HasGPS, &extra, &x.ImportedAt); err != nil {
		return Source{}, err
	}
	if raw.Valid {
		x.RawSHA256 = &raw.String
	}
	x.Extra = json.RawMessage(extra)
	return x, nil
}

func (s *Store) PutSource(ctx context.Context, x Source) error {
	if len(x.Extra) == 0 {
		x.Extra = json.RawMessage(`{}`)
	}
	if x.ImportedAt == 0 {
		x.ImportedAt = time.Now().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO activity_sources (`+sourceCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET activity_id=excluded.activity_id, source=excluded.source,
			external_id=excluded.external_id, raw_sha256=excluded.raw_sha256, sport=excluded.sport,
			name=excluded.name, start_at=excluded.start_at, elapsed_s=excluded.elapsed_s,
			moving_s=excluded.moving_s, distance_m=excluded.distance_m, device=excluded.device,
			has_power=excluded.has_power, has_hr=excluded.has_hr, has_gps=excluded.has_gps,
			extra=excluded.extra`,
		x.ID, x.ActivityID, x.Source, x.ExternalID, x.RawSHA256, x.Sport, x.Name, x.StartAt, x.ElapsedS,
		x.MovingS, x.DistanceM, x.Device, x.HasPower, x.HasHR, x.HasGPS, string(x.Extra), x.ImportedAt)
	return err
}

func (s *Store) GetSource(ctx context.Context, id string) (Source, error) {
	x, err := scanSource(s.db.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM activity_sources WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	return x, err
}

func (s *Store) querySources(ctx context.Context, q string, args ...any) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sourceCols+` FROM activity_sources `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		x, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SourcesFor returns an activity's sources.
func (s *Store) SourcesFor(ctx context.Context, activityID string) ([]Source, error) {
	return s.querySources(ctx, `WHERE activity_id = ? ORDER BY imported_at`, activityID)
}

// SourcesNear returns sources starting within window of t (all epoch ms).
func (s *Store) SourcesNear(ctx context.Context, t, window int64) ([]Source, error) {
	return s.querySources(ctx, `WHERE start_at BETWEEN ? AND ?`, t-window, t+window)
}

// SourcesFor many activities at once, keyed by activity.
func (s *Store) SourcesForMany(ctx context.Context, ids []string) (map[string][]Source, error) {
	out := map[string][]Source{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	xs, err := s.querySources(ctx, `WHERE activity_id IN (?`+repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	for _, x := range xs {
		out[x.ActivityID] = append(out[x.ActivityID], x)
	}
	return out, nil
}

// ---- Streams --------------------------------------------------------------------

func (s *Store) PutStream(ctx context.Context, sourceID string, v any) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO streams (source_id, data) VALUES (?, ?)
		ON CONFLICT(source_id) DO UPDATE SET data = excluded.data`, sourceID, buf.Bytes())
	return err
}

// GetStream decodes a source's stream into v. ErrNotFound if it has none.
func (s *Store) GetStream(ctx context.Context, sourceID string, v any) error {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM streams WHERE source_id = ?`, sourceID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ---- Raw files -------------------------------------------------------------------

func (s *Store) HasRaw(ctx context.Context, sha string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM raw_files WHERE sha256 = ?`, sha).Scan(&n)
	return n > 0, err
}

func (s *Store) PutRaw(ctx context.Context, sha, path string, size int64, format, origin string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO raw_files (sha256, path, size, format, origin, imported_at)
		VALUES (?, ?, ?, ?, ?, ?)`, sha, path, size, format, origin, time.Now().UnixMilli())
	return err
}

// SourceForRaw finds the source created from a raw file, if any.
func (s *Store) SourceForRaw(ctx context.Context, sha string) (Source, error) {
	xs, err := s.querySources(ctx, `WHERE raw_sha256 = ? LIMIT 1`, sha)
	if err != nil {
		return Source{}, err
	}
	if len(xs) == 0 {
		return Source{}, ErrNotFound
	}
	return xs[0], nil
}

// ---- Key/value: athlete settings and sync state -----------------------------------

func (s *Store) getKV(ctx context.Context, table, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM `+table+` WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) putKV(ctx context.Context, table, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO `+table+` (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) Athlete(ctx context.Context, key string) (string, error) {
	return s.getKV(ctx, "athlete", key)
}
func (s *Store) SetAthlete(ctx context.Context, key, value string) error {
	return s.putKV(ctx, "athlete", key, value)
}
func (s *Store) DeleteAthlete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM athlete WHERE key = ?`, key)
	return err
}
func (s *Store) SyncState(ctx context.Context, key string) (string, error) {
	return s.getKV(ctx, "sync_state", key)
}
func (s *Store) SetSyncState(ctx context.Context, key, value string) error {
	return s.putKV(ctx, "sync_state", key, value)
}

// ---- Recovery history and sleep ---------------------------------------------------

func (s *Store) RecoveryRange(ctx context.Context, from, to string) ([]Recovery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT date, score, hrv_ms, resting_hr, source FROM recovery
		WHERE date BETWEEN ? AND ? ORDER BY date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recovery{}
	for rows.Next() {
		var r Recovery
		if err := rows.Scan(&r.Date, &r.Score, &r.HRVms, &r.RestingHR, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Sleep is one sleep period.
type Sleep struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	StartAt     int64  `json:"start_at"`
	EndAt       int64  `json:"end_at"`
	AsleepS     int    `json:"asleep_s"`
	Performance *int   `json:"performance"`
	Source      string `json:"source"`
}

func (s *Store) PutSleep(ctx context.Context, x Sleep) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sleep (id, date, start_at, end_at, asleep_s, performance, source)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET date=excluded.date, start_at=excluded.start_at,
		end_at=excluded.end_at, asleep_s=excluded.asleep_s, performance=excluded.performance, source=excluded.source`,
		x.ID, x.Date, x.StartAt, x.EndAt, x.AsleepS, x.Performance, x.Source)
	return err
}

func (s *Store) SleepRange(ctx context.Context, from, to string) ([]Sleep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, date, start_at, end_at, asleep_s, performance, source
		FROM sleep WHERE date BETWEEN ? AND ? ORDER BY start_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sleep{}
	for rows.Next() {
		var x Sleep
		var perf sql.NullInt64
		if err := rows.Scan(&x.ID, &x.Date, &x.StartAt, &x.EndAt, &x.AsleepS, &perf, &x.Source); err != nil {
			return nil, err
		}
		if perf.Valid {
			v := int(perf.Int64)
			x.Performance = &v
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ---- OAuth tokens -------------------------------------------------------------------

// Token is a provider's OAuth grant. It lives in the storage directory so a
// backup carries it and a restore brings it back (outpost-service-spec §4).
type Token struct {
	Provider     string
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	Scope        string
}

func (s *Store) GetToken(ctx context.Context, provider string) (Token, error) {
	t := Token{Provider: provider}
	err := s.db.QueryRowContext(ctx, `SELECT access_token, refresh_token, expires_at, scope FROM oauth_tokens
		WHERE provider = ?`, provider).Scan(&t.AccessToken, &t.RefreshToken, &t.ExpiresAt, &t.Scope)
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	return t, err
}

func (s *Store) PutToken(ctx context.Context, t Token) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO oauth_tokens (provider, access_token, refresh_token, expires_at, scope)
		VALUES (?,?,?,?,?) ON CONFLICT(provider) DO UPDATE SET access_token=excluded.access_token,
		refresh_token=excluded.refresh_token, expires_at=excluded.expires_at, scope=excluded.scope`,
		t.Provider, t.AccessToken, t.RefreshToken, t.ExpiresAt, t.Scope)
	return err
}

func (s *Store) DeleteToken(ctx context.Context, provider string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE provider = ?`, provider)
	return err
}

// ExecBatch runs several statements in one transaction.
func (s *Store) ExecBatch(ctx context.Context, stmts []string, args [][]any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, args[i]...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MoveSources reassigns every source of from to to (used when merging).
func (s *Store) MoveSources(ctx context.Context, from, to string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE activity_sources SET activity_id = ? WHERE activity_id = ?`, to, from)
	return err
}

// DeleteSource removes one source record and its stream. Raw files stay.
func (s *Store) DeleteSource(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM streams WHERE source_id = ?`, `DELETE FROM activity_sources WHERE id = ?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearSession detaches an activity from a deleted session.
func (s *Store) ClearSession(ctx context.Context, activityID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE activities SET session_id = NULL WHERE id = ?`, activityID)
	return err
}
