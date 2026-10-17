package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteCacheHonorsTTLAndPrunes(t *testing.T) {
	c, err := newSQLiteCache(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("newSQLiteCache: %v", err)
	}

	c.Put("live", []byte("x"), time.Hour)
	if _, ok := c.Get("live"); !ok {
		t.Fatal("live entry not served")
	}

	// The ttl argument must actually be honored, not just kept for show.
	c.Put("dead", []byte("y"), -time.Second)
	if _, ok := c.Get("dead"); ok {
		t.Fatal("entry past its ttl was served")
	}

	// Putting anything prunes expired rows: nothing else ever deletes from
	// this table, so without this the cache grows forever.
	c.Put("live2", []byte("z"), time.Hour)
	sc := c.(*sqliteCache)
	var n int
	if err := sc.db.QueryRow(`SELECT COUNT(*) FROM cache WHERE key = 'dead'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expired row survived a Put; count = %d", n)
	}
}

// A cache file created before the column held an expiry time must be migrated,
// not silently turned into a table that every Get/Put errors on.
func TestSQLiteCacheMigratesLegacyTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE cache(
		key TEXT PRIMARY KEY,
		blob BLOB NOT NULL,
		fetched_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO cache(key, blob, fetched_at) VALUES('old', ?, ?)`,
		[]byte("b"), time.Now().Add(-48*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	c, err := newSQLiteCache(path)
	if err != nil {
		t.Fatalf("newSQLiteCache on legacy file: %v", err)
	}
	sc := c.(*sqliteCache)

	// Startup prunes rows that can never be served again (a legacy row's
	// write time reads as an already-passed expiry).
	var n int
	if err := sc.db.QueryRow(`SELECT COUNT(*) FROM cache WHERE key = 'old'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expired legacy row survived startup prune; count = %d", n)
	}

	// A fresh Put must both succeed (migration ran) and keep the legacy row
	// pruned.
	c.Put("new", []byte("x"), time.Hour)
	if _, ok := c.Get("new"); !ok {
		t.Fatal("entry written after migration not served")
	}
	if err := sc.db.QueryRow(`SELECT COUNT(*) FROM cache WHERE key = 'old'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("legacy row survived pruning; count = %d", n)
	}
}
