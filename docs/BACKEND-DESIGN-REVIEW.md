# Backend design review - Stellar (star.md)

Date: 2026-10-17. Scope: `apps/server` (Go), checked against the agreed backend
direction in `AGENTS.md` and the locked contract in `packages/contract`.

Method: full code read, `go build` / `go vet` / `go test -race`, live
end-to-end runs against real repos (octocat/Hello-World, kelseyhightower/nocode,
sindresorhus/slugify, vercel/next.js), then two independent passes:

1. an **adversarial code review** of the fixes (separate reviewer, mutation
   testing in a scratch copy, fake-git shim experiments), and
2. a **design research pass** over Part 2 recommendations against current
   authoritative sources (Go docs, Kubernetes, SQLite, GitHub, Prometheus,
   Google SRE), with runtime verification of the tricky Go semantics.

Every claim below has a test, a recorded run, or a cited source behind it.
All changes are in the working tree (uncommitted; nothing pushed).

---

## Part 1 - Bugs found and fixed

26 tests across `transform_test.go`, `cache_test.go`, `gitsource_test.go`,
`main_test.go`; the fixes are proven by 16 mutation checks (revert the fix ->
the covering test fails; see Appendix A). Independent review verdict: all
seven fixes VALID; 1.5 was software-hardened further and 1.6 was caveated
(see 1.8). A delta re-review of the hardened tree returned "ready to commit:
yes", conditional only on three test-coverage asks - all closed (1.8.11).

### 1.1 Genesis star ignited last on single-commit repos (contract violation)

- Symptom: for a repo with one commit, that node is both genesis and HEAD.
  `buildMain` set `Type: "genesis", BirthT: 0`, then the HEAD-pin overwrote
  `BirthT` to `1` on the same node. The locked renderer ignites genesis first,
  so the reveal order broke against the contract ("genesis birthT 0").
- Root cause: the final `out[len(out)-1].BirthT = 1` did not account for
  `len(out) == 1`, where the last node IS the genesis.
- Fix: pin HEAD to 1 only when `len(out) > 1` (`transform.go`).
- Verification: `TestBuildMainSingleCommitGenesisStaysZero` failed before the
  fix (`genesis birthT = 1, want 0`), passes after; the multi-commit pin is
  covered by `TestBuildMainHeadPinnedToOne`, rewritten to equal-timestamp
  commits so it now actually exercises the pin (the original version passed
  with the pin removed - review finding, fixed).

### 1.2 One bad token broke every public repo (operational outage vector)

- Symptom (reproduced): with `GITHUB_TOKEN=bogus-token-12345`, requesting
  `octocat/Hello-World` - a public repo, already cached on that host - returned
  `404 {"error":"not_found"}`. The token was force-injected into every git
  network command (`http.extraHeader`), so GitHub rejected even the anonymous-
  capable public probe.
- Impact: any operator with a stale, revoked, or mistyped token loses public
  charting entirely, with a misleading "repository not found".
- Root cause: `gitEnv()` always attached credentials, conflating "credentials
  available" with "credentials needed".
- Fix: network commands (`ls-remote`, `clone`, `fetch`) now run **anonymous
  first**; the credentialed attempt happens only when a token exists and the
  anonymous attempt returned `errNotFound` (exactly how a private repo presents
  itself) or `errRateLimit` (anonymous quota is per-IP; a token has its own).
  This matches the agreed direction: the token is spent only where it is the
  only option. The rate-limit case, its curl phrasing ("returned error: 429"),
  and the DNS negative case are all covered by tests.
- Verification: the bogus-token server now returns HTTP 200 with a full
  contract for octocat/Hello-World (was 404); anonymous-first ordering, the
  authed retry, the no-token path, and the no-retry-on-network-error path are
  covered by a fake-git shim (`TestRemoteHead*`). The token never appears in
  argv or logs (asserted).
- Note: `git hist` shows this exact failure mode half-fixed once before in a
  scratch copy (a dead `retryAuthed` stub returning false); it is real here.

### 1.3 `branches: null` instead of `[]` crashed the renderer

