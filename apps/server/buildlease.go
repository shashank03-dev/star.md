package main

// Distributed singleflight for Vercel instances. In-process singleflight
// deduplicates one worker; Redis leases deduplicate workers on different
// machines, preventing a burst of requests from triggering the same clone.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

const buildLeasePrefix = "stellar:build:v1:"

// tryBuildLease returns acquired=false when another worker has the lease or
// Redis is unavailable. Fail closed under load instead of stampeding GitHub.
// Leases have finite TTL so a killed Function cannot hold a key forever.
func (c *redisCache) tryBuildLease(key string) (release func(), acquired bool) {
	if c == nil || !redisKeyOK(key) {
		return nil, false
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, false
	}
	token := hex.EncodeToString(id[:])
	lockKey := buildLeasePrefix + key
	result, ok := c.command("SET", lockKey, token, "NX", "EX", 240)
	if !ok {
		return nil, false
	}
	var status string
	if json.Unmarshal(result, &status) != nil || status != "OK" {
		return nil, false
	}
	return func() {
		// Atomic compare-and-delete prevents an expired owner's cleanup from
		// deleting a lease that was subsequently acquired by another worker.
		const script = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end`
		c.command("EVAL", script, 1, lockKey, token)
	}, true
}

// waitForBuild polls a bounded time for the winner's result. The caller can
// cancel immediately, and clients get a typed 503 busy when build capacity is
// temporarily exhausted. Redis REST calls have their own short HTTP timeout.
func (s *server) waitForBuild(ctx context.Context, key string) ([]byte, error) {
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errBusy
		case <-ticker.C:
			if blob, ok := s.cache.Get(key); ok && len(blob) > 0 {
				return blob, nil
			}
		}
	}
}
