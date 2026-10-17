import type { NextConfig } from "next";
import path from "node:path";

// Static export: `next build` emits a fully static ./out that the Go binary
// serves as STATIC_DIR. trailingSlash makes /map resolve to map/index.html from
// a plain file server. Images are unused, so optimization is disabled (the
// export target has no image server).
const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
  // Pin the workspace root to this monorepo so Next does not infer an unrelated
  // parent-directory lockfile as the root.
  turbopack: { root: path.join(import.meta.dirname, "..", "..") },
};

export default nextConfig;