- Symptom: for a repo with no PRs and no live branches,
  `kelseyhightower/nocode` returned `"branches": null`. The Go nil slice
  marshals as `null`, but the contract (`packages/contract`) declares
  `branches: BranchNode[]`, and the locked renderer does
  `BRANCH_DEFS.forEach(...)` (renderer.ts:160,257) - `null.forEach` throws, so
  the whole map fails to render for such repos.
- Fix: `buildBranches` now returns a non-nil slice; empty renders as `[]`.
  Since this changes the emitted contract shape, `paramsVersion` was bumped
  `v1 -> v2`, which invalidates every existing cached blob by design (the cache
  key includes the version). Post-review, the version suffix is now derived
  from the sampling knobs (`fmt.Sprintf("v2-s%d-b%d", maxStars, branchCap)`),
  so moving a knob can never silently serve contracts the old knobs shaped.
- Verification: `TestContractBranchesSerializesAsArray` failed before
  (`marshaled as null`), passes after; live run on nocode shows
  `branches: []` and the new cache key.

### 1.4 Contract cache grew without bound; the `ttl` argument was ignored

- Symptom: nothing ever deleted rows. `Get` treated rows older than 24h as
  stale but kept them; every `repo@sha` contract ever served accumulated in
  the SQLite file forever.
- Root cause: `Put` stored `fetched_at` and never pruned; `ttl` parameter was
  accepted and dropped on the floor.
- Fix: the column is now a real expiry (`expires_at = now + ttl`), with a
  best-effort migration renaming the old column (rows from before the change
  read as already expired; the cache rewarms itself). Every `Put` prunes
  expired rows; the same prune also runs at startup; an index on the column
  keeps the deletes a range operation. Post-review: every SQL call is bounded
  by a 2s context (`QueryRowContext`/`ExecContext`), and the prune boundary
  (`<=`) now matches `Get`'s "expired at this second is not live" rule.
- Verification: `TestSQLiteCacheHonorsTTLAndPrunes` (expired entry not served;
  pruned), `TestSQLiteCacheMigratesLegacyTable` (old-schema file still works;
  legacy row pruned at startup).

### 1.5 Mirror eviction could delete a mirror mid-fetch; `.tmp` dirs leaked

- Symptom (race): `sweep()` treats a mirror's mtime as an in-use lease, but
  the mtime was only stamped *after* `git fetch` returned. A fetch that starts
  on an idle mirror keeps the stale mtime for its whole duration, so under
  disk pressure the sweep could evict a mirror while a fetch was writing into
  it. Separately, `.tmp` clone dirs from a killed process were never cleaned
  (sweep only looked at `*.git`), leaking disk forever.
- First fix (mtime lease): touch before the network read, re-read the mtime
  immediately before any delete (`stillIdle`).
- Independent review verdict: PARTIAL. Stat-then-RemoveAll is not atomic
  against a build that leases the mirror in between, and two builds of the
  same repo at different HEADs share one mirror - one can `fetch` (or
  `RemoveAll` after a failed fetch) while the other parses.
- Final fix: the **mirror gate** (`mirrorGate`, `gitsource.go`). Every build
  holds its mirror's gate from before `ensureMirror` until the parse is done;
  the sweep may delete a directory only after marking it as deleting while no
  build holds it (`beginDelete`, which returns false when a build is inside,
  `endDelete` after the removal). A build arriving mid-delete waits and then
  reclones. This closes the eviction race and serializes same-repo builders,
  so a fetch and a parsing build can never race on one directory.
- Sweep also drops `.git.tmp*` dirs older than `CLONE_TIMEOUT`, and the
  classification now requires "contains `.git.tmp` **and** does not end in
  `.git`" - a valid mirror for a repo whose name contains `.git.tmp` is no
  longer deleted as abandoned (review finding m-b).
- Verification: `TestSweepSparesMirrorHeldByBuild` (gate held -> survives past
  TTL; released -> evicted), `TestSweepSparesHeldMirrorOverDiskCap` (same under
  disk pressure), `TestSweepKeepsMirrorNamedWithDotGitTmp`,
  `TestSweepDropsAbandonedTmpAfterTimeout`, plus the TTL/LRU tests.

