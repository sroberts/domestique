package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Bike is one bike. Its distance is computed from the rides assigned to it.
type Bike struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases"`
	DefaultRide    bool     `json:"default_ride"`
	DefaultVirtual bool     `json:"default_virtual"`
	StartDistanceM float64  `json:"start_distance_m"`
	Retired        bool     `json:"retired"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
}

// Component is a wearing part fitted to a bike.
type Component struct {
	ID             string  `json:"id"`
	BikeID         string  `json:"bike_id"`
	Kind           string  `json:"kind"`
	Name           string  `json:"name"`
	InstalledAt    int64   `json:"installed_at"`
	RemovedAt      *int64  `json:"removed_at"`
	ReplaceAtM     float64 `json:"replace_at_m"`
	StartDistanceM float64 `json:"start_distance_m"`
	ReplacedBy     *string `json:"replaced_by"`
	CreatedAt      int64   `json:"created_at"`
	UpdatedAt      int64   `json:"updated_at"`
}

// Sensor is a battery-powered sensor, such as a chest strap.
type Sensor struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	DeviceMatch  string  `json:"device_match"`
	BatteryAt    int64   `json:"battery_at"`
	BatteryLifeH float64 `json:"battery_life_h"`
	Retired      bool    `json:"retired"`
	CreatedAt    int64   `json:"created_at"`
	UpdatedAt    int64   `json:"updated_at"`
}

// ---- Bikes ------------------------------------------------------------------

const bikeCols = `id, name, aliases, default_ride, default_virtual, start_distance_m, retired, created_at, updated_at`

func scanBike(r scanner) (Bike, error) {
	var b Bike
	var aliases string
	if err := r.Scan(&b.ID, &b.Name, &aliases, &b.DefaultRide, &b.DefaultVirtual, &b.StartDistanceM,
		&b.Retired, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return Bike{}, err
	}
	_ = json.Unmarshal([]byte(aliases), &b.Aliases)
	if b.Aliases == nil {
		b.Aliases = []string{}
	}
	return b, nil
}

func (s *Store) ListBikes(ctx context.Context) ([]Bike, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+bikeCols+` FROM bikes ORDER BY retired, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bike{}
	for rows.Next() {
		b, err := scanBike(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) GetBike(ctx context.Context, id string) (Bike, error) {
	b, err := scanBike(s.db.QueryRowContext(ctx, `SELECT `+bikeCols+` FROM bikes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Bike{}, ErrNotFound
	}
	return b, err
}

// PutBike upserts a bike. A bike made the default for a kind of ride takes
// that role from any other bike; a retired bike is nobody's default.
func (s *Store) PutBike(ctx context.Context, b Bike) error {
	if b.Aliases == nil {
		b.Aliases = []string{}
	}
	aliases, _ := json.Marshal(b.Aliases)
	now := time.Now().UnixMilli()
	if b.CreatedAt == 0 {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	if b.Retired {
		b.DefaultRide, b.DefaultVirtual = false, false
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if b.DefaultRide {
		if _, err := tx.ExecContext(ctx, `UPDATE bikes SET default_ride = 0 WHERE id != ?`, b.ID); err != nil {
			return err
		}
	}
	if b.DefaultVirtual {
		if _, err := tx.ExecContext(ctx, `UPDATE bikes SET default_virtual = 0 WHERE id != ?`, b.ID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bikes (`+bikeCols+`) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, aliases=excluded.aliases,
			default_ride=excluded.default_ride, default_virtual=excluded.default_virtual,
			start_distance_m=excluded.start_distance_m, retired=excluded.retired, updated_at=excluded.updated_at`,
		b.ID, b.Name, string(aliases), b.DefaultRide, b.DefaultVirtual, b.StartDistanceM, b.Retired,
		b.CreatedAt, b.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteBike removes a bike with its components and ride assignments. Rides
// assigned to it fall back to automatic assignment.
func (s *Store) DeleteBike(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM components WHERE bike_id = ?`,
		`DELETE FROM activity_bikes WHERE bike_id = ?`,
		`DELETE FROM bikes WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- Components ----------------------------------------------------------------

const componentCols = `id, bike_id, kind, name, installed_at, removed_at, replace_at_m, start_distance_m,
	replaced_by, created_at, updated_at`

func scanComponent(r scanner) (Component, error) {
	var c Component
	var removed sql.NullInt64
	var by sql.NullString
	if err := r.Scan(&c.ID, &c.BikeID, &c.Kind, &c.Name, &c.InstalledAt, &removed, &c.ReplaceAtM,
		&c.StartDistanceM, &by, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Component{}, err
	}
	if removed.Valid {
		c.RemovedAt = &removed.Int64
	}
	if by.Valid {
		c.ReplacedBy = &by.String
	}
	return c, nil
}

func (s *Store) ListComponents(ctx context.Context) ([]Component, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+componentCols+` FROM components ORDER BY installed_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Component{}
	for rows.Next() {
		c, err := scanComponent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetComponent(ctx context.Context, id string) (Component, error) {
	c, err := scanComponent(s.db.QueryRowContext(ctx, `SELECT `+componentCols+` FROM components WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Component{}, ErrNotFound
	}
	return c, err
}

func putComponent(ctx context.Context, tx *sql.Tx, c Component) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO components (`+componentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET bike_id=excluded.bike_id, kind=excluded.kind, name=excluded.name,
			installed_at=excluded.installed_at, removed_at=excluded.removed_at, replace_at_m=excluded.replace_at_m,
			start_distance_m=excluded.start_distance_m, replaced_by=excluded.replaced_by, updated_at=excluded.updated_at`,
		c.ID, c.BikeID, c.Kind, c.Name, c.InstalledAt, c.RemovedAt, c.ReplaceAtM, c.StartDistanceM,
		c.ReplacedBy, c.CreatedAt, c.UpdatedAt)
	return err
}

// PutComponent upserts a component. With replaces set, the component it
// replaces is retired in the same transaction: removed when this one was
// installed, and pointed at its successor so its history stays readable.
func (s *Store) PutComponent(ctx context.Context, c Component, replaces string) error {
	now := time.Now().UnixMilli()
	if c.CreatedAt == 0 {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if replaces != "" {
		old, err := scanComponent(tx.QueryRowContext(ctx, `SELECT `+componentCols+` FROM components WHERE id = ?`, replaces))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		at := c.InstalledAt
		old.RemovedAt = &at
		old.ReplacedBy = &c.ID
		old.UpdatedAt = now
		if err := putComponent(ctx, tx, old); err != nil {
			return err
		}
	}
	if err := putComponent(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteComponent removes a component entered by mistake. A component it
// replaced is fitted again.
func (s *Store) DeleteComponent(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE components SET removed_at = NULL, replaced_by = NULL WHERE replaced_by = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM components WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- Sensors ---------------------------------------------------------------------

const sensorCols = `id, name, kind, device_match, battery_at, battery_life_h, retired, created_at, updated_at`

func scanSensor(r scanner) (Sensor, error) {
	var x Sensor
	err := r.Scan(&x.ID, &x.Name, &x.Kind, &x.DeviceMatch, &x.BatteryAt, &x.BatteryLifeH, &x.Retired, &x.CreatedAt, &x.UpdatedAt)
	return x, err
}

func (s *Store) ListSensors(ctx context.Context) ([]Sensor, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sensorCols+` FROM sensors ORDER BY retired, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sensor{}
	for rows.Next() {
		x, err := scanSensor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) GetSensor(ctx context.Context, id string) (Sensor, error) {
	x, err := scanSensor(s.db.QueryRowContext(ctx, `SELECT `+sensorCols+` FROM sensors WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Sensor{}, ErrNotFound
	}
	return x, err
}

func (s *Store) PutSensor(ctx context.Context, x Sensor) error {
	now := time.Now().UnixMilli()
	if x.CreatedAt == 0 {
		x.CreatedAt = now
	}
	x.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sensors (`+sensorCols+`) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind, device_match=excluded.device_match,
			battery_at=excluded.battery_at, battery_life_h=excluded.battery_life_h, retired=excluded.retired,
			updated_at=excluded.updated_at`,
		x.ID, x.Name, x.Kind, x.DeviceMatch, x.BatteryAt, x.BatteryLifeH, x.Retired, x.CreatedAt, x.UpdatedAt)
	return err
}

func (s *Store) DeleteSensor(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sensors WHERE id = ?`, id)
	return err
}

// ---- Ride assignment ------------------------------------------------------------------

// BikeOverride is a manual bike choice for one activity. BikeID nil means
// "no bike".
type BikeOverride struct {
	ActivityID string
	BikeID     *string
}

// SetActivityBike records a manual choice of bike (nil: no bike).
func (s *Store) SetActivityBike(ctx context.Context, activityID string, bikeID *string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO activity_bikes (activity_id, bike_id, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(activity_id) DO UPDATE SET bike_id = excluded.bike_id, updated_at = excluded.updated_at`,
		activityID, bikeID, time.Now().UnixMilli())
	return err
}

// ClearActivityBike returns an activity to automatic assignment.
func (s *Store) ClearActivityBike(ctx context.Context, activityID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM activity_bikes WHERE activity_id = ?`, activityID)
	return err
}

// BikeOverrides returns every manual assignment, keyed by activity.
func (s *Store) BikeOverrides(ctx context.Context) (map[string]*string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT activity_id, bike_id FROM activity_bikes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*string{}
	for rows.Next() {
		var id string
		var b sql.NullString
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		if b.Valid {
			v := b.String
			out[id] = &v
		} else {
			out[id] = nil
		}
	}
	return out, rows.Err()
}

// GearActivity is the slice of an activity gear arithmetic needs.
type GearActivity struct {
	ID        string
	Sport     string
	StartAt   int64
	MovingS   int
	ElapsedS  int
	DistanceM float64
	// StravaGear is the gear name Strava recorded for it, if any.
	StravaGear string
}

// GearRides returns every ride, oldest first, with the Strava gear name from
// any of its sources.
func (s *Store) GearRides(ctx context.Context) ([]GearActivity, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.sport, a.start_at, a.moving_s, a.elapsed_s, a.distance_m,
			COALESCE((SELECT json_extract(x.extra, '$.gear') FROM activity_sources x
				WHERE x.activity_id = a.id AND json_extract(x.extra, '$.gear') != '' LIMIT 1), '')
		FROM activities a WHERE a.sport IN ('ride', 'virtual_ride') ORDER BY a.start_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GearActivity{}
	for rows.Next() {
		var g GearActivity
		if err := rows.Scan(&g.ID, &g.Sport, &g.StartAt, &g.MovingS, &g.ElapsedS, &g.DistanceM, &g.StravaGear); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// HRUse is an activity whose heart rate a sensor may have recorded.
type HRUse struct {
	ActivityID string
	StartAt    int64
	Seconds    int
	// Devices are the device names of its sources with chest-strap-capable
	// heart rate (anything but Whoop's wrist band and the app's borrowed HR).
	Devices []string
	// Kinds are those sources' kinds (wahoo, garmin, zwift, ...).
	Kinds []string
}

// HRUses returns activities since from (epoch ms) that have heart rate from a
// source other than Whoop or the app.
func (s *Store) HRUses(ctx context.Context, from int64) ([]HRUse, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.start_at, CASE WHEN a.moving_s > 0 THEN a.moving_s ELSE a.elapsed_s END, x.source, x.device
		FROM activities a JOIN activity_sources x ON x.activity_id = a.id
		WHERE a.start_at >= ? AND x.has_hr = 1 AND x.source NOT IN ('whoop', 'app')
		ORDER BY a.start_at`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HRUse
	idx := map[string]int{}
	for rows.Next() {
		var id, kind, device string
		var start int64
		var secs int
		if err := rows.Scan(&id, &start, &secs, &kind, &device); err != nil {
			return nil, err
		}
		i, ok := idx[id]
		if !ok {
			i = len(out)
			idx[id] = i
			out = append(out, HRUse{ActivityID: id, StartAt: start, Seconds: secs})
		}
		out[i].Kinds = append(out[i].Kinds, kind)
		if device != "" {
			out[i].Devices = append(out[i].Devices, device)
		}
	}
	return out, rows.Err()
}

// SetSourceExtra sets one key of a source's extra JSON (e.g. the Strava gear
// name, learned from a newer export of an activity already imported).
func (s *Store) SetSourceExtra(ctx context.Context, sourceID, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE activity_sources SET extra = json_set(extra, '$.' || ?, json(?)) WHERE id = ?`,
		key, string(b), sourceID)
	return err
}
