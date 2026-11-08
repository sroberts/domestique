package store

import (
	"context"
	"errors"
	"fmt"
)

// migrations are applied in order; the schema version lives in SQLite's
// user_version, inside the database itself, so a restored older directory is
// simply migrated forward again on next start.
var migrations = []string{
	// 1: strength MVP
	`
	CREATE TABLE templates (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		exercises  TEXT NOT NULL,           -- JSON []Exercise
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE plans (
		date        TEXT PRIMARY KEY,       -- YYYY-MM-DD, athlete's local date
		template_id TEXT NOT NULL REFERENCES templates(id)
	);
	CREATE TABLE recovery (
		date       TEXT PRIMARY KEY,
		score      INTEGER NOT NULL,
		hrv_ms     REAL NOT NULL,
		resting_hr INTEGER NOT NULL,
		source     TEXT NOT NULL
	);
	CREATE TABLE sessions (
		id          TEXT PRIMARY KEY,       -- client-generated UUID
		template_id TEXT NOT NULL,
		name        TEXT NOT NULL,
		plan        TEXT NOT NULL,          -- JSON []Exercise snapshot at start
		started_at  INTEGER NOT NULL,       -- epoch ms, device clock
		finished_at INTEGER,
		strain      REAL,
		updated_at  INTEGER NOT NULL
	);
	CREATE INDEX sessions_started ON sessions(started_at);
	CREATE TABLE sets (
		id               TEXT PRIMARY KEY,  -- client-generated UUID
		session_id       TEXT NOT NULL,
		exercise_index   INTEGER NOT NULL,
		exercise_name    TEXT NOT NULL,
		set_index        INTEGER NOT NULL,
		target_reps      INTEGER NOT NULL,
		target_weight_kg REAL NOT NULL,
		reps             INTEGER NOT NULL,
		weight_kg        REAL NOT NULL,
		started_at       INTEGER NOT NULL,
		ended_at         INTEGER NOT NULL,
		updated_at       INTEGER NOT NULL
	);
	CREATE INDEX sets_session ON sets(session_id, started_at);
	CREATE TABLE hr_samples (
		source TEXT NOT NULL,
		ts     INTEGER NOT NULL,            -- epoch ms
		bpm    INTEGER NOT NULL,
		PRIMARY KEY (source, ts)
	) WITHOUT ROWID;
	CREATE INDEX hr_ts ON hr_samples(ts);
	`,
	// 2: all activities, multi-source, with streams and raw file archive
	`
	CREATE TABLE raw_files (
		sha256      TEXT PRIMARY KEY,
		path        TEXT NOT NULL,          -- relative to the storage dir
		size        INTEGER NOT NULL,
		format      TEXT NOT NULL,          -- fit, gpx, tcx, json
		origin      TEXT NOT NULL,          -- where it came from (strava-export, watch:<dir>, wahoo-api, ...)
		imported_at INTEGER NOT NULL
	);
	CREATE TABLE activities (
		id             TEXT PRIMARY KEY,
		sport          TEXT NOT NULL,
		name           TEXT NOT NULL,
		start_at       INTEGER NOT NULL,    -- epoch ms
		elapsed_s      INTEGER NOT NULL,
		moving_s       INTEGER NOT NULL,
		distance_m     REAL NOT NULL DEFAULT 0,
		metrics        TEXT NOT NULL,       -- JSON activity.Metrics of the merged stream
		route          TEXT NOT NULL DEFAULT '',
		strain         REAL,
		session_id     TEXT,                -- strength session logged in the app
		ftp            INTEGER NOT NULL DEFAULT 0, -- FTP in force on the day (derived)
		tss            REAL NOT NULL DEFAULT 0,
		intensity      REAL NOT NULL DEFAULT 0,
		load_method    TEXT NOT NULL DEFAULT 'none', -- power | hr | strain | none
		updated_at     INTEGER NOT NULL
	);
	CREATE INDEX activities_start ON activities(start_at);
	CREATE UNIQUE INDEX activities_session ON activities(session_id) WHERE session_id IS NOT NULL;
	CREATE TABLE activity_sources (
		id           TEXT PRIMARY KEY,      -- e.g. strava:123456, fit:<sha256>, whoop:<uuid>
		activity_id  TEXT NOT NULL REFERENCES activities(id),
		source       TEXT NOT NULL,         -- wahoo, zwift, garmin, whoop, strava, app, device
		external_id  TEXT NOT NULL DEFAULT '',
		raw_sha256   TEXT,
		sport        TEXT NOT NULL,
		name         TEXT NOT NULL DEFAULT '',
		start_at     INTEGER NOT NULL,
		elapsed_s    INTEGER NOT NULL,
		moving_s     INTEGER NOT NULL DEFAULT 0,
		distance_m   REAL NOT NULL DEFAULT 0,
		device       TEXT NOT NULL DEFAULT '',
		has_power    INTEGER NOT NULL DEFAULT 0,
		has_hr       INTEGER NOT NULL DEFAULT 0,
		has_gps      INTEGER NOT NULL DEFAULT 0,
		extra        TEXT NOT NULL DEFAULT '{}', -- JSON: strain, route, description, ...
		imported_at  INTEGER NOT NULL
	);
	CREATE INDEX sources_activity ON activity_sources(activity_id);
	CREATE INDEX sources_start ON activity_sources(start_at);
	CREATE TABLE streams (
		source_id TEXT PRIMARY KEY REFERENCES activity_sources(id),
		data      BLOB NOT NULL             -- gzip JSON activity.Stream
	);
	CREATE TABLE athlete (
		key   TEXT PRIMARY KEY,             -- ftp_override, max_hr_override, rest_hr, ...
		value TEXT NOT NULL
	);
	CREATE TABLE sleep (
		id          TEXT PRIMARY KEY,
		date        TEXT NOT NULL,          -- wake-up date
		start_at    INTEGER NOT NULL,
		end_at      INTEGER NOT NULL,
		asleep_s    INTEGER NOT NULL,
		performance INTEGER,                -- Whoop sleep performance %
		source      TEXT NOT NULL
	);
	CREATE INDEX sleep_date ON sleep(date);
	CREATE TABLE sync_state (
		key   TEXT PRIMARY KEY,             -- e.g. whoop:cursor, watch:/path
		value TEXT NOT NULL
	);
	CREATE TABLE oauth_tokens (
		provider      TEXT PRIMARY KEY,     -- whoop, wahoo
		access_token  TEXT NOT NULL,
		refresh_token TEXT NOT NULL,
		expires_at    INTEGER NOT NULL,
		scope         TEXT NOT NULL DEFAULT ''
	);
	`,
	// 3: clock alignment between the phone's set timestamps and heart rate
	`
	ALTER TABLE sessions ADD COLUMN clock_offset_ms INTEGER NOT NULL DEFAULT 0; -- server minus device clock, measured on the phone
	ALTER TABLE sessions ADD COLUMN hr_shift_ms INTEGER;                       -- manual HR alignment; NULL = automatic
	`,
	// 4: gear. Distances and hours are computed from activities, never stored,
	// so reassigning a ride recomputes everything.
	`
	CREATE TABLE bikes (
		id               TEXT PRIMARY KEY,
		name             TEXT NOT NULL,
		aliases          TEXT NOT NULL DEFAULT '[]', -- JSON []string: names other apps use (Strava's gear name)
		default_ride     INTEGER NOT NULL DEFAULT 0, -- default bike for outdoor rides
		default_virtual  INTEGER NOT NULL DEFAULT 0, -- default bike for indoor and virtual rides
		start_distance_m REAL NOT NULL DEFAULT 0,    -- distance before Domestique knew of it
		retired          INTEGER NOT NULL DEFAULT 0,
		created_at       INTEGER NOT NULL,
		updated_at       INTEGER NOT NULL
	);
	CREATE TABLE components (
		id               TEXT PRIMARY KEY,
		bike_id          TEXT NOT NULL REFERENCES bikes(id),
		kind             TEXT NOT NULL,              -- chain, tyres, cassette, brake_pads, other
		name             TEXT NOT NULL DEFAULT '',
		installed_at     INTEGER NOT NULL,           -- epoch ms; rides from then on count
		removed_at       INTEGER,                    -- epoch ms; NULL while fitted
		replace_at_m     REAL NOT NULL DEFAULT 0,    -- 0 = no target
		start_distance_m REAL NOT NULL DEFAULT 0,
		replaced_by      TEXT,                       -- the component fitted in its place
		created_at       INTEGER NOT NULL,
		updated_at       INTEGER NOT NULL
	);
	CREATE INDEX components_bike ON components(bike_id);
	CREATE TABLE sensors (
		id             TEXT PRIMARY KEY,
		name           TEXT NOT NULL,
		kind           TEXT NOT NULL DEFAULT 'hr_strap',
		device_match   TEXT NOT NULL DEFAULT '',     -- device name substring; '' = any non-Whoop heart rate
		battery_at     INTEGER NOT NULL,             -- epoch ms the battery was replaced
		battery_life_h REAL NOT NULL,
		retired        INTEGER NOT NULL DEFAULT 0,
		created_at     INTEGER NOT NULL,
		updated_at     INTEGER NOT NULL
	);
	-- A manual choice of bike for one ride. No foreign key to activities: an
	-- activity can be merged away, and that must not fail on this table.
	CREATE TABLE activity_bikes (
		activity_id TEXT PRIMARY KEY,
		bike_id     TEXT,                            -- NULL = no bike (a hire bike, a spin class)
		updated_at  INTEGER NOT NULL
	);
	`,
}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(migrations) {
		// A newer release wrote this database. Refuse rather than misread it.
		return fmt.Errorf("database schema v%d is newer than this binary (v%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			return errors.Join(fmt.Errorf("migration %d: %w", i+1, err), tx.Rollback())
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			return errors.Join(fmt.Errorf("migration %d version: %w", i+1, err), tx.Rollback())
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d commit: %w", i+1, err)
		}
	}
	return nil
}
