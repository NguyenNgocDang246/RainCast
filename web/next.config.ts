import type { NextConfig } from "next";

// The Go backend for `next dev`: requests to /api/* are proxied there. On
// Vercel the browser calls the backend directly (NEXT_PUBLIC_API_BASE).
// Rewrites are baked in at build time.
const apiUrl = process.env.API_URL ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${apiUrl}/api/:path*` }];
  },
};

export default nextConfig;
