package main

// Distributed singleflight for Vercel instances. In-process singleflight
// deduplicates one worker; Redis leases deduplicate workers on different
// machines, preventing a burst of requests from triggering the same clone.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const (
	buildLeasePrefix = "stellar:build:v1:"
	// tooLargePrefix shares gitSource's repo_too_large memo across instances,
	// so an oversized repo is not re-downloaded to the cap by every Function.
	tooLargePrefix = "stellar:toolarge:v1:"
	// buildLeaseTTL outlives the longest build a winner may legitimately run
	// (Vercel CLONE_TIMEOUT 150s + the 60s PR fallback) and bounds how long a
	// crashed winner can block others.
	buildLeaseTTL = 240
)

// errLeaseReleased means the winning instance gave up its lease without
// caching a contract: its build failed. The waiter should try to build itself
// rather than keep polling for a result that will never arrive.
var errLeaseReleased = errors.New("build lease released without a result")

// leaseWait caps how long a request waits on another instance's build. It
// matches the lease TTL: past that the winner is gone either way. A var so
// tests can shorten it.
var leaseWait = buildLeaseTTL * time.Second

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
	result, ok := c.command("SET", lockKey, token, "NX", "EX", buildLeaseTTL)
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

// leaseState reads, in one round trip, the cached contract, whether the build
// lease is still held, and whether the repo is marked too large. ok is false
// when Redis could not answer.
func (c *redisCache) leaseState(repo, key string) (blob []byte, held, tooLarge, ok bool) {
	if c == nil || !redisKeyOK(key) || !redisKeyOK(repo) {
		return nil, false, false, false
	}
	result, ok := c.command("MGET", redisContractKeyPrefix+key, buildLeasePrefix+key, tooLargePrefix+repo)
	if !ok {
		return nil, false, false, false
	}
	var vals []*string
	if json.Unmarshal(result, &vals) != nil || len(vals) != 3 {
		return nil, false, false, false
	}
	if vals[0] != nil && redisContractJSONOK([]byte(*vals[0])) {
		blob = []byte(*vals[0])
	}
	return blob, vals[1] != nil, vals[2] != nil, true
}

// knownTooLarge reports whether any instance recently found repo over the cap.
func (c *redisCache) knownTooLarge(repo string) bool {
	if c == nil || !redisKeyOK(repo) {
		return false
	}
	_, ok := c.command("GET", tooLargePrefix+repo)
	return ok // a missing key replies null, which command() reports as !ok
}

func (c *redisCache) rememberTooLarge(repo string) {
	if c == nil || !redisKeyOK(repo) {
		return
	}
	c.command("SET", tooLargePrefix+repo, "1", "EX", int64(tooLargeTTL/time.Second))
}

// waitForBuild polls for the winning instance's result for as long as it still
// holds the build lease - a cold build may legitimately run for minutes, and
// giving up after a fixed few seconds turned a second visitor into a spurious
// "busy" error. It returns errLeaseReleased as soon as the winner lets go
// without a result, errRepoTooLarge if the winner hit the size cap, and a
// typed 503 busy if Redis stops answering or the wait outlives the lease. The
// caller can cancel immediately.
func (s *server) waitForBuild(ctx context.Context, repo, key string) ([]byte, error) {
	deadline := time.NewTimer(leaseWait)
	defer deadline.Stop()
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errBusy
		case <-ticker.C:
			if s.lease == nil {
				if blob, ok := s.cache.Get(key); ok && len(blob) > 0 {
					return blob, nil
				}
				continue
			}
			blob, held, tooLarge, ok := s.lease.leaseState(repo, key)
			switch {
			case !ok:
				// Fail closed, but not for the whole lease TTL: a Redis outage
				// must not hang every cold request for minutes.
				if failures++; failures >= 3 {
					return nil, errBusy
				}
			case blob != nil:
				return blob, nil
			case tooLarge:
				return nil, errRepoTooLarge
			case !held:
				return nil, errLeaseReleased
			default:
				failures = 0
			}
		}
	}
}
