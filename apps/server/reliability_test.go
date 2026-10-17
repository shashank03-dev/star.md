package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestColdBuildAdmissionTimesOut(t *testing.T) {
	g := &gitSource{
		dir: t.TempDir(), sem: make(chan struct{}, 1),
		queueMax: 2, queueTimeout: 20 * time.Millisecond,
	}
	g.sem <- struct{}{} // an existing build is using the only slot
	start := time.Now()
	_, err := g.buildSnapshot(context.Background(), "owner/repo", "abc", "main")
	if !errors.Is(err, errBusy) {
		t.Fatalf("queue timeout returned %v, want busy", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("queue deadline ignored: %v", elapsed)
	}
	if got := g.waiting.Load(); got != 0 {
		t.Fatalf("queue leaked %d waiting builds", got)
	}
}

func TestColdBuildAdmissionRejectsOverflow(t *testing.T) {
	g := &gitSource{
		dir: t.TempDir(), sem: make(chan struct{}, 1),
		queueMax: 1, queueTimeout: time.Second,
	}
	g.waiting.Store(1)
	start := time.Now()
	_, err := g.buildSnapshot(context.Background(), "owner/repo", "abc", "main")
	if !errors.Is(err, errBusy) || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("overflow returned %v after %v", err, time.Since(start))
	}
	if got := g.waiting.Load(); got != 1 {
		t.Fatalf("overflow changed active waiter count to %d", got)
	}
}

func TestHeadMemoReducesNetworkProbes(t *testing.T) {
	logPath := fakeGit(t, "lsremote-public")
	g := &gitSource{
		home: t.TempDir(), tokens: &tokenPool{}, probe: time.Second,
		headTTL: 5 * time.Minute,
	}
	for i := 0; i < 2; i++ {
		sha, branch, err := g.remoteHead(context.Background(), "owner/repo")
		if err != nil || sha == "" || branch != "main" {
			t.Fatalf("probe #%d: sha=%q branch=%q err=%v", i, sha, branch, err)
		}
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(log), "ls-remote"); got != 1 {
		t.Fatalf("got %d Git probes, want 1: %s", got, log)
	}
}

func TestVercelDoesNotExposeTokenAccessiblePrivateRepos(t *testing.T) {
	t.Setenv("VERCEL", "1")
	t.Setenv("ALLOW_PRIVATE_REPOS", "")
	logPath := fakeGit(t, "lsremote-private")
	g := &gitSource{home: t.TempDir(), tokens: &tokenPool{tokens: []string{fakeToken}}, probe: time.Second}
	_, _, err := g.remoteHead(context.Background(), "acme/private")
	if !errors.Is(err, errNotFound) {
		t.Fatalf("private lookup = %v, want not_found", err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(log), "ls-remote"); got != 1 {
		t.Fatalf("unexpected authenticated retry: %s", log)
	}
}
