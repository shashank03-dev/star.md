# Stellar on Vercel — deployment and operating model

Stellar keeps its Go ingestion/transformer and locked Three.js renderer. Deploy two projects from the same GitHub repository, both on Vercel. The Go project is a **container-based Vercel Function**, which bundles the required Git binary. The Next.js project is a static export. A Redis store provisioned through the **Vercel Marketplace (Upstash)** holds contracts across independent API instances. Redis is operated by the Marketplace provider; it is not Vercel's own storage engine.

```text
GitHub README image → Vercel CDN → /api/badge[.gif]?repo=owner/name
Browser → static Vercel web → Go API /api/starmap[/stream]
                                      │
                                      ├─ Git ls-remote (bounded, in-process memo)
                                      ├─ Upstash Redis REST (shared contract cache, 15m TTL)
                                      ├─ short, bounded cold-analysis queue
                                      └─ Git bare partial clone in function /tmp
                                                   └─ deterministic Contract → SVG/GIF or Three.js
```

## Project setup

1. Import `shashank03-dev/star.md` **twice** into Vercel. Name the projects e.g. `stellar-api` and `stellar-web`. Select **Root Directory `apps/server`** for the API and **`apps/web`** for the web project. Both build automatically on pushes to `main`, without pull requests.
2. In the API project, use **Dockerfile / Container** detection with `apps/server/Dockerfile.vercel`. It installs Git and compiles the existing Go service. Set the project's `PORT=8080` environment variable so Vercel routes to the unprivileged container listener; `STATIC_DIR=-` disables static file serving on the API origin. **Do not select Go framework preset instead of the container**, since the Git executable is a required runtime dependency.
3. In the Vercel Marketplace for the API project, provision **Upstash for Redis** and link the database. The integration automatically injects encrypted `KV_REST_API_URL` and `KV_REST_API_TOKEN` for the selected environments; the Go service uses these directly. Explicit `UPSTASH_REDIS_REST_URL` and `UPSTASH_REDIS_REST_TOKEN` also work and take precedence. A local Docker service continues using SQLite when neither Redis pair is set. The shared cache is essential for the API project: local SQLite and Git mirrors inside Vercel Functions are ephemeral.
4. In the web project, configure `NEXT_PUBLIC_API_BASE=https://<api-project-domain>` as a **build-time** environment variable (omit a trailing slash). Run the standard Next.js build; `apps/web/next.config.ts` retains static export and original visuals. This direct-origin approach uses the API's CORS headers. If using custom domains, point this at the API domain.
5. Verify `GET /readyz` on the API returns `200 ready`; `503 backend unavailable` means Git or required serverless cache is absent. Verify a small public repo using `/api/starmap?repo=octocat/Hello-World` and an actual embedded `/api/badge?repo=octocat/Hello-World` image, then `/api/starmap/stream?repo=octocat/Hello-World`. Run a bounded concurrent traffic test before advertising to a large audience.

**Production setup (2026-10-17):** The `stellar-api` container project and `stellar-web` static project are linked to `shashank03-dev/star.md` and deploy from `main`. Upstash for Redis is connected to the API for production and preview through its automatic `KV_REST_API_*` variables. Production origins are `https://stellar-api-shashank03-devs-projects.vercel.app` and `https://stellar-web-taupe.vercel.app`. The API returned 200 for `/readyz`, a public repository JSON contract, SVG/GIF badges, and the terminal NDJSON progress stream; 16 concurrent cached contract requests all succeeded. These bounded checks do not establish unlimited concurrency or fault tolerance.

## Backend traffic and limits

| Control | Docker default | Vercel default | Why |
| --- | --- | --- | --- |
| `CLONE_CONCURRENCY` | `4` | `1` | Heavy Git jobs per process |
| `CLONE_QUEUE_MAX` | `32` | `32` | Reject load beyond the finite admission queue |
| `CLONE_QUEUE_TIMEOUT` | `20s` | `8s` | Return `503 busy`, with `Retry-After: 20`, instead of waiting indefinitely |
| `PROBE_CONCURRENCY` | `16` | `16` | Bound GitHub HEAD probes per instance |
| `HEAD_PROBE_TTL` | `30s` | `30s` | Reuse HEAD probes briefly across visits |
| `CLONE_PROBE_TIMEOUT` | `3m` | `20s` | Fail a stalled Git probe |
| `CLONE_TIMEOUT` | `10m` | `150s` | Fit ordinary Git work within a Vercel Function duration |
| `CLONE_MAX_MB` | `4096` | `300` | Cap a single partial mirror |
| `CLONE_DISK_MB` | `20480` | `380` | Preserve scratch-space headroom for function runtime |
| `RATE_LIMIT_REQUESTS` / `_WINDOW` | `60` / `1m` | same | Per-visitor API budget; `429 too_many_requests` with `Retry-After` |
| `RATE_LIMIT_BUILDS` / `_BUILD_WINDOW` | `10` / `10m` | same | Per-visitor budget for cache misses that clone |

