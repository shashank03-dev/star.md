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
	if _, err := s.waitForBuild(ctx, redisTestKey); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancelled wait was too slow")
	}
}