### 1.6 `STATIC_DIR=.` served `.git/` and `.env` over HTTP

- Symptom (reproduced): following the README's own run instructions from the
  repo root, `GET /.git/config` returned the repository config (including the
  origin URL); any `.env` beside it would be served too.
- Fix: `denyDotfiles` wraps the static file server and 404s any path with a
  dot-prefixed segment (dot inside a segment, e.g. `release.v2.txt`, is still
  served).
- Review finding: symlinked targets are not resolved, so a `link -> .git`
  symlink inside `STATIC_DIR` still exposes the contents (`/link/config` ->
  200). Latent (the default tree has no such links) but real; documented in
  the README and the `denyDotfiles` comment instead of complicating the
  middleware. Encoded/normalized bypass battery (`.%2Eenv`, `/a/%2e%2e/.env`,
  `//.env`, ...) all 404 or redirect - verified by the reviewer.
- Verification: `TestDenyDotfiles` (incl. nested `/foo/.hidden`, `/..env`);
  live run shows `/.git/config -> 404`, `/.env -> 404`, `/ -> 200`.

### 1.7 The freshness probe had no deadline; half-open connections held requests

- Symptom: `git ls-remote` runs on every request (cache hit or miss) under the
  request context only. git's HTTP transport has no low-speed abort, so a
  stalled socket can pin a handler for the kernel retransmit budget or until
  the client gives up. Measured 50-112s per probe on a throttled network here;
  a blackholed connection can be far worse.
- Fix: `remoteHead` bounds the probe with `CLONE_PROBE_TIMEOUT` (default 3m).
  The HTTP server also sets `ReadHeaderTimeout: 10s`, `ReadTimeout: 30s`,
  `IdleTimeout: 2m`; `WriteTimeout` stays unset because cold builds and the
  NDJSON stream legitimately write for minutes (review agreed; it noted the
  earlier comment had conflated read and write budgets).
- Verification: with `CLONE_PROBE_TIMEOUT=2s`, a repo whose probe needs longer
  returns `504 clone_timeout` in 2.012s instead of hanging;
  `TestProbeTimeoutBounded` covers it without network.

### 1.8 Post-review hardening (found by the independent passes)

Each item below was found by the review or the research pass, not by me, and
is fixed and tested in the same working tree.

1. **Panic containment in the shared build.** A panic inside the
   singleflight function is re-raised by `singleflight` in a fresh goroutine
   when callers are attached, where net/http's per-connection recover cannot
   catch it - one bad input could kill the process. `guardedSnapshot` now
   converts it to a `500 internal` apiError.
   (`TestGuardedSnapshotRecoversPanic`)
2. **Post-kill pipe drain bounded.** `cmd.WaitDelay = 10s`: `Wait` reads
   stdout until EOF, which a subprocess that escaped the process group could
   hold open past the context deadline.
3. **Anonymous rate limit retries authed** (see 1.2) **and curl's
   "returned error: 429"** phrasing now maps to `rate_limited`.
   (`TestRemoteHeadRateLimitRetriesAuthed`, `TestRetryAuthedPredicate`)
4. **Cache calls bounded and pruned at startup** (see 1.4) - a stalled disk
   surfaces as a miss, not a pinned handler.
5. **`paramsVersion` derived from the sampling knobs** (see 1.3).
6. **Merged-PR detail calls are now serial.** The old fan-out ran up to 8
   concurrent REST calls; GitHub's own best-practices guide says secondary
   rate limits are avoided by serial requests. Worst case ~9 sequential calls
   on a cold build is noise next to the clone.
7. **PR dedupe by number, not title.** Two squash-merged PRs with the same
   title no longer collapse into one asterism (`prKey`, used by
   `countPRCommits` and `buildBranches`).
   (`TestCountPRCommitsDedupesByNumber`,
   `TestBuildBranchesDedupesByPRNumberNotTitle`)