**Per-visitor limits.** The visitor is the client IP: Vercel overwrites `x-real-ip`/`x-forwarded-for` at its edge, so they cannot be spoofed there; Docker trusts only the socket address unless `TRUST_PROXY_HEADERS=true`. IPv6 is grouped by /64. On Vercel the fixed-window counters live in Upstash Redis (one `EVAL` per request) so every instance shares a budget; locally they are in memory. The limiter fails open if Redis is unreachable, leaving the global clone queue as the backstop. README badges (`/api/badge`, `/api/badge.gif`) are not limited per visitor, because GitHub's camo proxy fetches every README image from shared addresses. The Vercel Firewall's own rate-limit rules can be layered on top as an edge filter, but they count requests rather than clones and are capped per plan, so they complement rather than replace this. A repo that is not found is remembered for 60 seconds per instance, so typos and crawlers do not re-probe GitHub on every request. Repository names are canonicalized to lowercase (GitHub names are case-insensitive), so every spelling shares one cache entry, mirror and layout.

Contract cache TTL is 15 minutes. This also bounds the usual delay for tags and non-default branch changes when HEAD does not change. That lag is deliberate: the cache key is HEAD-based because the per-request `ls-remote` asks only for `HEAD`; keying on every branch and tag would make each visit download the full ref list (tens of thousands of lines on large repos) to save at most 15 minutes of staleness on refs the map barely shows. Vercel Functions have a limited writable temporary filesystem (currently 500 MB), and Vercel Hobby Functions have a finite invocation limit (currently 300 seconds). Large repositories may return `413 repo_too_large`; unusually slow GitHub operations may time out. The limits are intentional, observable capacity boundaries, rather than an assertion that unlimited load is possible.

Keep API and Redis in nearby regions. The CDN can absorb repeat README image requests, while Redis reduces clone duplication across instances. `GET /api/status` reports local active/waiting jobs but is **per instance**, so it is not global capacity telemetry. Use Vercel Observability/logs and the Firewall for production traffic and abuse controls. Do not add repository names, tokens, or clone URLs containing credentials to metric labels or logs.

## Safety and future heavy-work path

The public API must not use its deployment token to expose private repositories. On Vercel, `ALLOW_PRIVATE_REPOS` defaults to disabled; the token remains available for GitHub rate-limit fallback and optional PR enrichment. Enabling `ALLOW_PRIVATE_REPOS=true` on a public project would require **real per-visitor authentication and authorization** first. Badge images are inherently publicly readable.

The current on-demand function path supports bounded workloads. To serve very large repositories reliably, route misses to a **durable Vercel Queue** and a **Vercel Sandbox** (or Daemon container) worker with enough disk and time to clone; store the resulting contract in shared Redis/Blob, and let clients poll a typed pending-result state. Implement that path before removing the small function's disk cap. Vercel's Queue/Sandbox/Daemon products have their own availability, concurrency, timeout and cost limits. The existing streaming UI expects terminal `result`/`error` frames, so a polling/queued protocol requires coordinated frontend and backend changes.

## Sources and validation

- [Vercel container-based Functions](https://vercel.com/changelog/bring-your-dockerfile-to-vercel-functions)
- [Vercel Function runtime limits and scratch filesystem](https://vercel.com/docs/functions/runtimes)
- [Vercel Function durations](https://vercel.com/docs/functions/configuring-functions/duration)
- [Vercel Redis Marketplace integrations](https://vercel.com/docs/redis)
- [Upstash REST command API](https://upstash.com/docs/redis/features/restapi)
- [Vercel Queues](https://vercel.com/docs/queues)
- [Vercel Sandbox](https://vercel.com/docs/sandbox)

Local tests verify API behavior and compilation, and the deployed smoke checks verify Git transport and HTTP responses. Monitor real traffic, cold-cache load, provider quotas, and function costs through Vercel and Upstash before increasing exposure.
