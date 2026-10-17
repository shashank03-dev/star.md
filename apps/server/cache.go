package main

import (
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

// Cache is the swappable boundary. SQLite is the launch implementation; moving
// to Postgres/Redis/Turso later is a one-file change implementing these two
// methods and nothing else.
type Cache interface {
	Get(key string) (blob []byte, ok bool)
	Put(key string, blob []byte, ttl time.Duration)
}

// bestEffort wraps any Cache so every operation is panic- and error-safe. A
// cache failure (missing db, locked file, corrupt row, invisible serverless
// disk) is logged and swallowed; the caller always falls through to a fresh
// build. A cache problem can slow a response, never turn it into an error page.
type bestEffort struct{ inner Cache }

func (b bestEffort) Get(key string) (blob []byte, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("cache: swallowed Get panic for %q: %v", key, r)
			blob, ok = nil, false
		}
	}()
	if b.inner == nil {
		return nil, false
	}
	return b.inner.Get(key)
}

func (b bestEffort) Put(key string, blob []byte, ttl time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("cache: swallowed Put panic for %q: %v", key, r)
		}
	}()
	if b.inner == nil {
		return
	}
	b.inner.Put(key, blob, ttl)
}

// ---- SQLite implementation ----

type sqliteCache struct {
	db  *sql.DB
	dsn string
}

func newSQLiteCache(path string) (Cache, error) {
	// WAL: unlimited concurrent readers, one writer, writers queue in ms.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS cache(
		key TEXT PRIMARY KEY,
		blob BLOB NOT NULL,
		fetched_at INTEGER NOT NULL
	)`); err != nil {
		return nil, err
	}
	return &sqliteCache{db: db, dsn: dsn}, nil
}

func (c *sqliteCache) Get(key string) ([]byte, bool) {
	var blob []byte
	var fetchedAt int64
	err := c.db.QueryRow(`SELECT blob, fetched_at FROM cache WHERE key = ?`, key).
		Scan(&blob, &fetchedAt)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("cache: Get error for %q: %v", key, err)
		}
		return nil, false
	}
	if time.Since(time.Unix(fetchedAt, 0)) > 24*time.Hour {
		return nil, false // stale; caller rebuilds
	}
	return blob, true
}

func (c *sqliteCache) Put(key string, blob []byte, ttl time.Duration) {
	_, err := c.db.Exec(
		`INSERT INTO cache(key, blob, fetched_at) VALUES(?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET blob = excluded.blob, fetched_at = excluded.fetched_at`,
		key, blob, time.Now().Unix())
	if err != nil {
		log.Printf("cache: Put error for %q: %v", key, err)
	}
}
