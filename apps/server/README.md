# Stellar Go backend

This service turns GitHub repository history into the shared, versioned Stellar
[Contract](../../packages/contract/index.ts). The interactive Three.js map and
GitHub README SVG/GIF images consume the **same contract**; their presentation
does not affect Git history parsing or output semantics.

## Request flow

1. Validate the GitHub `owner/repo` identifier and probe its HEAD using
   `git ls-remote` (30-second in-process memo, bounded concurrent probes).
2. Look up `owner/repo@HEAD#paramsVersion` in the contract cache. On Vercel,
   Upstash Redis REST shares the cache across instances; local Docker uses
   SQLite.
3. On a Vercel cache miss, acquire a short-lived Redis build lease so other
   instances wait for the first completed contract. The local server also uses
   `singleflight` and a per-repo mirror lock.
4. Admit a bounded concurrent Git job, clone/fetch a blob-less bare mirror
   (`git clone --bare --filter=tree:0`), parse commits/branches/tags locally,
   and optionally enrich merged pull requests through GitHub REST.
5. Construct a deterministic contract, write it to the cache for 15 minutes,
   and return JSON, NDJSON progress, static SVG, or animated GIF.

Public repositories work anonymously, without consuming personal GitHub REST
quota for their commit histories. The optional GitHub token is used for
merged-PR metadata and allowed authenticated fallbacks.

## Routes

| Route | Behavior |
| --- | --- |
| `GET /healthz` | Process liveness |
| `GET /readyz` | Git and required Vercel cache configuration |
| `GET /api/status` | Per-instance active and queued jobs, no global totals |
| `GET /api/starmap?repo=owner/name` | Contract JSON |
| `GET /api/starmap/stream?repo=owner/name` | Flushed NDJSON with terminal result/error |
| `GET /api/badge?repo=owner/name&size=medium` | Static self-contained SVG; sizes compact, medium, large |
| `GET /api/badge.gif?repo=owner/name` | Looping 11-frame 720×200 GIF, genesis to HEAD |

Badge routes return five-minute publicly cacheable images. Do **not** enable
private repo access on a public deployment: public URLs and GitHub image proxies
cannot enforce repository permissions. Vercel disables private-token fallback
unless deliberately overridden; that override requires visitor authentication
and authorization to be designed first. Git transport arguments do not contain
embedded tokens.

## Local server

Prerequisites: Go 1.26.5+, Git, and optionally the prebuilt frontend in
`apps/web/out`.

```bash
# From the repository root
go -C apps/server test -race -count=1 ./...
go -C apps/server vet ./...
go -C apps/server build ./...
pnpm build
pnpm start:server
```

Open http://localhost:8770/map/?repo=octocat/Hello-World. Or run the single
container with `docker compose up --build`. If using `go run .` from
`apps/server`, set `STATIC_DIR=../web/out` for local static serving.

## Production (two Vercel projects)

Set the API project's root directory to `apps/server`. Vercel auto-detects
`Dockerfile.vercel`, which installs Git, builds the Go server, and listens on
`$PORT` (8080 in the image). Set the Vercel project environment variable
`PORT=8080`, then set `UPSTASH_REDIS_REST_URL` and
`UPSTASH_REDIS_REST_TOKEN` from the linked Upstash Marketplace Redis store.
The service returns a typed 503 when this cache is missing in Vercel.
Ephemeral `/tmp` mirrors are disposable; a shared SQLite file is unsuitable
for separate Functions. The frontend Vercel project uses root `apps/web`,
with `NEXT_PUBLIC_API_BASE` pointing to the API HTTPS origin.

Default Vercel clone concurrency is 1 per instance; clone queue length is
32, wait timeout 8s, cold build budget 150s, per-repo mirror cap 300 MiB,
scratch mirror budget 380 MiB, and probe timeout 20s. Actual Vercel plan and
request duration limits also apply. A 503 includes `Retry-After: 20`.
This architecture serves bounded on-demand analyses, not unlimited demand or
arbitrarily large repositories; see
[the deployment plan](../../docs/VERCEL-ARCHITECTURE.md) for the durable
queue/worker upgrade path.

| Environment variable | Local default | Purpose |
| --- | --- | --- |
| `PORT` | `8770` | HTTP listen port (set Vercel project to 8080) |
| `STATIC_DIR` | `.` | Serve static frontend locally; `-` disables |
| `CACHE_DB` | `stellar-cache.db` | Local SQLite only |
| `UPSTASH_REDIS_REST_URL`, `UPSTASH_REDIS_REST_TOKEN` | unset | Required on Vercel |
| `GITHUB_TOKEN`, `GITHUB_TOKENS` | unset | Optional PR metadata and retry pool |
| `CLONE_DIR`, `CLONE_TTL` | OS temp, 168h | Mirror path and local retention |
| `CLONE_CONCURRENCY`, `CLONE_QUEUE_MAX`, `CLONE_QUEUE_TIMEOUT` | 4, 32, 20s | Admission control |
| `CLONE_TIMEOUT`, `CLONE_PROBE_TIMEOUT` | 10m, 3m | Time budgets |
| `CLONE_MAX_MB`, `CLONE_DISK_MB` | 4096, 20480 | Scratch budget |
| `HEAD_PROBE_TTL`, `PROBE_CONCURRENCY` | 30s, 16 | Probe reuse and limits |

Errors are returned as typed JSON for normal HTTP requests. Once an NDJSON
stream starts, failures arrive as a terminal `error` frame; consumers must
not infer success from HTTP 200 alone. The locked 3D renderer, transformation
invariants, and prior hardening are described in
[DESIGN-LOCK.md](../../DESIGN-LOCK.md),
[AGENTS.md](../../AGENTS.md), and
[backend design review](../../docs/BACKEND-DESIGN-REVIEW.md).
