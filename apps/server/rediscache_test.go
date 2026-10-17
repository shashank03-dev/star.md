package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	redisTestKey  = "org/repo@0123456789abcdef0123456789abcdef01234567#v2-s1400-b8"
	redisTestBlob = `{"repo":"org/repo","headSha":"01234567","commitTotal":1,"scale":{"stars":1,"span":1,"turns":1,"duration":1},"main":[],"branches":[]}`
)

// A TLS test server emulates the REST command endpoint shared by two app
// instances. It also advances a virtual Redis clock to check expiry without
// sleeping for the TTL.
func TestRedisCacheSharedHitsMissesAndTTL(t *testing.T) {
	type entry struct {
		value string
		until time.Time
	}
	var mu sync.Mutex
	stored := map[string]entry{}
	clock := time.Unix(100, 0)
	var commands []string
	var ttlSeconds int64
	const token = "private-test-token"

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" || r.URL.RawQuery != "" {
			t.Errorf("request target = %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var args []json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Errorf("decode command: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var operation, key string
		if len(args) < 2 || json.Unmarshal(args[0], &operation) != nil || json.Unmarshal(args[1], &key) != nil {
			t.Errorf("invalid command: %s", args)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		commands = append(commands, operation)
		if key != redisContractKeyPrefix+redisTestKey && key != redisContractKeyPrefix+"unseen" {
			t.Errorf("key was not namespaced correctly: %q", key)
		}
		w.Header().Set("Content-Type", "application/json")
		switch operation {
		case "SET":
			var value, ex string
			var seconds int64
			if len(args) != 5 || json.Unmarshal(args[2], &value) != nil ||
				json.Unmarshal(args[3], &ex) != nil || json.Unmarshal(args[4], &seconds) != nil || ex != "EX" {
				t.Errorf("bad SET arguments: %s", args)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			ttlSeconds = seconds
			stored[key] = entry{value: value, until: clock.Add(time.Duration(seconds) * time.Second)}
			_, _ = io.WriteString(w, `{"result":"OK"}`)
		case "GET":
			if len(args) != 2 {
				t.Errorf("bad GET arguments: %s", args)
			}
			item, exists := stored[key]
			if !exists || !clock.Before(item.until) {
				_, _ = io.WriteString(w, `{"result":null}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"result": item.value})
		default:
			t.Errorf("unexpected command %q", operation)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	c1, err := newRedisCacheWithClient(srv.URL, token, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	c2, err := newRedisCacheWithClient(srv.URL, token, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c1.Get("unseen"); ok {
		t.Fatal("GET for nonexistent key was a hit")
	}
	c1.Put(redisTestKey, []byte(redisTestBlob), 1501*time.Millisecond)
	mu.Lock()
	if ttlSeconds != 2 {
		t.Errorf("SET EX = %d, want ceil(1501ms) = 2", ttlSeconds)
	}
	mu.Unlock()
	if got, ok := c2.Get(redisTestKey); !ok || string(got) != redisTestBlob {
		t.Fatalf("second instance GET = %q, %v", got, ok)
	}
	mu.Lock()
	clock = clock.Add(time.Second)
	mu.Unlock()
	if _, ok := c1.Get(redisTestKey); !ok {
		t.Fatal("value expired before its TTL")
	}
	mu.Lock()
	clock = clock.Add(time.Second)
	mu.Unlock()
	if _, ok := c2.Get(redisTestKey); ok {
		t.Fatal("expired value was returned")
	}
	c1.Put(redisTestKey, []byte(redisTestBlob), 24*time.Hour)
	mu.Lock()
	if ttlSeconds != 86400 {
		t.Errorf("24h SET EX = %d, want 86400", ttlSeconds)
	}
	if len(commands) != 6 {
		t.Errorf("recorded %d commands (%v), want 6", len(commands), commands)
	}
	mu.Unlock()
}

func TestRedisCacheConfigurationRejectsUnsafeURLs(t *testing.T) {
	const token = "top-secret-credentials"
	for _, endpoint := range []string{
		"", "http://redis.example", "ftp://redis.example", "https://", "https://user:password@redis.example",
		"https://redis.example/path", "https://redis.example/?token=secret", "https://redis.example/#token",
		"https://redis.example/\n"} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := newRedisCache(endpoint, token)
			if err == nil {
				t.Fatal("unsafe endpoint was accepted")
			}
			if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "password") {
				t.Fatalf("configuration error leaked credentials: %v", err)
			}
		})
	}
	if _, err := newRedisCache("https://redis.example", ""); err == nil {
		t.Fatal("empty token was accepted")
	}
	if _, err := newRedisCache("https://redis.example", "token"); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
}

func TestRedisCacheRejectsBrokenRESTResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{"unauthorized", 401, `{"error":"invalid token"}`},
		{"rate limited", 429, `{"error":"quota"}`},
		{"server error", 500, `{"error":"unavailable"}`},
		{"redis error", 200, `{"error":"WRONGPASS"}`},
		{"missing field", 200, `{}`},
		{"bad envelope", 200, `broken`},
		{"wrong result type", 200, `{"result":123}`},
		{"null result", 200, `{"result":null}`},
		{"corrupt value", 200, `{"result":"{not JSON}"}`},
		{"scalar value", 200, `{"result":"42"}`},
		{"oversized value", 200, `{"result":"` + strings.Repeat("a", redisMaxBlobBytes+1) + `"}`},
		{"oversized response", 200, strings.Repeat(" ", redisMaxResponseBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c, err := newRedisCacheWithClient(srv.URL, "token", srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			if data, ok := c.Get(redisTestKey); ok || data != nil {
				t.Fatalf("broken response accepted as hit: %q, %v", data, ok)
			}
			c.Put(redisTestKey, []byte(redisTestBlob), time.Hour) // failures must be best-effort
		})
	}
}

func TestRedisCacheRejectsInvalidWritesBeforeNetwork(t *testing.T) {
	var calls int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"result":null}`)
	}))
	defer srv.Close()
	c, err := newRedisCacheWithClient(srv.URL, "token", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  string
		blob []byte
		ttl  time.Duration
	}{
		{redisTestKey, []byte(redisTestBlob), 0},
		{redisTestKey, []byte(redisTestBlob), -time.Second},
		{"", []byte(redisTestBlob), time.Hour},
		{strings.Repeat("x", redisMaxKeyBytes+1), []byte(redisTestBlob), time.Hour},
		{redisTestKey, []byte("broken"), time.Hour},
		{redisTestKey, []byte("[]"), time.Hour},
		{redisTestKey, []byte("null"), time.Hour},
		{redisTestKey, []byte(strings.Repeat("x", redisMaxBlobBytes+1)), time.Hour},
	} {
		c.Put(tc.key, tc.blob, tc.ttl)
	}
	if _, ok := c.Get(""); ok {
		t.Fatal("empty key was accepted by GET")
	}
	if calls != 0 {
		t.Fatalf("made %d network calls with invalid inputs", calls)
	}
}

func TestRedisCacheTimeoutAndNoCredentialLeak(t *testing.T) {
	const secret = "a-sensitive-token"
	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"error":"`+secret+`"}`)
	}))
	defer srv.Close()
	c, err := newRedisCacheWithClient(srv.URL, secret, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(redisTestKey); ok {
		t.Fatal("server error was a cache hit")
	}
	c.Put(redisTestKey, []byte(redisTestBlob), time.Hour)
	if strings.Contains(logs.String(), secret) {
		t.Fatal("redis error leaked token to logs")
	}

	// Intercept the transport and check the actual deadline on both requests.
	// The handler need not sleep for two seconds to prove timeout coverage.
	deadlines := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > cacheOpTimeout || time.Until(deadline) < 0 {
			t.Errorf("request missing bounded timeout: %v, %v", deadline, ok)
		}
		deadlines++
		return nil, context.DeadlineExceeded
	})}
	c, err = newRedisCacheWithClient("https://redis.example", secret, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(redisTestKey); ok {
		t.Fatal("timed out GET returned a hit")
	}
	c.Put(redisTestKey, []byte(redisTestBlob), time.Hour)
	if deadlines != 2 {
		t.Errorf("requests with deadlines = %d, want 2", deadlines)
	}
}

func TestRedisCacheStalledHTTPIsBounded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hold the reply beyond the client's deadline. The explicit release
		// also lets httptest.Close finish on platforms where the server does
		// not observe the disconnected POST before consuming its request body.
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	base := srv.Client()
	base.Timeout = 10 * time.Second
	c, err := newRedisCacheWithClient(srv.URL, "token", base)
	if err != nil {
		t.Fatal(err)
	}
	if base.Timeout != 10*time.Second {
		t.Fatal("constructor changed the caller's HTTP client")
	}
	start := time.Now()
	if _, ok := c.Get(redisTestKey); ok {
		t.Fatal("stalled GET returned a hit")
	}
	if elapsed := time.Since(start); elapsed > cacheOpTimeout+time.Second {
		t.Fatalf("stalled request took %v, timeout is %v", elapsed, cacheOpTimeout)
	}
}

func TestRedisCacheDoesNotFollowRedirectsWithCredentials(t *testing.T) {
	var visited int
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visited++
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, err := newRedisCacheWithClient(source.URL, "secret", source.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(redisTestKey); ok {
		t.Fatal("redirect counted as a hit")
	}
	c.Put(redisTestKey, []byte(redisTestBlob), time.Hour)
	if visited != 0 {
		t.Fatalf("forwarded credentials to redirected server %d times", visited)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
