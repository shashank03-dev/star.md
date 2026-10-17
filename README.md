# Stellar

**Your GitHub history, mapped as a living galaxy.**

Stellar turns a repository's real commit history into an interactive, animated 3D star map. The first commit ignites at the center; subsequent commits form a growing galactic disc. Branches become constellations, pull requests connect them to the main history, and releases mark significant stars. Paste `owner/repo` or a GitHub repository URL into the app to explore it.

This repository contains the website and its Go API. Both ship together in **one Docker container**. Public repositories work without a GitHub token.

> **Status:** The interactive website and on-demand repository analysis are implemented. The live-updating embeddable README artifact is planned for phase 2. See [DESIGN-LOCK.md](DESIGN-LOCK.md) for the established visual behavior.

## How it works

```text
Browser (Next.js static export)
    │
    ├── /                      Landing / repository input
    └── /map/?repo=owner/name  Interactive 3D map
            │
            └── GET /api/starmap/stream?repo=owner/name
                    │
                    ▼
               Go service
               ├── git ls-remote       Find default branch and HEAD
               ├── SQLite cache        Reuse an existing contract by HEAD
               ├── Bare partial clone   Fetch commit graph, branches, tags
               ├── Local git parsing    Sample actual commits and PR merges
               └── Optional GitHub REST Enrich merged PRs when needed
                    │
                    ▼
          Shared repository contract
                    │
                    ▼
              Three.js renderer
```

The backend sends **repository facts**, not 3D coordinates. The renderer consistently places real commits using a deterministic, seeded layout. Visual decisions are locked in [DESIGN-LOCK.md](DESIGN-LOCK.md); backend changes must preserve the shape of [`packages/contract`](packages/contract/index.ts).

The Git transport retrieves public history anonymously using `git clone --bare --filter=tree:0`. GitHub REST is reserved for optional merged-PR enrichment, while an access token can also unlock private repositories. When `GITHUB_TOKEN` is set, the backend may make REST requests for repositories whose history lacks recoverable PR counts.

## Quick start: Docker

