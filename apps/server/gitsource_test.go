package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// testSource builds a gitSource with just the fields sweep() reads.
func testSource(dir string, timeout, ttl time.Duration, diskCap int64) *gitSource {
	return &gitSource{dir: dir, timeout: timeout, ttl: ttl, diskCap: diskCap, gate: newMirrorGate()}
}

func mkMirror(t *testing.T, dir, name string, age time.Duration, bytes int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if bytes > 0 {
		if err := os.WriteFile(filepath.Join(path, "pack"), make([]byte, bytes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- git shim: exercises the real run/runCapped/retry paths without network ----

const fakeToken = "sekret-token-xyz"

// fakeGit puts a fake `git` on PATH that answers the subcommands the code
// under test invokes. It records every invocation (argv + whether credentials
// were present) to a log so tests can assert on anonymous-first behavior.
func fakeGit(t *testing.T, behavior string) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	body := ""
	switch behavior {
	case "lsremote-public":
		body = `  ls-remote)
    printf 'ref: refs/heads/main\tHEAD\n360a5c7d2f68a80bfb1cff0462e5b2bd6a29c0a3\tHEAD\n'
    exit 0
    ;;
`
	case "lsremote-private":
		// Anonymous git sees a private repo exactly like a missing one.
		body = `  ls-remote)
    if [ -z "${GIT_CONFIG_COUNT:-}" ]; then
      echo "remote: Repository not found." >&2
      exit 128
    fi
    printf 'ref: refs/heads/main\tHEAD\n360a5c7d2f68a80bfb1cff0462e5b2bd6a29c0a3\tHEAD\n'
    exit 0
    ;;
`
	case "lsremote-dns":
		body = `  ls-remote)
    echo "fatal: unable to access 'https://github.com/x/y.git/': Could not resolve host: github.com" >&2
    exit 128
    ;;
`
	case "lsremote-429":
		// Anonymous IP rate limit: credentials would have their own quota.
		body = `  ls-remote)
    if [ -z "${GIT_CONFIG_COUNT:-}" ]; then
      echo "fatal: unable to access 'https://github.com/acme/private.git/': The requested URL returned error: 429" >&2
      exit 128
    fi
    printf 'ref: refs/heads/main\tHEAD\n360a5c7d2f68a80bfb1cff0462e5b2bd6a29c0a3\tHEAD\n'
    exit 0
    ;;
`
	case "lsremote-slow":
		body = `  ls-remote)
    sleep 30
    exit 0
    ;;
`
	case "revlist":
		body = `  rev-list)
    echo 3
    exit 0
    ;;
`
	case "log-partial-failure":
		body = `  log)
    printf 'aaaa\t\t1700000000\tfirst\n'
    echo 'fatal: corrupt object' >&2
    exit 128
    ;;
`
	case "clone-big-fast":
		body = `  clone)
    last=""
    for a in "$@"; do last="$a"; done
    mkdir -p "$last"
    printf 'ref: refs/heads/main\n' > "$last/HEAD"
    dd if=/dev/zero of="$last/pack" bs=1024 count=8 2>/dev/null
    exit 0
    ;;
`
	case "lsremote-429-badtoken":
		// Anonymous IP rate limit; the credential itself is broken, so the
		// reported error must stay the rate limit, not become a 404.
		body = `  ls-remote)
    if [ -z "${GIT_CONFIG_COUNT:-}" ]; then
      echo "fatal: unable to access 'https://github.com/acme/private.git/': The requested URL returned error: 429" >&2
      exit 128
    fi
    echo "fatal: Authentication failed for 'https://github.com/acme/private.git/'" >&2
    exit 128
    ;;
`
	case "clone-parse-slow":
		// A successful 2-commit clone whose clone and rev-list steps linger,
		// so a test can probe the mirror gate while each phase runs.
		body = `  clone)
    last=""
    for a in "$@"; do last="$a"; done
    mkdir -p "$last"
    : > "$last/HEAD"
    sleep 0.8
    exit 0
    ;;
  rev-list)
    sleep 0.8
    echo 2
    exit 0
    ;;
  log)
    printf 'aaa\t\t1700000000\tfirst\nbbb\t\t1700000100\tsecond\n'
    exit 0
    ;;
  for-each-ref)
    exit 0
    ;;
`
	case "clone-private":
		body = `  ls-remote)
    printf 'ref: refs/heads/main\tHEAD\n360a5c7d2f68a80bfb1cff0462e5b2bd6a29c0a3\tHEAD\n'
    exit 0
    ;;
  clone)
    last=""
    for a in "$@"; do last="$a"; done
    if [ -z "${GIT_CONFIG_COUNT:-}" ]; then
      mkdir -p "$last"
      echo partial > "$last/PARTIAL-FROM-ANON"
      echo "remote: Repository not found." >&2
      exit 128
    fi
    mkdir -p "$last"
    printf 'ref: refs/heads/main\n' > "$last/HEAD"
    exit 0
    ;;
`
	}
	script := `#!/bin/sh
log="` + logPath + `"
{ printf 'ARGS:%s\n' "$*"; printf 'AUTHCOUNT:%s\n' "${GIT_CONFIG_COUNT:-}"; printf 'AUTHVAL:%s\n' "${GIT_CONFIG_VALUE_0:-}"; } >> "$log"
sub=""
for a in "$@"; do
  case "$a" in ls-remote|clone|fetch|rev-list|log|for-each-ref) sub="$a"; break;; esac
done
case "$sub" in
` + body + `esac
exit 0
`
	p := filepath.Join(dir, "git")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return logPath
}

func readCallLog(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func countCalls(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "ARGS:") {
			n++
		}
	}
	return n
}

// A public repo must be probed anonymously even when a token is configured,
// and the token must never appear in argv.
func TestRemoteHeadPublicStaysAnonymous(t *testing.T) {
	logPath := fakeGit(t, "lsremote-public")
	g := &gitSource{home: t.TempDir(), probe: time.Minute, tokens: &tokenPool{tokens: []string{fakeToken}}}

	head, branch, err := g.remoteHead(context.Background(), "octocat/Hello-World")
	if err != nil {
		t.Fatalf("remoteHead: %v", err)
	}
	if head != "360a5c7d2f68a80bfb1cff0462e5b2bd6a29c0a3" || branch != "main" {
		t.Fatalf("head=%q branch=%q", head, branch)
	}
	log := readCallLog(t, logPath)
	if n := countCalls(log); n != 1 {
		t.Errorf("git invoked %d times, want 1 (anonymous only)", n)
	}
	if !strings.Contains(log, "AUTHCOUNT:\n") {
		t.Errorf("probe was not anonymous:\n%s", log)
	}
	if strings.Contains(log, fakeToken) {
		t.Errorf("token leaked into argv/log:\n%s", log)
	}
}

// A private repo (anonymous 404) retries with credentials.
func TestRemoteHeadPrivateRetriesAuthed(t *testing.T) {
	logPath := fakeGit(t, "lsremote-private")
	g := &gitSource{home: t.TempDir(), probe: time.Minute, tokens: &tokenPool{tokens: []string{fakeToken}}}

	head, branch, err := g.remoteHead(context.Background(), "acme/private")
	if err != nil {
		t.Fatalf("remoteHead: %v", err)
	}
	if head == "" || branch != "main" {
		t.Fatalf("head=%q branch=%q", head, branch)
	}
	log := readCallLog(t, logPath)
	if n := countCalls(log); n != 2 {
		t.Errorf("git invoked %d times, want 2 (anon then authed)", n)
	}
	if strings.Contains(log, fakeToken) {
		t.Errorf("token leaked into argv/log:\n%s", log)
	}
}

// No token configured -> no retry, the original 404 is preserved.
func TestRemoteHeadNoTokenNoRetry(t *testing.T) {
	logPath := fakeGit(t, "lsremote-private")
	g := &gitSource{home: t.TempDir(), probe: time.Minute, tokens: &tokenPool{}}

	_, _, err := g.remoteHead(context.Background(), "acme/private")
	if !errors.Is(err, errNotFound) {
		t.Fatalf("err = %v, want errNotFound", err)
	}
	if n := countCalls(readCallLog(t, logPath)); n != 1 {
		t.Errorf("git invoked %d times, want 1 (no token, no retry)", n)
	}
}

// A network fault must not be misread as "private", and must not burn a token.
func TestRemoteHeadNetworkErrorNoAuthedRetry(t *testing.T) {
	logPath := fakeGit(t, "lsremote-dns")
	g := &gitSource{home: t.TempDir(), probe: time.Minute, tokens: &tokenPool{tokens: []string{fakeToken}}}

	_, _, err := g.remoteHead(context.Background(), "octocat/Hello-World")
	if err == nil || errors.Is(err, errNotFound) {
		t.Fatalf("err = %v, want a non-not-found failure", err)
	}
	if ae, ok := err.(*apiError); !ok || ae.code != "git_error" {
		t.Errorf("err = %#v, want git_error apiError", err)
	}
	if n := countCalls(readCallLog(t, logPath)); n != 1 {
		t.Errorf("git invoked %d times, want 1 (network fault, no authed retry)", n)
	}
}

// An anonymous rate limit (per-IP) can be lifted by a token's own quota, so
// the credentialed retry covers it too.
func TestRemoteHeadRateLimitRetriesAuthed(t *testing.T) {
	logPath := fakeGit(t, "lsremote-429")
	g := &gitSource{home: t.TempDir(), probe: time.Minute, tokens: &tokenPool{tokens: []string{fakeToken}}}

	head, _, err := g.remoteHead(context.Background(), "acme/private")
	if err != nil {
		t.Fatalf("remoteHead: %v", err)
	}
	if head == "" {
		t.Fatal("no head sha returned")
	}
	if n := countCalls(readCallLog(t, logPath)); n != 2 {
		t.Errorf("git invoked %d times, want 2 (anon 429 then authed)", n)
	}
}

// A stale token must not rewrite the error: when the anonymous attempt was
// rate-limited and the credentialed retry fails on the credential itself, the
// reported failure stays the rate limit, not a misleading 404.
func TestAnonymousErrorSurvivesFailedAuthedRetry(t *testing.T) {
	logPath := fakeGit(t, "lsremote-429-badtoken")
	g := &gitSource{home: t.TempDir(), probe: time.Minute, tokens: &tokenPool{tokens: []string{fakeToken}}}

	_, _, err := g.remoteHead(context.Background(), "acme/private")
	if !errors.Is(err, errRateLimit) {
		t.Fatalf("err = %v, want errRateLimit (anonymous error preserved)", err)
	}
	if n := countCalls(readCallLog(t, logPath)); n != 2 {
		t.Errorf("git invoked %d times, want 2 (anon then authed)", n)
	}
}

func TestRetryAuthedPredicate(t *testing.T) {
	withToken := &gitSource{tokens: &tokenPool{tokens: []string{fakeToken}}}
	noToken := &gitSource{tokens: &tokenPool{}}

	if !withToken.retryAuthed(errNotFound) || !withToken.retryAuthed(errRateLimit) {
		t.Error("not-found and rate-limit failures should retry with a configured token")
	}
	if withToken.retryAuthed(&apiError{status: 502, code: "git_error", msg: "boom"}) {
		t.Error("generic git errors must not trigger a credentialed retry")
	}
	if noToken.retryAuthed(errNotFound) || noToken.retryAuthed(errRateLimit) {
		t.Error("no token configured means no retry")
	}
}

// The ls-remote probe is bounded by its own timeout and maps to a 504 code.
func TestProbeTimeoutBounded(t *testing.T) {
	fakeGit(t, "lsremote-slow")
	g := &gitSource{home: t.TempDir(), probe: 150 * time.Millisecond, tokens: &tokenPool{}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := g.remoteHead(ctx, "a/b")
	elapsed := time.Since(start)
	if err != errCloneTimeout {
		t.Fatalf("err = %v, want errCloneTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("probe took %v; probe timeout not applied", elapsed)
	}
}

// A clone retry must wipe the anonymous attempt's partial tmp dir first.
func TestEnsureMirrorCloneRetryClearsTmp(t *testing.T) {
	logPath := fakeGit(t, "clone-private")
	base := t.TempDir()
	g := &gitSource{
		dir:      base,
		home:     t.TempDir(),
		timeout:  time.Minute,
		maxBytes: 1 << 30,
		gate:     newMirrorGate(),
		tokens:   &tokenPool{tokens: []string{fakeToken}},
	}
	dir := filepath.Join(base, "acme__private-deadbeef.git")
	if err := g.ensureMirror(context.Background(), "acme/private", dir); err != nil {
		t.Fatalf("ensureMirror: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		t.Fatalf("mirror HEAD missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "PARTIAL-FROM-ANON")); err == nil {
		t.Error("anonymous attempt's partial file survived into the installed mirror")
	}
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover tmp dir %s", e.Name())
		}
	}
	if n := countCalls(readCallLog(t, logPath)); n != 2 {
		t.Errorf("git invoked %d times, want 2 (anon clone then authed clone)", n)
	}
}

// The five-second poll can miss a fast clone that already exceeded its cap.
// A completed clone must be checked before its temp directory is installed.
func TestEnsureMirrorRejectsFastOversizedClone(t *testing.T) {
	fakeGit(t, "clone-big-fast")
	base := t.TempDir()
	g := &gitSource{dir: base, home: t.TempDir(), maxBytes: 1024, tokens: &tokenPool{}}
	dir := filepath.Join(base, "oversized.git")
	if err := g.ensureMirror(context.Background(), "a/b", dir); !errors.Is(err, errRepoTooLarge) {
		t.Fatalf("ensureMirror err = %v, want repo_too_large", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("oversized clone was installed: %v", err)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 0 {
		t.Errorf("oversized clone left temp directories: %v", entries)
	}
}

// A git log that emits some commits and then fails must never create a
// partial-but-successful contract that is subsequently cached for 24 hours.
func TestHistoryRejectsPartialGitLogFailure(t *testing.T) {
	fakeGit(t, "log-partial-failure")
	g := &gitSource{home: t.TempDir()}
	commits, _, err := g.history(context.Background(), t.TempDir(), "aaaa", 2, nil)
	if err == nil {
		t.Fatalf("history returned %d commits despite git log exiting 128", len(commits))
	}
	if commits != nil {
		t.Fatalf("history returned partial commits on error: %v", commits)
	}
}

// Two squash-merged PRs can share a title; they are distinct PRs and must not
// collapse into one candidate during dedupe.
func TestCountPRCommitsDedupesByNumber(t *testing.T) {
	fakeGit(t, "revlist")
	g := &gitSource{home: t.TempDir()}
	prs := []prMerge{
		{Number: 1, Ref: "same title", merged: true, sha: "aaaa"},
		{Number: 2, Ref: "same title", merged: true, sha: "bbbb"},
	}
	out := g.countPRCommits(context.Background(), t.TempDir(), prs)
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2 (same-title PRs are distinct)", len(out))
	}
	for i, p := range out {
		if p.Commits != 3 {
			t.Errorf("out[%d].Commits = %d, want 3", i, p.Commits)
		}
	}
}

// A panic inside the shared build must come back as an error, not crash the
// process: singleflight re-raises panics in a fresh goroutine, outside
// net/http's per-connection recover.
func TestGuardedSnapshotRecoversPanic(t *testing.T) {
	snap, err := guardedSnapshot("owner/repo", func() (*repoSnapshot, error) {
		panic("boom")
	})
	if snap != nil {
		t.Errorf("snapshot = %v, want nil", snap)
	}
	ae, ok := err.(*apiError)
	if !ok || ae.status != 500 || ae.code != "internal" {
		t.Errorf("err = %#v, want 500 internal apiError", err)
	}
}

// snapshot must route the shared build through the panic guard: a panic that
// escapes the singleflight function is re-raised in a fresh goroutine, outside
// any per-request recover, and takes the process down.
func TestSnapshotViaRecoversPanic(t *testing.T) {
	g := &gitSource{}
	_, err := g.snapshotVia(context.Background(), "acme/panics", "abc1234", "main",
		func(context.Context, string, string, string) (*repoSnapshot, error) {
			panic("injected panic")
		})
	ae, ok := err.(*apiError)
	if !ok || ae.status != 500 || ae.code != "internal" {
		t.Fatalf("err = %#v, want 500 internal apiError", err)
	}
}

// buildSnapshot must hold its mirror's gate across ensureMirror AND parse -
// the other gate tests exercise the primitive, this pins the wiring. If the
// acquire/release pair is ever dropped, the sweep can evict a live mirror.
func TestBuildSnapshotHoldsMirrorGate(t *testing.T) {
	fakeGit(t, "clone-parse-slow")
	base := t.TempDir()
	g := &gitSource{
		dir: base, home: t.TempDir(), timeout: time.Minute,
		maxBytes: 1 << 30, gate: newMirrorGate(),
		tokens: &tokenPool{}, sem: make(chan struct{}, 1),
	}
	dir, err := g.mirrorPath("acme/uses-gate")
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		snap *repoSnapshot
		err  error
	}
	done := make(chan result, 1)
	go func() {
		snap, err := g.buildSnapshot(context.Background(), "acme/uses-gate", "abc1234", "main")
		done <- result{snap, err}
	}()

	// Clone phase: the tmp dir exists while the fake git sleeps inside clone.
	waitFor(t, func() bool {
		entries, _ := os.ReadDir(base)
		prefix := filepath.Base(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), prefix) {
				return true
			}
		}
		return false
	}, "clone tmp dir")
	if g.gate.beginDelete(dir) {
		t.Error("gate not held while ensureMirror/clone runs")
		g.gate.endDelete(dir)
	}

	// Parse phase: the mirror is installed while rev-list sleeps.
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "HEAD"))
		return err == nil
	}, "installed mirror")
	if g.gate.beginDelete(dir) {
		t.Error("gate not held while the parse runs")
		g.gate.endDelete(dir)
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("buildSnapshot: %v", res.err)
	}
	if res.snap == nil || res.snap.Total != 2 {
		t.Fatalf("snapshot = %+v, want Total 2", res.snap)
	}
	if !g.gate.beginDelete(dir) {
		t.Fatal("gate still held after the build returned")
	}
	g.gate.endDelete(dir)
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// An abandoned .tmp clone dir (process killed mid-clone) is dropped once it is
// older than the clone timeout, and a fresh one is left alone.
func TestSweepDropsAbandonedTmpAfterTimeout(t *testing.T) {
	dir := t.TempDir()
	g := testSource(dir, time.Minute, 24*time.Hour, 1<<62)

	stale := mkMirror(t, dir, "a.git.tmpold", 2*time.Minute, 0)
	fresh := mkMirror(t, dir, "b.git.tmpnew", 5*time.Second, 0)
	g.sweep()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale tmp dir survived: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh tmp dir was removed: %v", err)
	}
}

