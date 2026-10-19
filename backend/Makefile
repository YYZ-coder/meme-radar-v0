BIN_DIR := bin
BIN     := $(BIN_DIR)/radar

GOFILES := $(shell find . -name '*.go' -not -path './vendor/*')
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: setup fmt fmt-check vet lint-go build test test-race run smoke check bench profile

setup:
	@command -v go >/dev/null 2>&1 || { echo "ERROR: go not found"; exit 1; }
	go mod download
	@cd .. && git config core.hooksPath .githooks 2>/dev/null || true
	@echo "Backend setup complete."

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@DIFF=$$(gofmt -l $(GOFILES)); \
	if [ -n "$$DIFF" ]; then echo "Unformatted Go files:"; echo "$$DIFF"; exit 1; fi

vet:
	go vet ./...

# 18-observability §4: goroutines must start via obs.Go/Loop/Supervise (trace + panic guard).
lint-go:
	@BAD=$$(grep -rnE '^[[:space:]]*go[[:space:]]+(func|[A-Za-z_][A-Za-z0-9_.]*\()' --include='*.go' . \
		| grep -v '^./internal/obs/' | grep -v '_test.go:'); \
	if [ -n "$$BAD" ]; then echo "Bare go statements (use obs.Go/Loop/Supervise):"; echo "$$BAD"; exit 1; fi

build: $(BIN)

$(BIN): $(GOFILES) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/radar

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

run: build
	RADAR_LOG_FILE=$${RADAR_LOG_FILE:-../.local/logs/radar.log} ./$(BIN) --config config.yaml

# Smoke: start postgres via docker compose, run the binary, verify /healthz, stop.
smoke: build
	@echo "==> smoke: starting postgres"
	@docker compose -f ../docker-compose.yml up -d postgres
	@echo "==> smoke: waiting for postgres to be ready"
	@for i in $$(seq 1 30); do \
		docker compose -f ../docker-compose.yml exec -T postgres \
			pg_isready -U radar -q && break || sleep 1; \
	done
	@echo "==> smoke: starting radar"
	@RADAR_DATABASE_URL="postgres://radar:radar@localhost:5433/radar?sslmode=disable" \
	 RADAR_HTTP_ADDR=":18080" \
	 ./$(BIN) --config config.example.yaml & echo $$! > /tmp/radar-smoke.pid
	@echo "==> smoke: waiting for /healthz"
	@for i in $$(seq 1 30); do \
		curl --noproxy '*' -sf http://localhost:18080/healthz >/dev/null && break || sleep 1; \
	done
	@echo "==> smoke: checking /healthz"
	@curl --noproxy '*' -sf http://localhost:18080/healthz | grep '"status":"ok"'
	@echo ""
	@echo "==> smoke: stopping radar"
	@kill $$(cat /tmp/radar-smoke.pid) 2>/dev/null || true
	@rm -f /tmp/radar-smoke.pid
	@echo "==> smoke: PASSED"

check: fmt-check vet lint-go build test smoke

bench:
	go test -bench=. -benchmem ./...

profile:
	@echo "Usage: go test -cpuprofile=cpu.prof -memprofile=mem.prof -bench=. ./internal/httpapi/..."
	@echo "Then:  go tool pprof -top cpu.prof"
