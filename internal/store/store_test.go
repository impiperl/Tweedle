package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addDevice(t *testing.T, st *Store, token string) int64 {
	t.Helper()
	if err := st.AddPairCode("c"+token, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	id, err := st.Pair("c"+token, token, Device{Name: token, Lang: "en", MinLevel: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addEvent(t *testing.T, st *Store, level string) int64 {
	t.Helper()
	id, err := st.AddEvent(Event{Time: time.Now(), Key: "k", Level: level, Code: "c"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func unseen(t *testing.T, st *Store, dev int64) int {
	t.Helper()
	n, err := st.Unseen(dev, []string{"warn", "error"})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSeenIsPerDeviceAndPerEvent(t *testing.T) {
	st := open(t)
	before := addEvent(t, st, "error") // before pairing: seen on new devices
	a, b := addDevice(t, st, "a"), addDevice(t, st, "b")
	w1 := addEvent(t, st, "warn")
	e1 := addEvent(t, st, "error")
	w2 := addEvent(t, st, "warn")
	if unseen(t, st, a) != 3 || unseen(t, st, b) != 3 {
		t.Fatalf("start: a=%d b=%d", unseen(t, st, a), unseen(t, st, b))
	}
	// A looks at the error filter: only e1 is shown and marked.
	if err := st.MarkSeen(a, []int64{e1}); err != nil {
		t.Fatal(err)
	}
	if unseen(t, st, a) != 2 || unseen(t, st, b) != 3 {
		t.Fatalf("after A saw e1: a=%d b=%d", unseen(t, st, a), unseen(t, st, b))
	}
	evs, err := st.Events(a, 0, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, e := range evs {
		seen[e.ID] = e.Seen
	}
	if !seen[before] || seen[w1] || !seen[e1] || seen[w2] {
		t.Fatalf("seen flags for A: %v", seen)
	}
	// Older clients send upTo.
	if err := st.MarkSeenUpTo(b, w1); err != nil {
		t.Fatal(err)
	}
	if unseen(t, st, b) != 2 {
		t.Fatalf("after upTo: b=%d", unseen(t, st, b))
	}
	if _, err := st.DeleteDevice(a); err != nil {
		t.Fatal(err)
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM event_seen WHERE device_id=?`, a).Scan(&n)
	if n != 0 {
		t.Fatalf("seen rows of a removed device left: %d", n)
	}
}

func TestMigrateGlobalSeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// Schema of the previous version: no seen_floor, one seen flag per event.
	for _, q := range []string{
		`CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER NOT NULL, key TEXT NOT NULL,
			level TEXT NOT NULL, code TEXT NOT NULL, params TEXT NOT NULL DEFAULT '{}', seen INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE devices (id INTEGER PRIMARY KEY AUTOINCREMENT, token_hash TEXT NOT NULL UNIQUE, name TEXT NOT NULL,
			lang TEXT NOT NULL, push_token TEXT NOT NULL DEFAULT '', min_level TEXT NOT NULL DEFAULT 'warn',
			created INTEGER NOT NULL, last_seen INTEGER NOT NULL)`,
		`INSERT INTO events(time,key,level,code,seen) VALUES (1,'k','warn','c',1), (2,'k','warn','c',1), (3,'k','warn','c',0)`,
		`INSERT INTO devices(token_hash,name,lang,created,last_seen) VALUES ('t','old','en',1,1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d, _ := st.DeviceByToken("t")
	if n := unseen(t, st, d.ID); n != 1 {
		t.Fatalf("after migration: %d unseen, want 1", n)
	}
}