// A repo name can contain ".git.tmp"; a valid mirror directory (which always
// ends in .git) must not be mistaken for an abandoned clone.
func TestSweepKeepsMirrorNamedWithDotGitTmp(t *testing.T) {
	dir := t.TempDir()
	g := testSource(dir, time.Minute, 24*time.Hour, 1<<62)

	valid := mkMirror(t, dir, "owner__x.git.tmp-abc123.git", 2*time.Hour, 10)
	abandoned := mkMirror(t, dir, "owner__y.git.tmpdead", 2*time.Hour, 0)
	g.sweep()

	if _, err := os.Stat(valid); err != nil {
		t.Errorf("valid mirror was treated as an abandoned tmp dir: %v", err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("abandoned tmp dir survived: %v", err)
	}
}

// A build holding its mirror's gate is never evicted, even past the TTL, and
// the same mirror is evicted once the build releases it.
func TestSweepSparesMirrorHeldByBuild(t *testing.T) {
	dir := t.TempDir()
	g := testSource(dir, time.Minute, time.Hour, 1<<62)

	held := mkMirror(t, dir, "held.git", 2*time.Hour, 10) // past TTL and idle window
	g.gate.acquire(held)
	g.sweep()
	if _, err := os.Stat(held); err != nil {
		t.Errorf("mirror held by a build was evicted: %v", err)
	}

	g.gate.release(held)
	g.sweep()
	if _, err := os.Stat(held); !os.IsNotExist(err) {
		t.Errorf("released mirror past its TTL survived: %v", err)
	}
	// The sweep must clear its deleting mark even after removing the dir:
	// the next build for that path has to be able to acquire it.
	if !g.gate.beginDelete(held) {
		t.Error("mirror stayed marked deleting after the sweep")
	} else {
		g.gate.endDelete(held)
	}
}

// Disk-pressure eviction must also refuse to touch a mirror a build holds.
func TestSweepSparesHeldMirrorOverDiskCap(t *testing.T) {
	dir := t.TempDir()
	g := testSource(dir, time.Minute, 24*time.Hour, 1) // any mirror is over budget

	held := mkMirror(t, dir, "lru.git", 10*time.Minute, 10)
	g.gate.acquire(held)
	g.sweep()
	if _, err := os.Stat(held); err != nil {
		t.Errorf("held mirror was evicted under disk pressure: %v", err)
	}

	g.gate.release(held)
	g.sweep()
	if _, err := os.Stat(held); !os.IsNotExist(err) {
		t.Errorf("released over-budget mirror survived eviction: %v", err)
	}
	// The LRU branch must clear its deleting mark too.
	if !g.gate.beginDelete(held) {
		t.Error("mirror stayed marked deleting after disk-pressure eviction")
	} else {
		g.gate.endDelete(held)
	}
}

// A mirror untouched past the TTL is removed even when the disk budget is fine;
// a mirror idle only past the timeout window (possibly in use) is not.
func TestSweepRemovesMirrorPastTTL(t *testing.T) {
	dir := t.TempDir()
	g := testSource(dir, time.Minute, time.Hour, 1<<62)

	dead := mkMirror(t, dir, "dead.git", 2*time.Hour, 10)
	busy := mkMirror(t, dir, "busy.git", 30*time.Second, 10)
	fresh := mkMirror(t, dir, "fresh.git", time.Second, 10)
	g.sweep()

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("past-TTL mirror survived: %v", err)
	}
	for _, p := range []string{busy, fresh} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("live mirror %s was removed: %v", p, err)
		}
	}
}

