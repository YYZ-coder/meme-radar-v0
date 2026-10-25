# Meme Radar Frontend

**English** | [简体中文](README.zh-CN.md)

A pixel-style, multi-chain smart-money terminal built with Next.js 16 App Router, React 19 and TypeScript. See the [technical overview](../TECHNICAL_OVERVIEW.md) for architecture, data flows and consistency boundaries, [AGENTS.md](AGENTS.md) for development conventions, and [api/openapi.yaml](../api/openapi.yaml) for the API contract.

## Running

Requirements: Node 24 LTS, npm 11 and Chromium for browser tests.

```sh
npm run setup   # npm ci + install Chromium
npm run dev     # Development server at 127.0.0.1:3000; built-in mock by default
```

| Environment variable     | Purpose                                                                                                                                   |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `RADAR_API_ORIGIN`       | Forward `/api/v1/*` and `/healthz` to the specified Go API. An unreachable backend returns 503 with a trace; there is no fallback to mock |
| `RADAR_API_MOCK=0`       | Explicitly disable mock when no origin is set; all API requests return 503                                                                |
| `RADAR_MOCK_ADMIN_TOKEN` | Mock admin token; defaults to `mock-admin`                                                                                                |
| `RADAR_E2E_PORT`         | Production-server port for browser smoke tests; defaults to 31337, within 31000–31999                                                     |

To connect to the Go backend, run `make dev-connected` from the repository root. It starts the Go mock API on port 8080 and the frontend development server on port 3000. `npm run test:connected` uses Playwright to start both `../backend/bin/radar` (build it first with `make -C ../backend build`) and the current frontend production build, then runs the same smoke suite. `RADAR_E2E_API_PORT` defaults to 31437.

## Pages

| Route                                              | Contents                                                                                                                                                                                                                                                                                         | Milestone |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------- |
| `/smart-money/inflow`                              | Inflow overview cards; chain/window/DEX/platform filters stored in the URL; sorting; infinite scroll with `query_id` and automatic requery on 410; visible-row refresh every 10 seconds; watchlist via `?watch=1`                                                                                | M1        |
| `/token/{chain}/{address}`                         | Header; chart with six intervals, price/market cap, USD/native units, MA/EMA/BOLL/VOL/MACD/RSI, smart-money buy/sell markers, screenshots and fullscreen; activities; holders with coverage; liquidity pools/changes; market summary; smart-money holdings; developer; tokens with the same name | M1        |
| `/admin/health`, `/admin/import`, `/admin/members` | Operational health, list import with validation and confirmation, member and exclusion management                                                                                                                                                                                                | M1        |
| `/smart-money/signals`, `/smart-money/leaderboard` | Placeholders for M3 and M2, respectively                                                                                                                                                                                                                                                         | —         |

Global features include search (`/` to focus), English/Chinese selection stored in a cookie, quote-unit and reduced-motion settings, and major-asset prices in the footer.

## Validation

```sh
npm run check       # format → lint → api:check → typecheck → test → build → test:smoke
npm run test:smoke  # Build first; Playwright starts this production build (reuseExistingServer: false)
npm run test:e2e    # Build, then run all browser cases
npm run api:types   # Regenerate types after changing ../api/openapi.yaml
```

Production smoke tests cover key desktop/mobile flows, recovery after refreshing during list preparation, activation, exclusion/restoration, and pausing old-list value refreshes before an explicit requery. Reports are written to `playwright-report/`; failure screenshots are in `test-results/`. Browsers other than Chromium have not been accepted.
