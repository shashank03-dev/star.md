# syntax=docker/dockerfile:1
# Single-image build: the Go binary serves the statically-exported Next.js
# frontend AND the /api. Pure-Go SQLite (modernc.org/sqlite) means the server
# needs no CGO, so it ships as a static binary on a distroless base.

# 1) Build the static frontend -> apps/web/out
FROM node:24-alpine AS web
RUN corepack enable
WORKDIR /repo
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY packages ./packages
COPY apps/web ./apps/web
RUN pnpm install --frozen-lockfile
RUN pnpm --filter web build

# 2) Build the Go server as a static binary
FROM golang:1.26-alpine AS server
WORKDIR /src
COPY apps/server/go.mod apps/server/go.sum ./
RUN go mod download
COPY apps/server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /stellar .

# 3) Minimal runtime. The history now comes from `git clone --bare
#    --filter=tree:0`, so the runtime needs a real git binary and CA certs -
#    that rules out distroless/static. Alpine + git is the smallest base that
#    still runs the (statically linked, CGO-free) server.
FROM alpine:3
RUN apk add --no-cache git ca-certificates \
 && adduser -D -u 65532 -h /home/nonroot nonroot \
 && mkdir -p /data /mirrors \
 && chown 65532:65532 /data /mirrors
WORKDIR /app
COPY --from=server /stellar /stellar
COPY --from=web /repo/apps/web/out /app/web
USER 65532:65532
ENV STATIC_DIR=/app/web \
    PORT=8770 \
    CACHE_DB=/data/stellar-cache.db \
    CLONE_DIR=/mirrors
EXPOSE 8770
# No token is required: public repos are cloned anonymously over the git
# transport, which is a separate budget from the REST API's 5,000 req/hr.
# GITHUB_TOKEN (or GITHUB_TOKENS, comma-separated) is only spent on private-repo
# auth and on merged-PR lookups for repos whose history has no PR references.
# Both /data (contract cache) and /mirrors (bare clones) are best-effort caches;
# mount volumes to keep them warm across restarts. The mirror cache evicts
# itself down to CLONE_DISK_MB, so an ephemeral /mirrors is also fine.
ENTRYPOINT ["/stellar"]
