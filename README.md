# Stellar — turn GitHub history into a galaxy

Stellar visualizes a repository's real commit history as an animated, interactive **3D star map**. The first commit ignites at the center. Subsequent commits spread into a galactic disc; branches form constellations, merges draw connections, and releases become brighter stars.

Explore the galaxy in the browser or embed a **live, repository-specific galaxy image** in a GitHub README. The embed is a real animated GIF or static SVG generated from the same backend data that drives the interactive view.

**Status (October 2026):** The Go analyzer, 3D website, SVG/GIF endpoints, and Vercel deployment configuration are implemented and tested locally. Production deployment requires creating the Vercel projects, linking Upstash Redis, and setting the API URL. Example domains below are placeholders. **No hosted badge URL is live yet.**

## Embed your repository galaxy

Once the Stellar API is deployed, add either snippet to your repository's `README.md`. Replace `YOUR-API.vercel.app` with the deployed API hostname, and `OWNER/REPO` with your GitHub repository.

**Animated galaxy (GIF; growth from genesis to HEAD):**

```md
[![My repository galaxy](https://YOUR-API.vercel.app/api/badge.gif?repo=OWNER/REPO)](https://YOUR-WEB.vercel.app/map/?repo=OWNER/REPO)
```

**Static galaxy (SVG; compact, medium, or large):**

```md
[![My repository galaxy](https://YOUR-API.vercel.app/api/badge?repo=OWNER/REPO&size=medium)](https://YOUR-WEB.vercel.app/map/?repo=OWNER/REPO)
```

The image links to the full interactive map. GitHub README images cannot execute JavaScript or WebGL, so they **cannot embed the interactive Three.js scene**. Stellar renders the GIF and SVG on the server from your repository's commit contract; they represent the galaxy with a lightweight 2D projection suitable for GitHub's image proxy. The GIF loops through 11 growth frames (720 × 200 pixels); SVG sizes are `compact`, `medium` (default), and `large`.

Images receive a 5-minute public cache lifetime. GitHub's image proxy may retain them longer; changes to tags, side branches, or the default branch typically become visible after the 15-minute contract cache expires, plus any upstream image caching. Very large, inaccessible, or rate-limited repositories can return an error rather than an image. **Use public repositories for public embeds.** On the default Vercel configuration, private repositories are not served.

## Architecture

```text
GitHub README <img> → Vercel API → /api/badge[.gif] → SVG / looping GIF
                                                   ↑
Browser → Vercel static Next.js /map/ → Go API → shared Contract
                                           │
                                           ├── git ls-remote (HEAD probe)
                                           ├── Upstash Redis REST cache (15m)
                                           ├── Redis build lease (cross-instance)
                                           ├── bounded queue + per-instance clone limit
                                           ├── blob-less bare Git clone in temporary storage
                                           ├── git log / branches / tags / merges
                                           └── optional GitHub REST merged-PR enrichment
                                                                    │
Shared Contract → deterministic Three.js galaxy renderer (browser)
```

The Go API returns facts, sampled commits, release markers, and branch summaries as a versioned [Contract](packages/contract/index.ts). It does **not** dictate 3D coordinates. The frontend uses the contract to render the locked [galaxy design](DESIGN-LOCK.md). A public repository can be analyzed anonymously over Git without using a GitHub personal access token. GitHub REST calls are optional and limited to merged-PR enrichment. The README images reuse the identical contract.

## Deploy both services on Vercel

This repo supports **two Vercel projects** connected to the same GitHub repository and deploying directly from `main`:

| Project | Root directory | Deployment | Configuration |
| --- | --- | --- | --- |
| API | `apps/server` | `Dockerfile.vercel` container Function with Go + Git | Link Upstash Redis; set `UPSTASH_REDIS_REST_URL`, `UPSTASH_REDIS_REST_TOKEN`, `PORT=8080` |
| Website | `apps/web` | Next.js static export (`out`) | Set `NEXT_PUBLIC_API_BASE=https://YOUR-API.vercel.app` at build time |

The API needs a container because the Git binary is required for partial cloning and parsing. Vercel Functions have **ephemeral local storage**; Redis is shared across instances. Configure the two projects and their environment variables before sending real traffic. Set the frontend root's **Include source files outside the Root Directory** option so it can access `packages/contract` and the workspace lockfile. The frontend refuses a Vercel build if the public API origin is missing or malformed.

The API's `/readyz` checks the Git executable and cache configuration; `/api/status` exposes local active/waiting jobs. Clone and disk limits are enforced per Function. Heavy repositories may exceed these limits and return a typed error; protecting the service from overload is preferable to hanging all requests. Redis leases coalesce concurrent cold builds across instances, and retries after overload are supported with `Retry-After`. Production traffic testing, cost/quotas, GitHub image proxy behavior, and Vercel Observability still require a live deployment.

Full deployment instructions and operational limits: [Vercel architecture](docs/VERCEL-ARCHITECTURE.md) and [frontend deployment](docs/VERCEL-FRONTEND.md).

## Run locally

Prerequisites: **Go 1.26.5+**, **Git**, **Node.js 24**, **pnpm 11**, or Docker and Docker Compose.