// Over the disk budget, the least-recently-used mirror that is provably idle
// (past the timeout window, inside the TTL) is evicted first.
func TestSweepEvictsLRUOverDiskCap(t *testing.T) {
	dir := t.TempDir()
	g := testSource(dir, time.Minute, 24*time.Hour, 250)

	lru := mkMirror(t, dir, "lru.git", 10*time.Minute, 100)
	mru := mkMirror(t, dir, "mru.git", 2*time.Minute, 100)
	fresh := mkMirror(t, dir, "fresh.git", time.Second, 100)
	g.sweep()

	if _, err := os.Stat(lru); !os.IsNotExist(err) {
		t.Errorf("LRU mirror survived eviction: %v", err)
	}
	if _, err := os.Stat(mru); err != nil {
		t.Errorf("MRU idle mirror should survive the first eviction: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh (possibly in-use) mirror was evicted: %v", err)
	}
}

// A long squash-merge title is shortened for the legend. Cutting at a byte
// offset can split a multi-byte character, which json.Marshal then turns into
// U+FFFD on the star map.
func TestParsePRSquashTitleTruncatesOnRuneBoundary(t *testing.T) {
	subject := strings.Repeat("界", 40) + " (#7)"
	pr, ok := parsePR(subject, commitRec{SHA: "abc"})
	if !ok {
		t.Fatal("squash subject not recognised")
	}
	if !utf8.ValidString(pr.Ref) || strings.ContainsRune(pr.Ref, utf8.RuneError) {
		t.Fatalf("truncated title is not valid UTF-8: %q", pr.Ref)
	}
	if !strings.HasSuffix(pr.Ref, "…") {
		t.Errorf("long title not marked as truncated: %q", pr.Ref)
	}
}

