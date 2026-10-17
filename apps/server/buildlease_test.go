package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRedisBuildLeaseExclusiveAndOwnerSafe(t *testing.T) {
	var mu sync.Mutex
	var owner string
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args []json.RawMessage
		if json.NewDecoder(r.Body).Decode(&args) != nil || len(args) < 2 {
			w.WriteHeader(400)
			return
		}
		var op string
		_ = json.Unmarshal(args[0], &op)
		mu.Lock()
		defer mu.Unlock()
		requests++
		switch op {
		case "SET":
			if len(args) != 6 { // SET key token NX EX 240
				t.Errorf("wrong lease command length: %d", len(args))
			}
			var candidate string
			_ = json.Unmarshal(args[2], &candidate)
			if owner != "" {
				_, _ = w.Write([]byte(`{"result":null}`))
			} else {
				owner = candidate
				_, _ = w.Write([]byte(`{"result":"OK"}`))
			}
		case "EVAL":
			if len(args) != 5 {
				t.Errorf("wrong EVAL command length: %d", len(args))
			}
			var releasing string
			_ = json.Unmarshal(args[4], &releasing)
			if owner == releasing {
				owner = ""
			}
			_, _ = w.Write([]byte(`{"result":1}`))
		default:
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	iface, err := newRedisCacheWithClient(srv.URL, "test-token", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	cache := iface.(*redisCache)
	release1, ok := cache.tryBuildLease(redisTestKey)
	if !ok {
		t.Fatal("first instance did not obtain lease")
	}
	if _, ok := cache.tryBuildLease(redisTestKey); ok {
		t.Fatal("second instance obtained duplicate lease")
	}
	release1()
	release2, ok := cache.tryBuildLease(redisTestKey)
	if !ok {
		t.Fatal("lease not available after release")
	}
	release1() // old owner must not delete a new owner's lock
	if _, ok := cache.tryBuildLease(redisTestKey); ok {
		t.Fatal("old owner deleted the new owner's lease")
	}
	release2()
	mu.Lock()
	defer mu.Unlock()
	if owner != "" || requests != 7 {
		t.Fatalf("unexpected lock state owner=%q requests=%d", owner, requests)
	}
}

func TestWaitingForSharedBuildHonorsCancellation(t *testing.T) {
	s := &server{cache: bestEffort{inner: nil}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := s.waitForBuild(ctx, "org/repo", redisTestKey); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancelled wait was too slow")
	}
}

// fakeLeaseRedis answers MGET contract/lease/tooLarge with whatever state()
// returns for the n-th poll; status 500 simulates Redis being unreachable.
func fakeLeaseRedis(t *testing.T, state func(n int) (status int, reply string)) *redisCache {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args []string
		_ = json.NewDecoder(r.Body).Decode(&args)
		if len(args) != 4 || args[0] != "MGET" {
			t.Errorf("unexpected command %v", args)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		n++
		status, reply := state(n)
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	iface, err := newRedisCacheWithClient(srv.URL, "test-token", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return iface.(*redisCache)
}

func leaseReply(blob, lease, tooLarge string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		b, _ := json.Marshal(s)
		return string(b)
	}
	return `{"result":[` + q(blob) + `,` + q(lease) + `,` + q(tooLarge) + `]}`
}

// A waiter must keep waiting while the winner still holds the lease (a cold
// build may run for minutes) and return the winner's contract once cached.
func TestWaitForBuildWaitsWhileLeaseHeld(t *testing.T) {
	lease := fakeLeaseRedis(t, func(n int) (int, string) {
		if n < 3 {
			return 200, leaseReply("", "owner", "")
		}
		return 200, leaseReply(redisTestBlob, "", "")
	})
	s := &server{lease: lease}
	blob, err := s.waitForBuild(context.Background(), "org/repo", redisTestKey)
	if err != nil || string(blob) != redisTestBlob {
		t.Fatalf("waitForBuild = %q, %v; want the winner's contract", blob, err)
	}
}

// When the winner releases its lease without caching anything, its build
// failed: the waiter must stop at once instead of polling until it times out
// as "busy".
func TestWaitForBuildStopsWhenLeaseReleasedWithoutResult(t *testing.T) {
	lease := fakeLeaseRedis(t, func(int) (int, string) { return 200, leaseReply("", "", "") })
	s := &server{lease: lease}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.waitForBuild(ctx, "org/repo", redisTestKey); err != errLeaseReleased {
		t.Fatalf("err = %v, want errLeaseReleased", err)
	}
}

// The winner's repo_too_large is shared, so waiters report it rather than
// re-downloading the repo themselves.
func TestWaitForBuildReportsSharedTooLarge(t *testing.T) {
	lease := fakeLeaseRedis(t, func(int) (int, string) { return 200, leaseReply("", "owner", "1") })
	s := &server{lease: lease}
	if _, err := s.waitForBuild(context.Background(), "org/repo", redisTestKey); err != errRepoTooLarge {
		t.Fatalf("err = %v, want repo_too_large", err)
	}
}

// A Redis outage must fail closed quickly, not hold every cold request for
// the whole lease TTL.
func TestWaitForBuildFailsFastWhenRedisDown(t *testing.T) {
	lease := fakeLeaseRedis(t, func(int) (int, string) { return 500, "" })
	s := &server{lease: lease}
	start := time.Now()
	if _, err := s.waitForBuild(context.Background(), "org/repo", redisTestKey); err != errBusy {
		t.Fatalf("err = %v, want busy", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("waited %v during a Redis outage", time.Since(start))
	}
}
