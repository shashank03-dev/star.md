# Vercel frontend deployment

This Vercel project hosts the **static Next.js frontend**. A second Vercel
project hosts the Go API as a container Function on a separate HTTPS origin.
The existing combined local Docker setup remains available.
Current production: [Stellar website](https://stellar-web-taupe.vercel.app/)
and [Go API](https://stellar-api-shashank03-devs-projects.vercel.app/readyz).
The frontend's `apps/web/next.config.ts` already uses `output: "export"`, so
`pnpm build` writes static files to `apps/web/out/`. The app does not need a
Next.js server or Vercel Functions at runtime.

## Configure the Vercel project

Import this Git repository into a new Vercel project with these settings:

| Setting | Value |
| --- | --- |
| Root Directory | `apps/web` |
| Framework Preset | **Other** (`apps/web/vercel.json` sets `framework: null`) |
| Build Command | `pnpm build` |
| Output Directory | `out` (relative to `apps/web`) |
| Install Command | **Automatic** (use the root pnpm workspace and lockfile) |
| Node.js Version | **24.x** |

Enable **Include source files outside of the Root Directory in the Build Step**
under the Root Directory setting. This is needed for the repo-level
`pnpm-lock.yaml` and `packages/contract` (`@stellar/contract` is a workspace
dependency). The repository pins pnpm `11.18.0` in the root `package.json`;
allow Vercel to detect the package manager. The checked-in
`apps/web/vercel.json` contains the build, output, and trailing-slash settings,
so avoid overriding those with conflicting dashboard values. You do not need
to set a Vercel API route, rewrite, or proxy.

Under **Project Settings → Environment Variables**, add:

| Name | Value | Environments |
| --- | --- | --- |
| `NEXT_PUBLIC_API_BASE` | `https://stellar-api-shashank03-devs-projects.vercel.app` for this deployment | **Production** and **Preview** (and other build targets you use) |

Set the value to the HTTPS **origin only**: no `/api`, path, query string, or
trailing `/`. The static client already adds `/api/starmap` or
`/api/starmap/stream` and the `?repo=owner/name` query. Because Next.js inlines
`NEXT_PUBLIC_` variables into browser bundles, configure this **before** the
build and redeploy after changing it. It is public configuration, not a secret.
`next.config.ts` fails a Vercel build if this origin is missing, not HTTPS, or
contains a trailing slash/path. Leave it unset when building the existing
same-origin Docker deployment outside Vercel.

## External API requirements

Deploy the Go API as the second Vercel project described in
[`VERCEL-ARCHITECTURE.md`](VERCEL-ARCHITECTURE.md), reachable publicly over
HTTPS. It must provide `GET /api/starmap` (JSON) and
`GET /api/starmap/stream` (flushed NDJSON). The current Go handlers already
return `Access-Control-Allow-Origin: *` on both endpoints for browser GETs;
ensure any CDN or reverse proxy preserves that header and does not buffer the
stream. Browsers on an HTTPS Vercel site cannot fetch an HTTP API (mixed
content). This static frontend project runs no Git clones or Go process; the
separate API Vercel project needs Upstash Redis for shared cached contracts.

The landing's hero/legend use a synthesized demo contract and do not contact
the API. Visiting `/map/?repo=octocat/Hello-World` loads the real repository
through the cross-origin progress stream. Static export generates
`out/index.html` and `out/map/index.html`; the trailing slash setting keeps
`/map/` URLs working, including query parameters. The backend implements a
**live README image embed** at `/api/badge` (SVG) and `/api/badge.gif` (animated
GIF). It becomes available after the API Vercel project is deployed and working.

## Verify before and after deployment

Run from the repository root with a real API origin:

```sh
NEXT_PUBLIC_API_BASE=https://api.example.com pnpm build
```

The build should emit `apps/web/out/index.html` and
`apps/web/out/map/index.html`. After deploying, open `/` and
`/map/?repo=octocat/Hello-World` on the Vercel domain. In browser Developer
Tools → Network, confirm that the map fetches
`https://api.example.com/api/starmap/stream?repo=octocat%2FHello-World`,
receives NDJSON progress and a terminal `result`, and displays the 3D map.
To confirm the API supports the frontend origin independently:

```sh
curl -i -H 'Origin: https://your-site.vercel.app' \
  'https://api.example.com/api/starmap?repo=octocat/Hello-World'
curl -N -H 'Origin: https://your-site.vercel.app' \
  'https://api.example.com/api/starmap/stream?repo=octocat/Hello-World'
```

Replace both example hostnames. The first response needs a compatible
`Access-Control-Allow-Origin` header; the second should flush newline-separated
events without proxy buffering. Errors in those cross-origin requests need to
be fixed at the API host or proxy, rather than in the Vercel static frontend.