// A repository over CLONE_MAX_MB fails the same way on every visit. Without a
// memo each visit re-downloads up to the cap before failing again.
func TestBuildSnapshotMemoizesRepoTooLarge(t *testing.T) {
	log := fakeGit(t, "clone-big-fast")
	g := &gitSource{dir: t.TempDir(), home: t.TempDir(), maxBytes: 1024,
		tokens: &tokenPool{}, sem: make(chan struct{}, 1), gate: newMirrorGate(),
		timeout: time.Minute}
	for i := 0; i < 3; i++ {
		if _, err := g.buildSnapshot(context.Background(), "a/b", "head", "main"); !errors.Is(err, errRepoTooLarge) {
			t.Fatalf("attempt %d: err = %v, want repo_too_large", i, err)
		}
	}
	if n := strings.Count(readCallLog(t, log), "ARGS:"); n != 1 {
		t.Fatalf("git invoked %d times for an oversized repo, want 1 (later visits served from the memo)", n)
	}
}

// A missing repo is probed once per notFoundTTL, not on every request: typo'd
// and crawled names otherwise turn straight into GitHub traffic.
func TestRemoteHeadMemoizesNotFound(t *testing.T) {
	logPath := fakeGit(t, "lsremote-private") // anonymous: "Repository not found"
	g := &gitSource{home: t.TempDir(), probe: time.Minute, headTTL: 30 * time.Second, tokens: &tokenPool{}}
	for i := 0; i < 3; i++ {
		if _, _, err := g.remoteHead(context.Background(), "acme/missing"); !errors.Is(err, errNotFound) {
			t.Fatalf("attempt %d: err = %v, want not_found", i, err)
		}
	}
	if n := countCalls(readCallLog(t, logPath)); n != 1 {
		t.Fatalf("git probed a missing repo %d times, want 1", n)
	}
}
