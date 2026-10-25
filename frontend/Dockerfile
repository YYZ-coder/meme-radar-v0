# Next.js production image: next start on :3000, /api/v1 proxied to RADAR_API_ORIGIN.
FROM node:24.19.0-bookworm-slim AS build
ARG NPM_REGISTRY=https://registry.npmjs.org/
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --registry="${NPM_REGISTRY}" --no-audit --no-fund
COPY . .
# The /healthz rewrite is resolved at build time; /api/v1 forwarding reads the runtime value.
ARG RADAR_API_ORIGIN=http://backend:8080
ENV RADAR_API_ORIGIN=${RADAR_API_ORIGIN} NEXT_TELEMETRY_DISABLED=1
RUN npx next build

FROM node:24.19.0-bookworm-slim
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1
WORKDIR /app
COPY --from=build --chown=node:node /app /app
USER node
EXPOSE 3000
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=12 \
  CMD node -e "fetch('http://127.0.0.1:3000/smart-money/inflow').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"
CMD ["npx", "next", "start", "--hostname", "0.0.0.0", "--port", "3000"]