8. **Docs match behavior.** README: dotfile wording softened to the actual
   guarantee (URL paths rejected, symlinks not resolved), auth note updated
   for the rate-limit retry, and a new ops rule: the SQLite file and mirror
   directory are single-instance local state - never attach them to more than
   one replica (SQLite WAL is not safe on network filesystems).
9. **Anonymous errors survive a failed credentialed retry.** If the anonymous
   attempt was rate-limited and the authed retry fails on the credential
   itself, the reported error stays the rate limit - a stale token can no
   longer rewrite a 503 into a misleading 404.
   (`TestAnonymousErrorSurvivesFailedAuthedRetry`)
10. **The merged-PR REST fallback phase is bounded** by a 60s timeout around
    the up-to-9 serial calls, so a stalled API cannot extend a build by the
    sum of nine worst cases. (Part 2.7)
11. **Tests pin the mechanisms, not just the symptoms** (re-review asks):
    `TestBuildSnapshotHoldsMirrorGate` drives a real `buildSnapshot` (fake
    git) and probes the gate during both clone and parse;
    `TestSnapshotViaRecoversPanic` pins the panic guard at the singleflight
    call site via an injectable `snapshotVia` seam; the gate tests assert the
    sweep clears its deleting mark after eviction (`endDelete` pairing). All
    three survive their mutations only if the mechanism is present.

---

## Part 2 - System design issues (researched, ranked)

Structural issues, each with impact, a validated recipe, and sources. The
recipes below were checked against current authoritative documentation and,
where the semantics were subtle (shutdown, write deadlines), verified with
throwaway programs on go1.26.5. Severity: **S1** = user-visible under load or
outage, **S2** = operational risk as usage grows, **S3** = polish/evolution.

### 2.1 (S1) Cold-build admission is unbounded: the queue can grow forever

`buildSnapshot` acquires the (default 4)-slot semaphore with a detached,
deadline-less context (`context.WithoutCancel`), and `CLONE_TIMEOUT` starts
only *after* the slot is acquired - so the `<-ctx.Done()` arm of the select
can never fire. Concurrent cold builds beyond 4 wait unboundedly (singleflight
only coalesces the same `repo@sha`); each waiter is a heap-blocked goroutine.

Validated fix - separate the **queue wait** from the **work budget**: acquire
with a queue timeout (e.g. `CLONE_QUEUE_TIMEOUT=45s`, 0 = unlimited), reject
with 503 + `Retry-After` (`busy`) on expiry, keep `CLONE_TIMEOUT` starting
after acquisition. `golang.org/x/sync/semaphore.Weighted.Acquire(ctx, 1)`
(already an x/sync dependency) expresses this directly. The stream path has
already committed 200, so `busy` must travel as the in-band `error` frame -
which is why 2.10 (freeze the error vocabulary) comes first, and the web
client's `humanError()` map needs the new code in the same PR. Pick the budget
from the queue-wait histogram (2.4), not by taste. This is textbook load
shedding: queue what a queue can absorb, fail the rest fast and retryably.

Sources: Google SRE "Handling Overload" (sre.google/sre-book/handling-overload/);
pkg.go.dev/golang.org/x/sync/semaphore.

### 2.2 (S2) Cache freshness is keyed to the default-branch HEAD sha only

`repo@headSha` is the cache key, so tag additions/edits/deletions and
non-default branch activity do not invalidate a cached contract (up to 24h
staleness for those signals - documented). Every request also pays the
ls-remote probe, un-coalesced; `remoteHead` runs per request, cache hit or
miss.

Validated: keep the cache key as-is for now (release stars converging within
a day is reasonable), but the **probe memo is now P1** - it is ~30 lines, cuts
every hit's cost, and reduces pressure on GitHub's rate-limited anonymous git
transport. Design: a TTL map (5-15s) plus the existing singleflight to
coalesce in-flight probes; **bound the map** (e.g. 1024 entries) or it becomes
its own crawler vector. Memoize 404s for a shorter negative TTL (5-30s) -
GitHub's guidance explicitly says not to re-request missing resources on every
hit. Note what singleflight is not: it dedupes in-flight calls only and drops
results once delivered (per its docs), so the TTL cache is yours. The
refs-digest key remains a Phase-2 option: any branch push would then invalidate
the contract for all viewers of an active repo.

