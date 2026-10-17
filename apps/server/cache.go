package main

import (
	"context"
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
		expires_at INTEGER NOT NULL
	)`); err != nil {
		return nil, err
	}
	// Migrate pre-expiry tables (the column used to hold the write time).
	// Best-effort by design: on a fresh table the column is already named
	// expires_at and the rename fails harmlessly. Rows from before the
	// migration read as already expired, so the cache simply rewarns itself.
	_, _ = db.Exec(`ALTER TABLE cache RENAME COLUMN fetched_at TO expires_at`)
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS cache_expires_at ON cache(expires_at)`); err != nil {
		return nil, err
	}
	// Prune once at startup so a long-idle cache reclaims its expired rows
	// even if no later Put ever runs (pruning also rides every Put).
	if _, err := db.Exec(`DELETE FROM cache WHERE expires_at <= ?`, time.Now().Unix()); err != nil {
		log.Printf("cache: startup prune failed: %v", err)
	}
	return &sqliteCache{db: db, dsn: dsn}, nil
}

// cacheOpTimeout bounds every sqlite call: the cache is best-effort, so a
// stalled disk should surface as a miss (or a swallowed write error), never as
// a pinned handler.
const cacheOpTimeout = 2 * time.Second

func (c *sqliteCache) Get(key string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheOpTimeout)
	defer cancel()
	var blob []byte
	var expiresAt int64
	err := c.db.QueryRowContext(ctx, `SELECT blob, expires_at FROM cache WHERE key = ?`, key).
		Scan(&blob, &expiresAt)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("cache: Get error for %q: %v", key, err)
		}
		return nil, false
	}
	if time.Now().Unix() >= expiresAt {
		return nil, false // expired; caller rebuilds
	}
	return blob, true
}

func (c *sqliteCache) Put(key string, blob []byte, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheOpTimeout)
	defer cancel()
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO cache(key, blob, expires_at) VALUES(?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET blob = excluded.blob, expires_at = excluded.expires_at`,
		key, blob, time.Now().Add(ttl).Unix())
	if err != nil {
		log.Printf("cache: Put error for %q: %v", key, err)
		return
	}
	// Opportunistic expiry: nothing else ever deletes from this table, so
	// without this the cache would keep every repo@sha contract it has ever
	// served. The expiry index makes this a range delete. The <= matches Get
	// ("expired at this second" is not live), so no row can be served while
	// prune considers it alive.
	if _, err := c.db.ExecContext(ctx, `DELETE FROM cache WHERE expires_at <= ?`, time.Now().Unix()); err != nil {
		log.Printf("cache: prune error: %v", err)
	}
}
