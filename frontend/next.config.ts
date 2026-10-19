import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts");

// Same-origin /api/v1 (14-frontend §3). With RADAR_API_ORIGIN set at build time the
// requests are proxied to the Go API; otherwise the built-in mock route answers.
const apiOrigin = process.env.RADAR_API_ORIGIN?.replace(/\/$/, "");

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // Keep frontend/AGENTS.md as the project's own conventions file (next dev would append to it).
  agentRules: false,
  async redirects() {
    return [{ source: "/admin", destination: "/admin/health", permanent: false }];
  },
  async rewrites() {
    return apiOrigin
      ? {
          beforeFiles: [
            { source: "/api/v1/:path*", destination: `${apiOrigin}/api/v1/:path*` },
            { source: "/healthz", destination: `${apiOrigin}/healthz` },
          ],
          afterFiles: [],
          fallback: [],
        }
      : { beforeFiles: [], afterFiles: [], fallback: [] };
  },
};

export default withNextIntl(nextConfig);