Sources: pkg.go.dev/golang.org/x/sync/singleflight;
docs.github.com "Best practices for using the REST API".

### 2.3 (S2) Slow readers can pin stream handlers

`emit` writes NDJSON with no write deadline. A client that stops reading
applies TCP backpressure; `Encode` blocks, `keepalive.stop()` waits on the
emitter, and the handler (plus its singleflight waiter role) is held until the
OS gives up.

Validated fix: `http.ResponseController` per-frame write deadline, and on the
first write error set a `dead` flag, skip further writes, and **cancel a
request-scoped context** so the snapshot waiter returns immediately instead of
waiting out the detached build. Runtime-verified on go1.26.5: a socket-blocked
`Write` unblocks at exactly the deadline with `os.ErrDeadlineExceeded`. Treat
`SetWriteDeadline` returning `http.ErrNotSupported` (e.g. some HTTP/2 cases)
as best-effort - the error-cancel path is the cross-protocol guarantee. Keep
the `Flush` per frame and the 5s keepalive (clears typical proxy idle
timeouts; nginx default `proxy_read_timeout` is 60s). No server-wide
`WriteTimeout` - per-frame deadlines are the right granularity for minute-long
cold builds.

Sources: go.dev/doc/go1.20#http; pkg.go.dev/net/http#ResponseController;
Cloudflare "The complete guide to Go net/http timeouts".

### 2.4 (S2) No observability beyond log lines

No metrics (cache hit ratio, clone/probe duration, queue depth, sweep
evictions, 5xx by code) and `/healthz` is a static "ok" that cannot see a
missing `git`, an unwritable mirror dir, or a filling disk (all logged once at
startup, invisible to orchestrators).

Validated, and this is **P1 first** because 2.1 and 2.5 are untunable without
it. Two honest options respecting the stdlib-first constraint: `expvar`
(zero dependencies, `/debug/vars`), or `prometheus/client_golang` +
`promhttp` if a Prometheus exists. Metric sketch: cache hits/misses; probe,
queue-wait, clone+parse seconds; in-flight builds and semaphore-in-use gauges;
4xx/5xx by code; sweep evictions; disk used/free; singleflight shared/unshared.
Follow Prometheus naming (`_seconds`, `_bytes`, `_total`, snake_case); label by
phase/code/outcome, **never** by repo (unbounded cardinality). A `/readyz`
should check: `git` resolvable (once at startup via `exec.LookPath`), mirror
dir writable, cache reachable (`SELECT 1` with a timeout), disk free above a
headroom floor (see 2.11), and a draining flag (2.5). Keep `/healthz` as pure
liveness; do **not** gate readiness on GitHub reachability - an upstream
outage is not per-instance health, and flapping replicas out of rotation helps
nobody.

Sources: pkg.go.dev/expvar; pkg.go.dev/github.com/prometheus/client_golang/prometheus/promhttp;
prometheus.io/docs/practices/naming/; Kubernetes probe docs.

### 2.5 (S2) No graceful shutdown

`ListenAndServe` + `log.Fatal`; SIGTERM kills the process mid-clone and
mid-stream. A rolling restart drops every in-flight stream silently.

Validated recipe (semantics runtime-checked on go1.26.5):
`Server.Shutdown` does **not** cancel in-flight request contexts and waits for
handlers to return - so long-lived streams must be canceled explicitly.
Concretely: `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`;
`BaseContext` returning an app context; `srv.RegisterOnShutdown(appCancel)`
(documentedly the hook for hijacked/long-lived handlers - it should *start*
drain, not wait); on signal: flip `/readyz` to failing, sleep ~2-5s so an LB
de-routes first (Kubernetes removes endpoints concurrently with TERM delivery;
Docker's default stop timeout is 10s, K8s
`terminationGracePeriodSeconds` is 30s), then `srv.Shutdown(ctx)` with a
budget ≤ the platform grace. Detached cold clones keep running by design;
exit after the budget - the `.tmp` sweep cleans up on next boot. Optionally
track in-flight detached builds in a WaitGroup and give them 5-10s too.

