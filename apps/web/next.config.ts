import type { NextConfig } from "next";
import path from "node:path";

// A Vercel build publishes only static files. The map's browser requests must
// point at the separately hosted API, not at the Vercel frontend origin.
// NEXT_PUBLIC_API_BASE is inlined by Next.js at build time, so reject a Vercel
// build that would bake in a missing or malformed origin. Local/same-origin Go
// builds can still leave it unset.
if (process.env.VERCEL === "1") {
  const apiBase = process.env.NEXT_PUBLIC_API_BASE;
  let valid = false;
  try {
    const url = new URL(apiBase ?? "");
    valid = url.protocol === "https:" && url.origin === apiBase;
  } catch {
    // A missing or malformed value is reported by the error below.
  }
  if (!valid) {
    throw new Error(
      "Vercel builds require NEXT_PUBLIC_API_BASE to be an HTTPS API origin " +
        "without a trailing slash or path (e.g. https://api.example.com).",
    );
  }
}

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
