package main

// Per-visitor rate limiting. Two budgets per client IP:
//
//   - requests: every /api/starmap call (each one costs at least a memoized
//     `git ls-remote` against GitHub);
//   - builds: cache misses that actually start a clone, the expensive part.
//
// On Vercel the counters live in Upstash Redis, so every Function instance
// shares one budget per visitor; locally they live in memory. The limiter fails
// open: a Redis outage must not take the site down, and the global clone queue
// (CLONE_CONCURRENCY / CLONE_QUEUE_MAX) still bounds total work.
//
// README badges are deliberately not limited per visitor: GitHub serves every
// README image through its shared camo proxy, so one "visitor" there is
// everyone's README at once.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type limiter interface {
	// allow counts one hit against key and reports whether it fits in limit
	// per window, plus how long until the window resets when it does not.
	allow(key string, limit int, window time.Duration) (ok bool, retryAfter time.Duration)
}

type limits struct {
	limiter     limiter
	requests    int // per window, per visitor; 0 disables
	window      time.Duration
	builds      int // cold builds per buildWindow, per visitor; 0 disables
	buildWindow time.Duration
}

func configuredLimits(cache Cache) limits {
	l := limits{
		requests:    envInt("RATE_LIMIT_REQUESTS", 60),
		window:      envDur("RATE_LIMIT_WINDOW", time.Minute),
		builds:      envInt("RATE_LIMIT_BUILDS", 10),
		buildWindow: envDur("RATE_LIMIT_BUILD_WINDOW", 10*time.Minute),
	}
	if getenv("RATE_LIMIT_DISABLED") == "true" {
		l.requests, l.builds = 0, 0
	}
	if shared, ok := cache.(*redisCache); ok {
		l.limiter = redisLimiter{shared}
	} else {
		l.limiter = newMemLimiter()
	}
	return l
}

var errTooManyRequests = func(retry time.Duration) *apiError {
	return &apiError{status: http.StatusTooManyRequests, code: "too_many_requests",
		msg:        "too many requests from this address; retry in " + retrySeconds(retry) + "s",
		retryAfter: retry}
}

func retrySeconds(d time.Duration) string {
	s := int64((d + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return strconv.FormatInt(s, 10)
}

// rateLimited wraps an API handler with the per-visitor request budget and
// carries the visitor into the context for the build budget. The stream
// endpoint reports a refusal in-band as a phase:"error" frame, because the
// frontend reader treats any non-200 as an unreadable stream.
func (s *server) rateLimited(next http.HandlerFunc, stream bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if s.limits.limiter != nil && s.limits.requests > 0 && ip != "" {
			if ok, retry := s.limits.limiter.allow("req:"+ip, s.limits.requests, s.limits.window); !ok {
				e := errTooManyRequests(retry)
				w.Header().Set("Retry-After", retrySeconds(retry))
				w.Header().Set("Access-Control-Allow-Origin", "*")
				if stream {
					w.Header().Set("Content-Type", "application/x-ndjson")
					w.Header().Set("Cache-Control", "no-cache")
					_ = json.NewEncoder(w).Encode(progressEvent{Phase: "error", Code: e.code, Message: e.msg})
					return
				}
				writeErr(w, e)
				return
			}
		}
		next(w, r.WithContext(withVisitor(r.Context(), ip)))
	}
}

type visitorKey struct{}

func withVisitor(ctx context.Context, ip string) context.Context {
	if ip == "" {
		return ctx
	}
	return context.WithValue(ctx, visitorKey{}, ip)
}

// allowBuild spends one unit of the visitor's cold-build budget. Requests
// without a visitor (badges, internal callers) are not limited here.
func (s *server) allowBuild(ctx context.Context) error {
	ip, _ := ctx.Value(visitorKey{}).(string)
	if ip == "" || s.limits.limiter == nil || s.limits.builds <= 0 {
		return nil
	}
	if ok, retry := s.limits.limiter.allow("build:"+ip, s.limits.builds, s.limits.buildWindow); !ok {
		return errTooManyRequests(retry)
	}
	return nil
}

// clientIP identifies the visitor. Vercel overwrites x-real-ip /
// x-forwarded-for at its edge, so there they cannot be spoofed. Anywhere else
// those headers are whatever the client sent, so only the socket address is
// trusted unless TRUST_PROXY_HEADERS says a reverse proxy appends the real
// client to X-Forwarded-For (its rightmost entry). IPv6 is grouped by /64,
// the block one subscriber is normally given.
func clientIP(r *http.Request) string {
	raw := ""
	switch {
	case getenv("VERCEL") != "":
		raw = r.Header.Get("X-Real-IP")
		if raw == "" {
			raw, _, _ = strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
		}
	case getenv("TRUST_PROXY_HEADERS") == "true":
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			raw = parts[len(parts)-1]
		}
	}
	if raw == "" {
		raw = r.RemoteAddr
		if host, _, err := net.SplitHostPort(raw); err == nil {
			raw = host
		}
	}
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return ""
	}
	if ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String()
	}
	return ip.String()
}

// ---- in-memory fixed window (single instance) ----

type memWindow struct {
	start time.Time
	n     int
}

type memLimiter struct {
	mu  sync.Mutex
	m   map[string]memWindow
	now func() time.Time
}

func newMemLimiter() *memLimiter {
	return &memLimiter{m: map[string]memWindow{}, now: time.Now}
}

func (l *memLimiter) allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w, ok := l.m[key]
	if !ok || now.Sub(w.start) >= window {
		if len(l.m) >= 100_000 {
			// Bounded memory: drop expired windows, or everything if a flood
			// of distinct addresses left nothing expired.
			for k, v := range l.m {
				if now.Sub(v.start) >= window {
					delete(l.m, k)
				}
			}
			if len(l.m) >= 100_000 {
				clear(l.m)
			}
		}
		w = memWindow{start: now}
	}
	w.n++
	l.m[key] = w
	if w.n > limit {
		return false, window - now.Sub(w.start)
	}
	return true, 0
}

// ---- Redis fixed window (shared across Vercel instances) ----

type redisLimiter struct{ c *redisCache }

const rateLimitPrefix = "stellar:rl:v1:"

// One round trip: increment, start the window on the first hit, and return
// the count with the remaining TTL for Retry-After.
const rateLimitScript = `local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
return {n, redis.call('TTL', KEYS[1])}`

func (l redisLimiter) allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	secs := int64(window / time.Second)
	if secs < 1 {
		secs = 1
	}
	result, ok := l.c.command("EVAL", rateLimitScript, 1, rateLimitPrefix+key, secs)
	if !ok {
		return true, 0 // fail open
	}
	var reply []int64
	if json.Unmarshal(result, &reply) != nil || len(reply) != 2 {
		return true, 0
	}
	if reply[0] > int64(limit) {
		ttl := reply[1]
		if ttl < 1 {
			ttl = secs
		}
		return false, time.Duration(ttl) * time.Second
	}
	return true, 0
}

// canonicalRepo folds a validated owner/name to the one spelling used for
// cache keys, mirrors, leases and the contract's layout seed. GitHub owner and
// repository names are case-insensitive, so "Facebook/React" and
// "facebook/react" are the same galaxy.
func canonicalRepo(repo string) string { return strings.ToLower(repo) }
