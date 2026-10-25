import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts");

// API forwarding lives in the route handler so connection failures retain the
// API error contract. The runner binds build and runtime to the same origin.
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
          beforeFiles: [{ source: "/healthz", destination: `${apiOrigin}/healthz` }],
          afterFiles: [],
          fallback: [],
        }
      : { beforeFiles: [], afterFiles: [], fallback: [] };
  },
};

export default withNextIntl(nextConfig);
