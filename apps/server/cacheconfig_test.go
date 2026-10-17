package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVercelRequiresSharedRedisWithoutSQLiteFallback(t *testing.T) {
	for _, tc := range []struct {
		name, url, token string
	}{
		{name: "missing both"},
		{name: "missing token", url: "https://redis.example.com"},
		{name: "missing url", token: "token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VERCEL", "1")
			t.Setenv("UPSTASH_REDIS_REST_URL", tc.url)
			t.Setenv("UPSTASH_REDIS_REST_TOKEN", tc.token)
			path := filepath.Join(t.TempDir(), "cache.db")
			if cache := configuredCache(path); cache != nil {
				t.Fatal("serverless backend fell back to a local cache")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("serverless backend created SQLite database: %v", err)
			}
		})
	}
}

func TestLocalConfigurationUsesSQLite(t *testing.T) {
	t.Setenv("VERCEL", "")
	t.Setenv("UPSTASH_REDIS_REST_URL", "")
	t.Setenv("UPSTASH_REDIS_REST_TOKEN", "")
	path := filepath.Join(t.TempDir(), "local.db")
	if cache := configuredCache(path); cache == nil {
		t.Fatal("local SQLite fallback unavailable")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SQLite cache was not created: %v", err)
	}
}