Sources: pkg.go.dev/net/http#Server.Shutdown and #Server.RegisterOnShutdown;
pkg.go.dev/os/signal#NotifyContext; Kubernetes pod-termination docs; Docker
`stop` reference.

### 2.6 (S3) Abuse and fairness

Anonymous callers can request unbounded distinct repos; each cold request
costs a clone. Bounded individually, not aggregated per client; a crawler can
monopolize the clone slots.

Validated fix: `golang.org/x/time/rate` per client (`Allow()` for drop-style
enforcement; safe for concurrent use), keyed by `net.SplitHostPort(r.RemoteAddr)`.
Honor `X-Forwarded-For` **only** behind a configured trusted proxy
(`TRUST_PROXY=1`) and take the right-most untrusted hop - XFF is spoofable
otherwise. Tier limits: cold builds strict (e.g. 6/min burst 2), cache
hits/static cheap (e.g. 60/min). Distinguish 429 (abuse) from 503 +
`Retry-After` (overload); stream rejections go in-band. Evict idle limiter
entries (unbounded per-IP state is its own memory DoS). Edge rate limiting
(CDN/nginx) stays the first line; in-app is the backstop for the clone budget.
The SSRF posture is already right: host pinned to github.com, repo regex-
constrained.

Sources: pkg.go.dev/golang.org/x/time/rate; MDN X-Forwarded-For; SRE
"Addressing Cascading Failures".

### 2.7 (S3) REST fallback policy is all-or-nothing

The merged-PR fallback runs only when *no* git-recovered PR carries a real
commit count (`hasSizedPR`); mixed squash/merge repos keep "squashed" labels
even with a token. The list is sorted by `updated`, so old merged PRs can fall
outside the 100-item window.

Updated in this pass: the per-PR detail calls are now **serial** (GitHub's
guidance: concurrent requests are how you trip secondary rate limits), and the
whole fallback phase is bounded by a 60s context so a stalled API cannot
extend a build by the sum of nine worst cases. Open items: honor
`Retry-After` / `X-RateLimit-Reset` when mapping 403/429 so the
UI can say "retry in N minutes" instead of "retry later"; gate any policy
change on "any unsized PR" behind an explicit mode (`PULLS_REST=auto|always|never`)
- flipping the default would spend REST calls on every squash-happy public
repo and break the "public repos cost zero REST" property; document the
`sort=updated` 100-window in the contract docs.

Sources: docs.github.com "Best practices for using the REST API" and
"Rate limits for the REST API".

### 2.8 (S3) Contract fidelity details

- **Fixed in this pass:** squash-merge PRs are no longer deduped by title -
  dedupe keys on the PR number when known (`prKey`), so two same-title PRs
  stay two asterisms.
- `short(sha)` truncation to 7 chars could theoretically collide in the tag
  map on huge repos (negligible; GitHub's own convention; noted for
  completeness).
