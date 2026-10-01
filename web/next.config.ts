import type { NextConfig } from "next";

// The Go backend; requests to /api/* are proxied there so the browser never
// talks to it directly (no CORS needed).
const apiUrl = process.env.API_URL ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${apiUrl}/api/:path*` }];
  },
};

export default nextConfig;
