# Stellar

Paste a GitHub repo, get an animated 3D star map of its history. The initial
commit is the genesis star at the center; history grows outward by timestamp as
a warped nebula galaxy - commits are stars, branches are constellations, pull
requests are the lines home, releases are milestones.

One repository, shipped as a single container: a Go service serves both the
statically-exported Next.js frontend and a `/api` that charts real repo history
from a blob-less `git clone --filter=tree:0` (REST only for merged-PR metadata
and private repos), with a best-effort SQLite cache.

## Structure

```
apps/
  web/          Next.js 16 (App Router, static export) - landing + star map
    lib/starmap/  the locked Three.js renderer (ported intact)
  server/       Go service: GitHub -> star-map contract, best-effort cache,
                also serves the built frontend
packages/
  contract/     shared TypeScript types mirroring the Go contract
Dockerfile      multi-stage: node build -> go build -> alpine + git runtime
docker-compose.yml
```

The star-map visual is locked (see `DESIGN-LOCK.md`). The backend feeds it real
data through the contract in `packages/contract`; it never computes 3D positions
- the renderer does, from the abstract model, which is what preserves the look.

## Run with Docker (production shape)

```bash
export GITHUB_TOKEN=$(gh auth token)   # optional; only private repos / PR fallback
docker compose up --build
# open http://localhost:8770
```

Or plain Docker:

```bash
docker build -t stellar .
docker run -p 8770:8770 -e GITHUB_TOKEN=$(gh auth token) \
  -v stellar-cache:/data -v stellar-mirrors:/mirrors stellar
```

Public repos need no token: history is cloned anonymously over the git
transport, a separate budget from the REST API's 5,000 req/hr. Set a token
only for private repos or the merged-PR fallback. Caches are best-effort: if
`/data` or `/mirrors` is not writable the app still serves correct maps, just
without the speedup.

## Local development

Two processes. Build the frontend once (or run `next dev`), and run the Go
server pointing at the export:

```bash
pnpm install
pnpm --filter web build                        # emits apps/web/out
pnpm start:server                              # Go serves out/ + /api on :8770
# open http://localhost:8770
```

For hot-reloading UI work, run the frontend dev server and point its API at the
Go backend:

```bash
# terminal 1: Go API + static (as above)
pnpm start:server
# terminal 2: Next dev with the API proxied to the Go server
NEXT_PUBLIC_API_BASE=http://localhost:8770 pnpm --filter web dev
# open http://localhost:3000
```

## Endpoints

- `GET /` - landing page
- `GET /map/?repo=owner/name` - the star map for a repo
- `GET /api/starmap?repo=owner/name` - the contract JSON
  - `404 {"error":"not_found"}` unknown repo
  - `422 {"error":"empty_repo"}` repo with no commits
  - `503 {"error":"rate_limited"}` GitHub rate limit
- `GET /api/starmap/stream?repo=owner/name` - NDJSON progress stream: `progress`
  frames, then a terminal `result` (the contract) or `error`
- `GET /healthz` - `200 ok`

## Config (env)

| var | default | meaning |
|-----|---------|---------|
| `PORT` | `8770` | listen port |
| `STATIC_DIR` | `.` | folder served at `/` (the Next export) |
| `CACHE_DB` | `stellar-cache.db` | SQLite cache path (best-effort) |
| `GITHUB_TOKEN` | (unset) | private repos + merged-PR fallback; local dev falls back to `gh auth token` |
| `GITHUB_TOKENS` | (unset) | comma-separated token pool; wins over `GITHUB_TOKEN` |
| `NEXT_PUBLIC_API_BASE` | (empty) | frontend-only: absolute API origin for split dev |

The `CLONE_*` mirror-cache knobs (dir, concurrency, timeout, `CLONE_MAX_MB`,
`CLONE_DISK_MB`, TTL) are documented in `apps/server/README.md`.

## Notes

- Cold builds are one blob-less bare clone parsed locally, bounded by the
  `CLONE_*` size/time caps; warm requests are cached by `owner/repo + HEAD sha`
  and return in ~1s.
- Swap the `Cache` implementation in `apps/server/cache.go` (two methods) to move
  off SQLite to Postgres/Redis/Turso.