Install [Docker](https://docs.docker.com/get-docker/) with the Compose plugin. From the repository root:

```bash
git clone https://github.com/shashank03-dev/star.md.git
cd star.md
docker compose up --build
```

Open **http://localhost:8770**, or load an example map directly at **http://localhost:8770/map/?repo=octocat/Hello-World**.

No environment variables are required for public repositories. To enable private-repository access and optional GitHub REST enrichment, set `GITHUB_TOKEN` before starting Compose:

```bash
export GITHUB_TOKEN="your-github-token"
docker compose up --build
```

The Compose configuration persists SQLite contracts and Git mirrors in two Docker volumes. Stop it with `docker compose down` (this preserves the volumes; `down -v` removes them).

## Local development

**Prerequisites:** Node.js 24, pnpm 11, Go **1.26.5+**, and `git` on `PATH`. [Corepack](https://nodejs.org/api/corepack.html) can provision the pnpm version declared in `package.json`.

```bash
corepack enable
pnpm install --frozen-lockfile
pnpm build                 # creates apps/web/out
pnpm start:server          # serves the static app and API at localhost:8770
```

For browser hot reload, leave the Go server running and start Next.js in another terminal:

```bash
NEXT_PUBLIC_API_BASE=http://localhost:8770 pnpm dev
# Open http://localhost:3000
```

`NEXT_PUBLIC_API_BASE` is baked into the static frontend at build time. For same-origin Docker deployments, leave it unset. Local Go commands can also be run from the root with `go -C apps/server test ./...`.

## API

The Go process serves the static website and these endpoints:

| Endpoint | Response |
| --- | --- |
| `GET /healthz` | `200 ok` (process liveness) |
| `GET /api/starmap?repo=owner/name` | JSON [`Contract`](packages/contract/index.ts) |
| `GET /api/starmap/stream?repo=owner/name` | Newline-delimited JSON progress events followed by a `result` or `error` event |

Example:

```bash
curl 'http://localhost:8770/api/starmap?repo=octocat/Hello-World'
curl -N 'http://localhost:8770/api/starmap/stream?repo=octocat/Hello-World'
```

The stream sends phases such as `resolve`, `head`, `history`, and `map`. Once the first event is sent, the stream's HTTP status is `200`; subsequent failures are reported *in-band* as `{"phase":"error","code":"...","message":"..."}`. A successful terminal event is `{"phase":"result","contract":{...}}`.

The JSON endpoint uses typed errors: `400 bad_repo`, `404 not_found`, `422 empty_repo`, `413 repo_too_large`, `503 rate_limited`, and `504 clone_timeout`; Git failures can also produce 5xx responses. Do not use the HTTP status of a started stream to determine whether the analysis succeeded.

## Backend configuration

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8770` | HTTP listening port |
| `STATIC_DIR` | `.` | Built frontend directory (Docker sets `/app/web`) |
| `CACHE_DB` | `stellar-cache.db` | SQLite contract cache path (Docker sets `/data/stellar-cache.db`) |
| `GITHUB_TOKEN` | unset | Private Git auth and optional REST enrichment |
| `GITHUB_TOKENS` | unset | Comma-separated token pool; overrides `GITHUB_TOKEN` |
| `CLONE_DIR` | OS temp dir + `/stellar-repos` | Bare Git mirror cache (Docker sets `/mirrors`) |
| `CLONE_CONCURRENCY` | `4` | Maximum concurrent clone/parse jobs |
| `CLONE_TIMEOUT` | `10m` | Time budget for a build after it obtains a slot |
| `CLONE_PROBE_TIMEOUT` | `3m` | Time limit for each remote HEAD probe |
| `CLONE_MAX_MB` | `4096` | Maximum size of an individual mirror |
| `CLONE_DISK_MB` | `20480` | Target total mirror storage budget |
| `CLONE_TTL` | `168h` | Time before unused mirrors can be evicted |
| `NEXT_PUBLIC_API_BASE` | unset | Build-time API origin for split frontend development |

Contract JSON is cached for 24 hours per `repository + default-branch HEAD SHA + transform version`. Changes to tags or other branches *without* a new default-branch commit may remain invisible until that cache entry expires. Git mirrors are incrementally fetched and periodically evicted. Cache failures are best-effort and trigger fresh builds.

**Operational limits:** This is currently a **single-instance** design. Keep `/data` and `/mirrors` writable and attached to only one server instance; do not share a SQLite WAL file across hosts or a network filesystem. Queue admission limits, readiness checks, metrics, graceful shutdown, and stream write deadlines are tracked in the [backend design review](docs/BACKEND-DESIGN-REVIEW.md) and [validation notes](docs/BACKEND-VALIDATION-2026-10-17.md). They are not implemented yet.

## Repository map

```text
apps/
  server/             Go HTTP API, Git ingestion, transform, cache and tests
  web/                Next.js 16 / React 19 static website
    lib/starmap/       Renderer, demo model and streaming client
    components/       Landing, map and animation components
packages/contract/    Shared TypeScript contract for the Go API
DESIGN-LOCK.md         Locked galaxy behavior and visual constraints
docs/                  Backend findings, verification and planned work
Dockerfile             Build frontend and backend into one container
docker-compose.yml     Local runtime and persistent volumes
CLAUDE.md              Contributor/agent guidance for this repository
```

## Checks

```bash
go -C apps/server test -race -count=1 ./...
go -C apps/server vet ./...
go -C apps/server build ./...
pnpm build
```

The backend tests cover the contract's genesis and branch invariants, SQLite expiry and migration, Git authorization fallbacks, clone size limits, mirror eviction arbitration, panic containment, and partial-Git-output failures. The frontend build verifies the static export. See [the backend design review](docs/BACKEND-DESIGN-REVIEW.md) for the previous hardening pass and known architecture choices.

## Roadmap and constraints

- **Phase 1:** Website, real history ingestion, animated star map. Keep the renderer's visual design intact.
- **Backend reliability:** Validate admission limits, monitoring, shutdown, stream deadlines, and cache freshness policy before changing the deployment architecture. See [current decision points](docs/BACKEND-VALIDATION-2026-10-17.md).
- **Phase 2:** A live-updating map artifact suitable for embedding in repository READMEs.

No license file is included in this repository; do not assume an open-source license for reuse or redistribution.