The easiest local setup runs both services in one container with SQLite and persistent bare Git mirrors:

```sh
git clone https://github.com/shashank03-dev/star.md.git
cd star.md
docker compose up --build
```

Open `http://localhost:8770` and navigate to `http://localhost:8770/map/?repo=octocat/Hello-World`.

For local development without Docker:

```sh
corepack enable
pnpm install --frozen-lockfile
pnpm build
pnpm start:server
```

For frontend hot reload, run the Go API as above and, in another terminal:

```sh
NEXT_PUBLIC_API_BASE=http://localhost:8770 pnpm dev
```

Open `http://localhost:3000`. No GitHub token is needed for public repositories. A local trusted deployment may set `GITHUB_TOKEN` or `GITHUB_TOKENS` for optional PR enrichment/private clone fallback. **Do not enable private repository access on a publicly reachable API without per-user authorization.**

## HTTP API

| Endpoint | Purpose |
| --- | --- |
| `GET /healthz` | Process liveness |
| `GET /readyz` | Git/shared-cache readiness |
| `GET /api/status` | Local worker and queue counters |
| `GET /api/starmap?repo=OWNER/REPO` | JSON repository Contract |
| `GET /api/starmap/stream?repo=OWNER/REPO` | NDJSON progress plus terminal result/error |
| `GET /api/badge?repo=OWNER/REPO&size=medium` | Static galaxy SVG |
| `GET /api/badge.gif?repo=OWNER/REPO` | Animated genesis-to-HEAD galaxy GIF |

Test locally with:

```sh
curl 'http://localhost:8770/api/starmap?repo=octocat/Hello-World'
curl -N 'http://localhost:8770/api/starmap/stream?repo=octocat/Hello-World'
curl -o /tmp/stellar.gif 'http://localhost:8770/api/badge.gif?repo=octocat/Hello-World'
curl -o /tmp/stellar.svg 'http://localhost:8770/api/badge?repo=octocat/Hello-World'
```

NDJSON frames use `resolve`, `head`, `history`, and `map` progress phases, then a `result` containing the complete contract or an `error` with a typed code. After streaming begins, HTTP status remains 200 even when a later frame reports failure. Other routes return HTTP errors such as 400 invalid repo, 404 missing, 413 oversized repo, 422 empty repo, 503 busy/rate-limited, and 504 timeout.

## Runtime settings

| Variable | Local default | Vercel default / role |
| --- | --- | --- |
| `PORT` | 8770 | Set `PORT=8080` on the API project for its unprivileged container listener |
| `STATIC_DIR` | `.` | `-` (API only) |
| `CACHE_DB` | `stellar-cache.db` | Not used with Redis |
| `UPSTASH_REDIS_REST_URL` / `UPSTASH_REDIS_REST_TOKEN` | Unset | Required for Vercel shared cache/lease |
| `GITHUB_TOKEN` / `GITHUB_TOKENS` | Unset | Optional PR enrichment and rate-limit retry |
| `ALLOW_PRIVATE_REPOS` | Not applicable to local fallback | Disabled on Vercel; **never enable publicly without auth** |
| `CLONE_CONCURRENCY` | 4 | 1 per instance |
| `CLONE_QUEUE_MAX` / `CLONE_QUEUE_TIMEOUT` | 32 / 20s | 32 / 8s |
| `CLONE_TIMEOUT` / `CLONE_PROBE_TIMEOUT` | 10m / 3m | 150s / 20s |
| `CLONE_MAX_MB` / `CLONE_DISK_MB` | 4096 / 20480 | 300 / 380 |
| `HEAD_PROBE_TTL` / `PROBE_CONCURRENCY` | 30s / 16 | 30s / 16 |
| `CLONE_DIR` / `CLONE_TTL` | OS temp / 168h | Ephemeral mirrors |
| `NEXT_PUBLIC_API_BASE` | Unset | Website's HTTPS API origin, baked into build |

## Project layout

```text
apps/server/              Go API, Git reader, SQLite/Redis caches, badge renderers, tests
apps/server/Dockerfile.vercel
apps/web/                 Next.js 16 / React 19 static frontend
apps/web/lib/starmap/     Deterministic Three.js renderer and contract client
packages/contract/        Shared API types and invariants
docs/                     Backend review, validation and Vercel operations
DESIGN-LOCK.md             Authoritative galaxy design specification
AGENTS.md / CLAUDE.md     Coding and repository guidance
Dockerfile                Combined local Docker deployment
docker-compose.yml        Local persisted SQLite and mirrors
```

Run checks before committing:

```sh
go -C apps/server test -race -count=1 ./...
go -C apps/server vet ./...
go -C apps/server build ./...
NEXT_PUBLIC_API_BASE=https://api.example.com pnpm build
```

The code preserves the visual contract and existing design constraints; see [backend design findings](docs/BACKEND-DESIGN-REVIEW.md) and [validation](docs/BACKEND-VALIDATION-2026-10-17.md) for the earlier bug fixes. Current Vercel decisions and limitations are in [the architecture document](docs/VERCEL-ARCHITECTURE.md).

**License:** No license file is currently included; do not assume permission to redistribute the source.
