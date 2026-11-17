// Package store keeps events, metrics, devices and pairing codes in SQLite.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	time    INTEGER NOT NULL,          -- unix ms
	key     TEXT NOT NULL,
	level   TEXT NOT NULL,             -- info, warn, error
	code    TEXT NOT NULL,
	params  TEXT NOT NULL DEFAULT '{}',
	seen    INTEGER NOT NULL DEFAULT 0 -- unused since seen is kept per device
);
CREATE INDEX IF NOT EXISTS events_time ON events(time);

CREATE TABLE IF NOT EXISTS metrics (
	time    INTEGER PRIMARY KEY,       -- unix s, start of the minute
	cpu     REAL NOT NULL,
	mem     REAL NOT NULL,
	swap    REAL NOT NULL,
	load1   REAL NOT NULL,
	net_rx  REAL NOT NULL,
	net_tx  REAL NOT NULL,
	disks   TEXT NOT NULL DEFAULT '{}' -- mount → used percent
);

CREATE TABLE IF NOT EXISTS devices (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	token_hash  TEXT NOT NULL UNIQUE,
	name        TEXT NOT NULL,
	lang        TEXT NOT NULL,
	push_token  TEXT NOT NULL DEFAULT '',
	min_level   TEXT NOT NULL DEFAULT 'warn',
	created     INTEGER NOT NULL,
	last_seen   INTEGER NOT NULL
);

-- Seen is per device: events up to devices.seen_floor, plus the ones listed here.
CREATE TABLE IF NOT EXISTS event_seen (
	device_id INTEGER NOT NULL,
	event_id  INTEGER NOT NULL,
	PRIMARY KEY (device_id, event_id)
);

CREATE TABLE IF NOT EXISTS pair_codes (
	code_hash TEXT PRIMARY KEY,
	expires   INTEGER NOT NULL
);
`

type Store struct{ db *sql.DB }

// Open opens (and creates) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps things simple and safe
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate updates databases created by older versions.
func migrate(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('devices') WHERE name='seen_floor'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		// Seen used to be one flag for all devices, always set up to an id.
		if _, err := db.Exec(`ALTER TABLE devices ADD COLUMN seen_floor INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE devices SET seen_floor = COALESCE((SELECT MAX(id) FROM events WHERE seen=1), 0)`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// --- events ---------------------------------------------------------------

type Event struct {
	ID     int64             `json:"id"`
	Time   time.Time         `json:"time"`
	Key    string            `json:"key"`
	Level  string            `json:"level"`
	Code   string            `json:"code"`
	Params map[string]string `json:"params"`
	Seen   bool              `json:"seen"`
}

func (s *Store) AddEvent(e Event) (int64, error) {
	p, _ := json.Marshal(e.Params)
	res, err := s.db.Exec(`INSERT INTO events(time,key,level,code,params) VALUES(?,?,?,?,?)`,
		e.Time.UnixMilli(), e.Key, e.Level, e.Code, string(p))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// seenBy is true for events the device has seen.
const seenBy = `(id <= (SELECT seen_floor FROM devices WHERE id=?)
	OR EXISTS (SELECT 1 FROM event_seen WHERE device_id=? AND event_id=events.id))`

// Events returns up to limit events older than beforeID (0 = newest),
// newest first, restricted to the given levels (empty = all). Seen is that
// of the given device.
func (s *Store) Events(device, beforeID int64, limit int, levels []string) ([]Event, error) {
	q := `SELECT id,time,key,level,code,params,` + seenBy + ` FROM events WHERE 1=1`
	args := []any{device, device}
	if beforeID > 0 {
		q += ` AND id < ?`
		args = append(args, beforeID)
	}
	if len(levels) > 0 {
		q += ` AND level IN (` + placeholders(len(levels)) + `)`
		for _, l := range levels {
			args = append(args, l)
		}
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var ms int64
		var params string
		if err := rows.Scan(&e.ID, &ms, &e.Key, &e.Level, &e.Code, &params, &e.Seen); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ms)
		_ = json.Unmarshal([]byte(params), &e.Params)
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkSeen marks the given events as seen by the device.
func (s *Store) MarkSeen(device int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{device}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO event_seen(device_id, event_id)
		SELECT ?, id FROM events WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// MarkSeenUpTo marks all events up to and including id as seen by the device.
func (s *Store) MarkSeenUpTo(device, upToID int64) error {
	_, err := s.db.Exec(`UPDATE devices SET seen_floor = MAX(seen_floor, ?) WHERE id=?`, upToID, device)
	return err
}

// Unseen counts the events of the given levels the device has not seen.
func (s *Store) Unseen(device int64, levels []string) (int, error) {
	q := `SELECT COUNT(*) FROM events WHERE NOT ` + seenBy
	args := []any{device, device}
	if len(levels) > 0 {
		q += ` AND level IN (` + placeholders(len(levels)) + `)`
		for _, l := range levels {
			args = append(args, l)
		}
	}
	var n int
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// --- metrics --------------------------------------------------------------

type Metric struct {
	Time  time.Time          `json:"t"`
	CPU   float64            `json:"cpu"`
	Mem   float64            `json:"mem"`
	Swap  float64            `json:"swap"`
	Load1 float64            `json:"load1"`
	NetRx float64            `json:"netRx"`
	NetTx float64            `json:"netTx"`
	Disks map[string]float64 `json:"disks"`
}

func (s *Store) AddMetric(m Metric) error {
	d, _ := json.Marshal(m.Disks)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO metrics(time,cpu,mem,swap,load1,net_rx,net_tx,disks) VALUES(?,?,?,?,?,?,?,?)`,
		m.Time.Unix(), m.CPU, m.Mem, m.Swap, m.Load1, m.NetRx, m.NetTx, string(d))
	return err
}

