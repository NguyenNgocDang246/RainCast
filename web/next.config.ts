import type { NextConfig } from "next";

// The Go backend; requests to /api/* are proxied there so the browser never
// talks to it directly (no CORS needed). In the Docker deploy Caddy routes
// /api before it reaches Next, so this mainly serves `next dev`. Rewrites are
// baked in at build time.
const apiUrl = process.env.API_URL ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  // Self-contained server for the Docker image (web/Dockerfile).
  output: "standalone",
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${apiUrl}/api/:path*` }];
  },
};

export default nextConfig;
