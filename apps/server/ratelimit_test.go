package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemLimiterFixedWindow(t *testing.T) {
	l := newMemLimiter()
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("req:1.2.3.4", 3, time.Minute); !ok {
			t.Fatalf("request %d denied under the limit", i+1)
		}
	}
	ok, retry := l.allow("req:1.2.3.4", 3, time.Minute)
	if ok || retry <= 0 || retry > time.Minute {
		t.Fatalf("4th request: ok=%v retry=%v, want denied with a retry hint", ok, retry)
	}
	if ok, _ := l.allow("req:5.6.7.8", 3, time.Minute); !ok {
		t.Fatal("a different visitor was limited by someone else's usage")
	}
	l.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if ok, _ := l.allow("req:1.2.3.4", 3, time.Minute); !ok {
		t.Fatal("limit did not reset after the window")
	}
}

// On Vercel the platform overwrites x-real-ip, so it is the visitor. Anywhere
// else those headers are client-controlled and must be ignored unless the
// operator says a trusted proxy sets them.
func TestClientIPTrustsOnlyPlatformHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.9:5555"
	r.Header.Set("X-Real-IP", "6.6.6.6")
	r.Header.Set("X-Forwarded-For", "7.7.7.7, 8.8.8.8")

	t.Setenv("VERCEL", "")
	t.Setenv("TRUST_PROXY_HEADERS", "")
	if got := clientIP(r); got != "10.0.0.9" {
		t.Errorf("direct: clientIP = %q, want the socket address", got)
	}
	t.Setenv("TRUST_PROXY_HEADERS", "true")
	if got := clientIP(r); got != "8.8.8.8" {
		t.Errorf("behind a proxy: clientIP = %q, want the entry the proxy appended", got)
	}
	t.Setenv("TRUST_PROXY_HEADERS", "")
	t.Setenv("VERCEL", "1")
	if got := clientIP(r); got != "6.6.6.6" {
		t.Errorf("vercel: clientIP = %q, want x-real-ip", got)
	}
	// One IPv6 subscriber usually owns a whole /64; rotating within it must
	// not mint fresh budgets.
	r.Header.Set("X-Real-IP", "2001:db8:1:2:aaaa::1")
	a := clientIP(r)
	r.Header.Set("X-Real-IP", "2001:db8:1:2:bbbb::9")
	if b := clientIP(r); a != b || !strings.HasPrefix(a, "2001:db8:1:2::") {
		t.Errorf("IPv6 not grouped by /64: %q vs %q", a, b)
	}
}

func TestRateLimitMiddlewareJSONAndStream(t *testing.T) {
	t.Setenv("VERCEL", "")
	s := &server{limits: limits{limiter: newMemLimiter(), requests: 2, window: time.Minute}}
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	plain := s.rateLimited(ok, false)
	stream := s.rateLimited(ok, true)
	call := func(h http.HandlerFunc) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/starmap?repo=a/b", nil)
		r.RemoteAddr = "1.2.3.4:1"
		h(w, r)
		return w
	}
	call(plain)
	call(plain)
	w := call(plain)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over limit: status=%d retry=%q, want 429 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
	if !strings.Contains(w.Body.String(), `"too_many_requests"`) {
		t.Errorf("body = %s", w.Body.String())
	}
	// A started NDJSON stream reports failures in-band (backend invariant 7).
	w = call(stream)
	var frame progressEvent
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &frame) != nil ||
		frame.Phase != "error" || frame.Code != "too_many_requests" {
		t.Fatalf("stream over limit: status=%d body=%s", w.Code, w.Body.String())
	}
}

// Cold builds cost a clone; they get their own, tighter per-visitor budget,
// carried through the request context to where a build actually starts.
func TestBuildBudgetPerVisitor(t *testing.T) {
	s := &server{limits: limits{limiter: newMemLimiter(), builds: 1, buildWindow: time.Minute}}
	ctx := withVisitor(context.Background(), "1.2.3.4")
	if err := s.allowBuild(ctx); err != nil {
		t.Fatalf("first build refused: %v", err)
	}
	if err := s.allowBuild(ctx); err == nil || err.(*apiError).code != "too_many_requests" {
		t.Fatalf("second build: err = %v, want too_many_requests", err)
	}
	// Badges carry no visitor (README proxies share IPs); they are not limited.
	if err := s.allowBuild(context.Background()); err != nil {
		t.Fatalf("anonymous build refused: %v", err)
	}
}

func TestRedisLimiterSharedWindow(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args []any
		_ = json.NewDecoder(r.Body).Decode(&args)
		if len(args) != 5 || args[0] != "EVAL" {
			t.Errorf("unexpected command %v", args)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		key := args[3].(string)
		counts[key]++
		n := counts[key]
		mu.Unlock()
		_, _ = w.Write([]byte(`{"result":[` + itoa(n) + `,42]}`))
	}))
	defer srv.Close()
	iface, err := newRedisCacheWithClient(srv.URL, "t", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	l := redisLimiter{iface.(*redisCache)}
	if ok, _ := l.allow("req:1.2.3.4", 1, time.Minute); !ok {
		t.Fatal("first request denied")
	}
	ok, retry := l.allow("req:1.2.3.4", 1, time.Minute)
	if ok || retry != 42*time.Second {
		t.Fatalf("second request ok=%v retry=%v, want denied with the key's TTL", ok, retry)
	}
}

// Redis being down must not take the site down with it: the limiter fails
// open and the global clone queue still bounds the work.
func TestRedisLimiterFailsOpen(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	iface, _ := newRedisCacheWithClient(srv.URL, "t", srv.Client())
	if ok, _ := (redisLimiter{iface.(*redisCache)}).allow("req:x", 1, time.Minute); !ok {
		t.Fatal("limiter failed closed during a Redis outage")
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// GitHub names are case-insensitive. Every spelling must share one cache
// entry, one mirror and one deterministic layout.
func TestCanonicalRepo(t *testing.T) {
	if got := canonicalRepo("Facebook/React"); got != "facebook/react" {
		t.Fatalf("canonicalRepo = %q", got)
	}
}

func TestGalaxyGIFAcceptsAnyRepoCase(t *testing.T) {
	contract := galaxyGIFFixture()
	data, _ := json.Marshal(contract)
	h := NewGalaxyGIFHandler(func(_ context.Context, repo string) ([]byte, error) {
		if repo != contract.Repo {
			t.Errorf("build called with %q, want canonical %q", repo, contract.Repo)
		}
		return data, nil
	})
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/api/badge.gif?repo="+strings.ToUpper(contract.Repo), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("mixed-case badge: status %d %s", w.Code, w.Body.String())
	}
}
