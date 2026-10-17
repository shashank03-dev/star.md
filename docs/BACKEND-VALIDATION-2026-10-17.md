# Backend validation and decisions — 2026-10-17

Scope: the Go backend (`apps/server`), its public TypeScript contract and NDJSON reader, the container configuration, and local development entrypoints. This complements, rather than replaces, [BACKEND-DESIGN-REVIEW.md](BACKEND-DESIGN-REVIEW.md).

## What was checked

- Read the latest two commits (`33ab77e`, `e93dea8`), their change list and design review; reviewed the active API, Git ingestion, transformation, cache, token handling, test cases, and deployment entrypoints.
- Confirmed the regression suite already covers the seven previous fixes: single-genesis reveal time; anonymous public access with a stale token; empty branches as `[]`; SQLite TTL/legacy migration; mirror gate and abandoned temp-dir cleanup; dotfile path blocking; and remote probe deadline. Tests also cover the later panic and PR-deduplication hardening.
- Checked `Contract` parity against `packages/contract/index.ts` and streaming behavior against `apps/web/lib/starmap/stream.ts`. No visual renderer changes were made.
- Re-ran `go test -race -count=1 ./...`, `go vet ./...`, and `go build ./...` from `apps/server`. The current change also has two regression tests that were demonstrated failing before their fixes and passing afterward.

## Two additional reproducible bugs, fixed

1. **Partial `git log` output could be treated as success.** `history()` discarded `cmd.Wait()` errors, so Git could print commits and exit nonzero, yet the service would return and cache an incomplete contract. `TestHistoryRejectsPartialGitLogFailure` uses a fake Git command that writes one commit and exits 128; it failed before the change. `history()` now verifies the subprocess exit status and discards the partial result on failure. This follows Go's [`os/exec.Cmd.Wait`](https://pkg.go.dev/os/exec#Cmd.Wait) contract.
2. **Fast oversized clones bypassed `CLONE_MAX_MB`.** `runCapped()` watched on a five-second timer; a clone that grew beyond the cap and finished inside that interval would be installed as a valid mirror. `TestEnsureMirrorRejectsFastOversizedClone` proved the gap before the change. The code now checks final on-disk size before the rename/installation and cleans up rejected clone directories. Periodic policing is still approximate during transfer; post-completion enforcement is exact for successful commands.

**Documentation issue corrected:** `pnpm start:server` previously invoked `gh auth token` implicitly, despite public Git access being anonymous by design. It now honors explicitly supplied `GITHUB_TOKEN`/`GITHUB_TOKENS` only. The backend README's bare Go command also inadvertently served `apps/server` as the static root; its example now supplies `STATIC_DIR=../web/out`.

## Design-review validation and outstanding issues

The principal conclusions in Part 2 remain valid against current code and published upstream behavior:

| Issue | Current evidence | Disposition |
| --- | --- | --- |
| 2.1 Unbounded admission wait | `snapshotVia()` detaches the originating request, and `buildSnapshot()` waits for a semaphore before establishing its build deadline. Queue wait can therefore grow without a deadline. | Decide acceptable queue budget and overload policy before implementation. |
| 2.2 HEAD-only freshness | Cache key contains default HEAD but not tags/side-branch refs; every request also makes a new `ls-remote` call. | Short probe memo is a good incremental upgrade; a refs digest needs product freshness requirements. |
| 2.3 Slow streaming readers | `json.Encoder.Encode()`/`Flush()` has no per-write deadline and ignores errors. | Confirm connection lifetime, then add cancel-on-write-failure and write deadlines. |
| 2.4 Metrics and readiness | Only static `/healthz` exists. There is no clone queue telemetry or `/readyz`. | Choose the observability endpoint and target environment before wiring it. |
| 2.5 Graceful drain | `ListenAndServe()` exits fatally on shutdown; streams may be cut mid-analysis. | Define deployment grace period; coordinate readiness with a draining signal. |
| 2.6 Abuse and fairness | Cold clones bounded individually but no client-level request budget. | Choose edge/proxy identity and volume expectations before choosing limits. |
| 2.7 Optional REST pull metadata | Trigger is `!hasSizedPR`; repositories mixing merge types can remain only partly enriched. | Keep today's REST-sparing default until a product decision changes it. |
| 2.9 Local mirrors and SQLite | Local disk and WAL are single-instance design. | Continue single container per current architecture; do not mount the same cache volume to multiple replicas. |
| 2.10 Streaming protocol | Client understands unversioned progress/result/error events. | Freeze vocabulary before introducing `busy` or other errors. |
| 2.11 Free-space awareness | `CLONE_DISK_MB` is a mirror eviction target, not a filesystem headroom check. | Decide minimum free-space thresholds for the actual hosting volume. |

Primary references used to cross-check the proposed approach: [Git partial clone documentation](https://git-scm.com/docs/git-clone), [Go `Cmd.Wait` and subprocess semantics](https://pkg.go.dev/os/exec), [Go `http.Server.Shutdown`](https://pkg.go.dev/net/http#Server.Shutdown), [Go `ResponseController`](https://pkg.go.dev/net/http#ResponseController), [Go singleflight](https://pkg.go.dev/golang.org/x/sync/singleflight), [SQLite WAL constraints](https://www.sqlite.org/wal.html), and [SRE handling overload](https://sre.google/sre-book/handling-overload/). These support the mechanisms; timeout thresholds, capacity targets, and rollout order still require operational assumptions.

## Decisions needed before changing backend architecture

1. **Traffic and deployment:** Is this still one Docker container on a single host, possibly behind a reverse proxy? What number of simultaneous *different* cold repository analyses should it support, and what maximum queue wait is acceptable? The review's 45-second wait is an example rather than a measured target.
2. **Health and metrics:** Is there already a Prometheus-compatible monitoring stack? If not, favor Go's built-in `expvar` and a simple `/readyz`, recording stable metrics without repo-name labels. What platform shutdown grace period is configured?
3. **Freshness and PR fidelity:** Should a new tag or branch update appear within seconds for the eventual README embed, or is the current 24-hour contract TTL sufficient? Should merged-PR enrichment spend REST calls whenever any PR is unsized, or only under the current fallback rule?

No speculative capacity values, shared-cache migration, stream protocol expansion, or new deployment infrastructure were introduced during this validation pass.
