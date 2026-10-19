ROOT := $(shell pwd)

.PHONY: all setup fmt fmt-check check dev smoke obs-up obs-down

all: check

setup:
	$(MAKE) -C backend setup
	npm --prefix frontend run setup

fmt:
	$(MAKE) -C backend fmt
	npm --prefix frontend run format

fmt-check:
	$(MAKE) -C backend fmt-check
	npm --prefix frontend run format:check

check:
	$(MAKE) -C backend check
	npm --prefix frontend run check

dev:
	@echo "Start backend: make -C backend run"
	@echo "Start frontend: npm --prefix frontend run dev"

smoke:
	$(MAKE) -C backend smoke
	npm --prefix frontend run test:smoke

# Grafana + Loki + Alloy for logs: http://localhost:$${GRAFANA_PORT:-3300} (dashboard "Radar logs").
obs-up:
	mkdir -p .local/logs
	docker compose --profile observability up -d loki alloy grafana

obs-down:
	docker compose --profile observability stop loki alloy grafana