// Metrics returns all points since from, oldest first.
func (s *Store) Metrics(from time.Time) ([]Metric, error) {
	rows, err := s.db.Query(`SELECT time,cpu,mem,swap,load1,net_rx,net_tx,disks FROM metrics WHERE time >= ? ORDER BY time`, from.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Metric{}
	for rows.Next() {
		var m Metric
		var t int64
		var disks string
		if err := rows.Scan(&t, &m.CPU, &m.Mem, &m.Swap, &m.Load1, &m.NetRx, &m.NetTx, &disks); err != nil {
			return nil, err
		}
		m.Time = time.Unix(t, 0)
		_ = json.Unmarshal([]byte(disks), &m.Disks)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Prune deletes metrics and events older than the given times, and expired pairing codes.
func (s *Store) Prune(metricsBefore, eventsBefore time.Time) error {
	if _, err := s.db.Exec(`DELETE FROM metrics WHERE time < ?`, metricsBefore.Unix()); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM events WHERE time < ?`, eventsBefore.UnixMilli()); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM event_seen WHERE event_id NOT IN (SELECT id FROM events)`); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM pair_codes WHERE expires < ?`, time.Now().Unix())
	return err
}

// --- devices and pairing --------------------------------------------------

type Device struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Lang      string    `json:"lang"`
	PushToken string    `json:"pushToken"`
	MinLevel  string    `json:"minLevel"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"lastSeen"`
}

func (s *Store) AddPairCode(codeHash string, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO pair_codes(code_hash,expires) VALUES(?,?)`, codeHash, expires.Unix())
	return err
}

// ErrInvalidCode is returned for unknown, used or expired pairing codes.
var ErrInvalidCode = errors.New("invalid or expired pairing code")

// Pair consumes a pairing code and creates a device in one transaction.
func (s *Store) Pair(codeHash, tokenHash string, d Device) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM pair_codes WHERE code_hash=? AND expires >= ?`, codeHash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrInvalidCode
	}
	now := time.Now().Unix()
	// Events from before pairing count as seen on the new device.
	res, err = tx.Exec(`INSERT INTO devices(token_hash,name,lang,push_token,min_level,created,last_seen,seen_floor)
		VALUES(?,?,?,?,?,?,?,(SELECT COALESCE(MAX(id), 0) FROM events))`,
		tokenHash, d.Name, d.Lang, d.PushToken, d.MinLevel, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

const deviceCols = `id,name,lang,push_token,min_level,created,last_seen`

func scanDevice(sc interface{ Scan(...any) error }) (*Device, error) {
	var d Device
	var created, seen int64
	if err := sc.Scan(&d.ID, &d.Name, &d.Lang, &d.PushToken, &d.MinLevel, &created, &seen); err != nil {
		return nil, err
	}
	d.Created, d.LastSeen = time.Unix(created, 0), time.Unix(seen, 0)
	return &d, nil
}

// DeviceByToken returns the device for a token hash, or nil if unknown.
func (s *Store) DeviceByToken(tokenHash string) (*Device, error) {
	d, err := scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE token_hash=?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// DeviceExists reports whether the device is still paired.
func (s *Store) DeviceExists(id int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE id=?`, id).Scan(&n)
	return n > 0, err
}

func (s *Store) Devices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT ` + deviceCols + ` FROM devices ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDevice(d Device) error {
	_, err := s.db.Exec(`UPDATE devices SET name=?, lang=?, push_token=?, min_level=? WHERE id=?`,
		d.Name, d.Lang, d.PushToken, d.MinLevel, d.ID)
	return err
}

func (s *Store) TouchDevice(id int64) error {
	_, err := s.db.Exec(`UPDATE devices SET last_seen=? WHERE id=?`, time.Now().Unix(), id)
	return err
}

// ClearPushToken removes a push token everywhere (e.g. app uninstalled).
func (s *Store) ClearPushToken(token string) error {
	_, err := s.db.Exec(`UPDATE devices SET push_token='' WHERE push_token=?`, token)
	return err
}

// DeleteDevice removes a device; returns false if it did not exist.
func (s *Store) DeleteDevice(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM devices WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	if _, err := s.db.Exec(`DELETE FROM event_seen WHERE device_id=?`, id); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
