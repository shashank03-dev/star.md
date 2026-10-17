# Stellar backend

Go service that reads a real GitHub repo's history and emits the star-map
contract rendered by the locked renderer, `apps/web/lib/starmap/renderer.ts`
(`starmap-3d.html` was the original standalone reference). It also serves the
static frontend, replacing `python3 -m http.server 8770`.

## Where the data comes from

The history is read from **git**, not from the REST API. Each build makes one
`git ls-remote` (the HEAD sha, which is also the cache key) and, on a miss, one
blob-less bare clone - `git clone --bare --filter=tree:0` - which is then parsed
locally with `git log`, `for-each-ref` and `merge-base`.

This matters because the git transport is a **separate budget** from the REST
API's 5,000 requests/hour. Charting vercel/next.js (35,016 commits) costs zero
REST calls; the REST path it replaced cost ~1,400. One token is therefore no
longer the ceiling on how many repos this can serve.

REST is still used for exactly two things, neither of which git can answer:

- merged pull requests for repos whose commit history carries no PR reference
  (a rebase-merge workflow leaves nothing to parse) - at most 9 calls, and only
  when a token is configured;
- authenticating a **private** repo's clone.

Every REST call is logged as `rest: GET ...`, so "no `rest:` lines" is the proof
that a build stayed inside the git transport.

Requires `git` on PATH (the runtime image installs it).

## Run

```bash
# from the repo root, after `pnpm build` created apps/web/out
pnpm start:server
# or run the Go process directly:
cd apps/server
STATIC_DIR=../web/out go run .
```

Then open http://localhost:8770/ or http://localhost:8770/map/?repo=owner/name.

Auth is optional: public repos clone anonymously, even when a token is set
(network commands start anonymous and only retry with credentials when a repo
presents as private, or when the anonymous transport is rate-limited), so a
stale token cannot break public charting. Set `GITHUB_TOKEN` only for private
repos or the merged-PR fallback above. Static files with dot-prefixed URL path
segments (`.git`, `.env`) are rejected; symlinked targets are not resolved, so
keep links to dotfiles out of `STATIC_DIR`.

## Config (env)

| var | default | meaning |
|-----|---------|---------|
| `PORT` | `8770` | listen port |
| `STATIC_DIR` | `.` | folder served at `/` (run from repo root) |
| `CACHE_DB` | `stellar-cache.db` | SQLite cache path |
| `GITHUB_TOKEN` | (unset) | token for private repos / merged-PR fallback |
| `GITHUB_TOKENS` | (unset) | comma-separated pool, used round-robin; wins over `GITHUB_TOKEN` |
| `CLONE_DIR` | `$TMPDIR/stellar-repos` | where bare mirrors live |
| `CLONE_CONCURRENCY` | `4` | clones/fetches allowed to run at once |
| `CLONE_TIMEOUT` | `10m` | per-build ceiling (clone + parse) |
| `CLONE_PROBE_TIMEOUT` | `3m` | ceiling for the per-request HEAD probe (`git ls-remote`) |
| `CLONE_MAX_MB` | `4096` | per-repo disk cap; a clone past it is killed |
| `CLONE_DISK_MB` | `20480` | total mirror-cache budget before LRU eviction |
| `CLONE_TTL` | `168h` | drop mirrors untouched for this long |

## Endpoints

- `GET /healthz` -> `200 ok`
- `GET /api/starmap?repo=owner/name` -> contract JSON
  - `404 {"error":"not_found"}` unknown repo
  - `422 {"error":"empty_repo"}` repo with no commits
  - `503 {"error":"rate_limited"}` GitHub rate limit
- `GET /api/starmap/stream?repo=owner/name` -> NDJSON `progress` frames, then a
  terminal `result` (the contract) or `error`

## Notes

- The cache is best-effort: any cache failure is logged and swallowed, and the
  request falls through to a fresh GitHub build. A cache problem never produces
  an error page. Swap the `Cache` implementation in `cache.go` (2 methods) to
  move off SQLite.
- Cold builds are cached by `owner/repo + HEAD sha`, so a second request is
  instant until the repo gets a new commit. A cold build is one `git ls-remote`
  plus a blob-less bare clone parsed locally, bounded by `CLONE_TIMEOUT` and
  `CLONE_MAX_MB`. Warm requests are ~1s.
- The SQLite cache and the mirror directory are single-instance local state:
  never attach them to more than one replica (SQLite WAL is not safe on network
  filesystems). Scale by routing repos to instances, or swap the 2-method
  `Cache` interface for a shared store first.
