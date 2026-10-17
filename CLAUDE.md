# Stellar repository guidance

Stellar (`star.md`) maps real GitHub repository history to a deterministic animated 3D star map. This file is guidance for contributors and coding agents; the architectural and visual sources of truth are `AGENTS.md`, `DESIGN-LOCK.md`, and `docs/BACKEND-DESIGN-REVIEW.md`.

## Current scope and decision boundaries

- The priority is backend correctness, scalability review, and documentation. Do **not** redesign the landing page or the locked star-map appearance.
- Preserve the `Contract` fields in `packages/contract/index.ts` and their Go counterparts in `apps/server/transform.go`. Changes to contract semantics require Go and frontend compatibility checks plus a cache `paramsVersion` bump.
- The approved Vercel deployment uses two projects (Go container Function and static web) with Upstash Redis for shared contracts and distributed build leases. Surface decisions that would add paid durable workers or alter frontend/backend API semantics.
- Work directly on `main` and push to `origin/main` when authorized; do not open pull requests. Use the repository's configured author identity, and never rewrite existing history or fabricate authorship.

## Components

| Path | Responsibility |
| --- | --- |
| `apps/server/main.go` | HTTP routing, NDJSON progress stream, orchestration |
| `apps/server/gitsource.go` | Anonymous-first Git probes/clones, bounded mirror cache, snapshot parsing |
| `apps/server/github.go` | Optional GitHub REST PR metadata and token pool |
| `apps/server/transform.go` | Repo snapshot to shared visual Contract, star sampling |
| `apps/server/cache.go` | Best-effort SQLite contract cache and TTL migration |
| `apps/server/rediscache.go`, `buildlease.go` | Shared Upstash REST contract cache and cross-instance lease coordination |
| `apps/server/badge.go`, `galaxygif.go` | GitHub README SVG and animated GIF derived from the same Contract |
| `apps/server/*_test.go` | Backend regression tests; add a failing test before fixing an observed bug |
| `packages/contract/index.ts` | TypeScript schema used by the renderer/client |
| `apps/web/lib/starmap/stream.ts` | Frontend progress/error protocol reader |
| `apps/web/lib/starmap/renderer.ts` | Locked Three.js renderer; leave the visuals alone |
| `Dockerfile`, `docker-compose.yml` | One-container production shape and local persistent volumes |
| `apps/server/Dockerfile.vercel`, `apps/web/vercel.json` | Independent Vercel API and frontend deployment |

Data flow: `GET /api/starmap[ /stream]?repo=owner/name` → memoized `git ls-remote` → Redis (Vercel) or SQLite (local) cache hit or bounded partial clone/fetch → history parse + optional REST PRs → deterministic `Contract` → renderer or badge. Cold Vercel misses acquire a Redis lease before cloning; oversized or overloaded requests fail with typed errors.

## Backend invariants

1. Only pinned GitHub HTTPS URLs may be cloned; user-provided repo names must be validated, and Git credentials must remain out of command arguments, URLs, logs, and committed files.
2. Public Git history works anonymously even with a stale token. Use a token only for auth fallback or optional merged-PR REST metadata.
3. Acquire the mirror gate before touching a mirror and release it after parsing; the sweep must never remove a mirror held by a build.
4. Enforce build time/size/concurrency budgets. If any Git subprocess fails (including after partial output), do not emit or cache a partial contract.
5. Genesis is first with `birthT = 0`; HEAD is pinned to 1 only for histories with multiple commits; `branches` serializes as `[]` when empty; output is stable for the same snapshot.
6. Cache keys include repository, HEAD SHA, and the transform version. Contract TTL is 15 minutes, so unchanged-HEAD tag and side-branch changes may lag. Local cache is best-effort; production leases prevent concurrent Redis cache failures from stampeding GitHub.
7. A started NDJSON stream reports terminal failures in-band (`phase: "error"`); a successful result embeds the complete contract. Preserve the frontend reader's assumptions.
8. SQLite and mirror caches are local-only state. Vercel stores reusable contracts in Upstash Redis, uses ephemeral Git mirrors, and never shares SQLite WAL volumes across instances.
9. Public badges must not expose private repository data. Public Vercel deployments keep `ALLOW_PRIVATE_REPOS` disabled and do not log credentials. Set Vercel's `PORT=8080` for the unprivileged Go container.

## Commands

```bash
# From repository root
pnpm install --frozen-lockfile
pnpm build
pnpm start:server

go -C apps/server test -race -count=1 ./...
go -C apps/server vet ./...
go -C apps/server build ./...
docker compose up --build
```

Public repositories require no token. Configure `GITHUB_TOKEN` explicitly for private access. `STATIC_DIR=../web/out` is set by `pnpm start:server` when it runs from `apps/server`; Docker sets an absolute static path.

## Backend design review status

`docs/BACKEND-DESIGN-REVIEW.md` documents the prior bug fixes and system-design findings; `docs/BACKEND-VALIDATION-2026-10-17.md` records the original validation pass. `docs/VERCEL-ARCHITECTURE.md` describes the newer two-project design, implemented queue bounds, leases, README embeds, operational limits, and outstanding deployment work. Large repository processing via durable queues remains future work.

## Contribution routine

Read the relevant Go module and tests; reproduce bugs in regression tests; make targeted fixes without unrelated frontend edits; run race tests, vet, and build; verify the diff and commit using the repository's configured identity. Keep README, server README, and backend review notes truthful about current behavior. Avoid PRs and feature branches unless the user changes the workflow.