- Repo names are case-sensitive in cache keys and mirror names while GitHub is
  case-insensitive, so `Owner/Repo` and `owner/repo` are two entries (harmless
  duplication; if it ever matters, lowercase the cache key/mirror input and
  keep the user's string for display).

### 2.9 (S3) Single-instance state by design

SQLite cache + on-disk mirrors are per container. The compose setup persists
both volumes; the 2-method `Cache` interface keeps a swap to Redis/Postgres a
one-file change.

Updated in this pass: the README now carries the hard ops rule - never attach
the SQLite file or the mirror volume to more than one replica (SQLite WAL is
not safe over network filesystems; two containers r/w the same file is a
corruption case, not a cache-miss case). Scaling path, in order: (1) repo-hash
sticky routing at the edge with per-instance warm mirrors (keeps singleflight
and mirror locality); (2) swap `Cache` for a shared store with a shorter
`CLONE_TTL` for mirrors; (3) object-storage mirror hydration only if needed.

Sources: sqlite.org/wal.html; sqlite.org/faq.html#q5.

### 2.10 (S3) Streaming protocol niceties

NDJSON frames carry no protocol version; the error `code` set is de facto
public API, and 2.1/2.3 would add to it (`busy`) or change stream semantics
(write-death).

Validated, and it should ship **before** 2.1/2.3 because both add protocol
surface: freeze the vocabulary in `packages/contract` - a
`STREAM_PROTOCOL_VERSION` constant plus a `StreamErrorCode` union
(`bad_repo | not_found | empty_repo | rate_limited | clone_timeout |
repo_too_large | git_unavailable | git_error | internal | busy`), with a Go
test asserting the emitted set so drift is loud. Add the version to the first
and terminal frames (the TS reader ignores unknown fields, so this is
backward-compatible). Document the conventions already relied on: HTTP 200
once the stream is committed, errors only in-band, `result.contract` is the
`Contract` verbatim. NDJSON has no formal spec; the de-facto one is
ndjson.org's "one JSON value per newline" - exactly what `json.Encoder`
emits. Staying on fetch+NDJSON rather than `EventSource` is right for this use
case (no reconnection/POST semantics needed).

Sources: github.com/ndjson/ndjson-spec; MDN Server-sent events; WHATWG SSE
spec.

### 2.11 (S2) No free-disk awareness

New (research pass): `CLONE_DISK_MB` is a policy cap, not a floor, and the
mirrors plus SQLite share one volume. On a smaller or filling volume the
failure mode is opaque: git writes fail mid-clone, tmp renames fail, SQLite
writes die behind best-effort. Fix: `syscall.Statfs` on the mirror dir at
startup and periodically; fail `/readyz` below a headroom floor (e.g.
`CLONE_MAX_MB` + 1 GiB); reject cold builds with 503 `busy` early; log free
space with the sweep. Sources: pkg.go.dev/syscall#Statfs; Kubernetes
eviction/disk-pressure docs.

---

## Part 3 - What is solid (keep it)

- Budget separation via blob-less bare clone is the right architecture: public
  repos cost zero REST calls regardless of size; the token is spent only where
  it is the only option (private clone, merged-PR fallback, anon rate-limit
  fallback).
- Constant-memory history parse (streamed `git log`, bounded retention) and
  force-included milestones; the scaling math matches the contract (verified:
  slugify = 78 stars, 16 releases, span 33.86 = span0 * sqrt(78/46)).
- Resource caps: per-repo size kill during transfer, disk budget with LRU+TTL,
  bounded clone concurrency, clone+parse timeout with process-group kill,
  singleflight per `repo@sha`, and (new) mirror-gate arbitration between
  builds and the sweep.
- Failure containment: best-effort cache never turns into an error page;
  panics in the shared build become errors; a failed anonymous attempt never
  burns or leaks credentials.
- Token hygiene: env-only (never argv or URL, never logged), API-shaped 404
  for private repos, hardened git env (no prompts, no hooks, https-only).
- The reviewer could not falsify any of the seven original fixes; every
  regression mutation targeted at the actual logic was caught by the tests.

## Part 4 - Suggested sequence

- **P0 (this pass, done):** the seven fixes + the 1.8 hardening, all tested
  (26 tests, 16 mutation checks).
- **P1 (next):** 2.4 metrics + `/readyz` first (foundation - its numbers
  choose 2.1's queue budget and 2.5's drain budget); then 2.5 graceful
  shutdown and 2.3 stream write deadline (same connection-lifecycle
  workstream; reuse the readiness flag); then 2.10 protocol freeze in the
  same PR as 2.1 admission (`busy` code lands in the frozen vocabulary and
  the frontend error map); pull 2.2's probe memo (with negative TTL) into P1;
  fold in 2.11 free-disk, plus `go test -race ./...` as a standing gate.
- **P2:** 2.6 per-IP rate limiting, 2.7's Retry-After surfacing + `PULLS_REST`
  mode, 2.8's remaining nits, 2.9 multi-instance decision, refs-digest cache
  key (only if README-embed freshness demands it).

Top 5 upgrades ranked by impact (research pass):

| # | Upgrade | Why it ranks here |
|---|---------|-------------------|
| 1 | Graceful drain + readiness (2.5 + 2.4) | User-visible on every deploy/restart; today a rolling restart silently kills in-flight streams |
| 2 | Metrics + `/readyz` (2.4) | Foundation: without queue-wait/clone/sweep numbers you cannot set 2.1's budget or alert on anything |
| 3 | Stream write deadline + cancel-on-write-failure (2.3) | ~15 lines for the worst unbounded resource hold (handler + conn + singleflight waiter) |
| 4 | Bounded admission wait -> 503 `busy` (2.1) | Converts an unbounded latency tail into an honest, retryable failure |
| 5 | Panic containment in the shared build (1.8.1) | One `defer recover` prevents an instance-wide crash net/http cannot intercept |

---

## Appendix A - Evidence log (2026-10-17)

- `go build ./...` clean, `go vet ./...` clean, `gofmt -l .` empty,
  `go test -race -count=1 ./...` passes: **26 tests** across transform, cache,
  gitsource (fake-git shim + mirror gate + sweep + build wiring), and the
  dotfile guard.
- Mutation checks (revert fix -> covering test must fail): 16/16 caught -
  gate (TTL and LRU branches), tmp-name classification, head pin, rate-limit
  retry, probe timeout, panic guard (helper and call site), genesis guard,
  `branches: []` shape, dotfile guard, PR-number dedupe (both call sites),
  gate wiring in `buildSnapshot` (acquire+release, and release-only),
  `endDelete` pairing, retry error fidelity.
- Slugify full contract: commitTotal 78, stars 78, releases force-included,
  monotonic birthT 0..1, scale.span 33.86, squashed PRs labeled honestly.
- next.js cold build on a throttled link: clone killed at exactly the 10m
  deadline, 504 returned immediately after (no overshoot). The repo is
  chartable given normal bandwidth; the sandbox link (~26 KiB/s) is the limit.
- Bogus-token E2E: public repo 200 with full contract (was 404).
- nocode E2E: `branches: []`, genesis birthT 0, cache key includes the version
  (`v2-s1400-b8`).
- Probe E2E: `CLONE_PROBE_TIMEOUT=2s` -> 504 in 2.012s.
- Dotfile E2E: `/.git/config` 404, `/.env` 404, `/` 200.

## Appendix B - Provenance of the two independent passes

- **Adversarial code review** (separate reviewer agent, same day): read the
  diff and the tests, ran `go build`/`vet`/`test -race`/`gofmt`, mutation-
  tested every fix in a scratch copy (`/tmp/opencode/review-scratch`, not part
  of the repo), and built fake-git/adversarial probes (encoded dotfile
  bypasses, symlink bypass, cache migration edge cases, concurrent cache,
  slow-fetch lease). Verdicts: 1.1-1.4, 1.7 VALID; 1.5 PARTIAL (residual
  TOCTOU + same-repo mirror races) and 1.6 PARTIAL (symlink bypass) - both
  addressed in 1.8/1.6. It also found: the vacuous head-pin test, missing
  tests for 1.2/1.7 (added), the `.git.tmp` substring misclassification, the
  prune boundary mismatch, and the read-timeout comment error. A **delta
  re-review** of the hardened tree then judged the mirror gate VALID and
  deadlock-free (closing both findings), asked for the three coverage tests
  now in 1.8.11, and returned "ready to commit: Yes".
- **Design research pass** (separate researcher agent, same day): validated
  Part 2 against Go/Kubernetes/SQLite/GitHub/Prometheus/SRE sources, verified
  `Shutdown`/`ResponseController`/`BaseContext` semantics with throwaway
  programs on go1.26.5, and surfaced the missed issues now in 1.8 and 2.11
  (panic containment, `WaitDelay`, context-less cache calls, free-disk,
  knob-decoupled `paramsVersion`, REST concurrency). It also amended the
  Part 4 sequence (metrics before admission; protocol freeze before `busy`).
