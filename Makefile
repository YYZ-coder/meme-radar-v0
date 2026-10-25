# Root entry points. Backend: Go mock API (backend/); frontend: Next.js (frontend/).
TASK ?= check
# Loopback must bypass any HTTP(S)_PROXY set on the machine: proxied 127.0.0.1
# requests gave empty replies and slowed Playwright's server checks (2026-10-24).
export NO_PROXY := 127.0.0.1,localhost,$(NO_PROXY)
export no_proxy := 127.0.0.1,localhost,$(no_proxy)
.PHONY: all setup fmt fmt-check check check-race dev dev-connected start smoke smoke-connected backend frontend up down logs ingest-up ingest-down ingest-logs obs-up obs-down alert-test
all: check
setup:
	./scripts/install-hooks.sh
	cd backend && go mod download
	cd frontend && npm run setup
fmt:
	$(MAKE) -C backend fmt
	cd frontend && npm run format
fmt-check:
	$(MAKE) -C backend fmt-check
	cd frontend && npm run format:check
# Backend gate, frontend gate (mock), then the frontend smoke suite against the Go API.
check:
	$(MAKE) -C backend check
	cd frontend && npm run check
	$(MAKE) smoke-connected
check-race:
	$(MAKE) -C backend test-race
# Frontend dev server against its built-in mock.
dev:
	cd frontend && npm run dev
# Go mock API on :8080 plus the frontend dev server proxying /api/v1 to it.
dev-connected:
	./scripts/dev-connected.sh
start:
	cd frontend && npm run build && npm run start
smoke:
	cd frontend && npm run build && npm run test:smoke
smoke-connected:
	$(MAKE) -C backend build
	cd frontend && npm run build && npm run test:connected
backend:
	$(MAKE) -C backend $(TASK)
frontend:
	cd frontend && npm run $(TASK)
# Containers: backend and frontend as separate compose services.
up:
	docker compose up -d --build --wait
down:
	docker compose down
logs:
	docker compose logs -f
# Ingest containers (one per chain) and the Solana parser against RADAR_DATABASE_URL from .env.
ingest-up:
	@test -f .env || { echo "missing .env: copy .env.example and set RADAR_DATABASE_URL"; exit 1; }
	docker compose --profile ingest up -d --build api ingest-solana ingest-base ingest-bsc parse-solana parse-bsc parse-base compute enrich backfill-solana backfill-base backfill-bsc
ingest-down:
	docker compose --profile ingest stop api ingest-solana ingest-base ingest-bsc parse-solana parse-bsc parse-base compute enrich backfill-solana backfill-base backfill-bsc
ingest-logs:
	docker compose --profile ingest logs -f --tail=50 api ingest-solana ingest-base ingest-bsc parse-solana parse-bsc parse-base compute enrich backfill-solana backfill-base backfill-bsc
# Prometheus + Loki + Alloy + Grafana (http://127.0.0.1:3301).
obs-up:
	docker compose --profile observability up -d prometheus alertmanager loki alloy grafana
obs-down:
	docker compose --profile observability stop prometheus alertmanager loki alloy grafana
# Reload the SMTP settings from .env and send a test alert email (arrives after ~30s).
alert-test:
	docker compose --profile observability up -d --force-recreate alertmanager
	@for i in $$(seq 1 30); do docker compose --profile observability exec -T alertmanager wget -qO- http://localhost:9093/-/ready >/dev/null 2>&1 && break; sleep 2; done
	docker compose --profile observability exec -T alertmanager amtool alert add RadarTestAlert severity=info service=alertmanager \
		--annotation=summary="meme-radar test alert: email delivery works" --alertmanager.url=http://localhost:9093
