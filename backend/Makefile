# Backend entry points (backend/AGENTS.md §3). Run from the repository root as
# `make -C backend <target>` or through the root Makefile.
BIN := bin/radar
PKG ?= ./internal/httpapi
BENCH ?= BenchmarkInflowFirstQuery
PROFILE_DIR := ../.local/profile
GO_FILES = $(shell find . -name '*.go' -not -path './bin/*')
# Local PostgreSQL for integration tests and ingest (container meme-radar-app-pg).
DB_PORT ?= 55432
DB_ADMIN_URL ?= postgres://radar:radar@127.0.0.1:$(DB_PORT)/postgres?sslmode=disable
DB_URL ?= postgres://radar:radar@127.0.0.1:$(DB_PORT)/radar?sslmode=disable
CHAIN ?= base

.PHONY: setup fmt fmt-check vet build test test-race test-db db-up db-down ingest run smoke check bench profile
setup:
	go version
	go mod download
	cd .. && ./scripts/install-hooks.sh
fmt:
	gofmt -w $(GO_FILES)
fmt-check:
	@out="$$(gofmt -l $(GO_FILES))"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
vet:
	go vet ./...
build:
	go build ./...
	go build -o $(BIN) ./cmd/radar
test:
	go test -count=1 ./...
test-race:
	go test -race -count=1 ./...
# PostgreSQL integration tests skip unless RADAR_TEST_DATABASE_URL is set;
# test-db starts the local database and runs everything (race-enabled).
test-db: db-up
	RADAR_TEST_DATABASE_URL='$(DB_ADMIN_URL)' go test -race -count=1 ./...
db-up:
	@docker start meme-radar-app-pg >/dev/null 2>&1 || docker run -d --name meme-radar-app-pg \
		-e POSTGRES_USER=radar -e POSTGRES_PASSWORD=radar -e POSTGRES_DB=radar \
		-p 127.0.0.1:$(DB_PORT):5432 -v meme-radar-app-pgdata:/var/lib/postgresql/data \
		--health-cmd 'pg_isready -U radar' --health-interval 2s postgres:17 >/dev/null
	@for i in $$(seq 1 60); do [ "$$(docker inspect -f '{{.State.Health.Status}}' meme-radar-app-pg)" = healthy ] && exit 0; sleep 1; done; \
		echo "meme-radar-app-pg not healthy"; exit 1
db-down:
	docker stop meme-radar-app-pg
# Follow one chain from its free public RPC: make -C backend ingest CHAIN=solana
ingest: build db-up
	RADAR_DATABASE_URL='$(DB_URL)' ./$(BIN) ingest --chain=$(CHAIN)
run: build
	./$(BIN) api --store=mock --addr=$${RADAR_API_ADDR:-127.0.0.1:8080}
smoke: build
	../scripts/backend-smoke.sh $(BIN)
check: fmt-check vet build test smoke
bench:
	go test -run '^$$' -bench '$(BENCH)' -benchmem -count=1 $(PKG)
profile:
	mkdir -p $(PROFILE_DIR)
	go test -run '^$$' -bench '$(BENCH)' -benchmem -count=1 -o $(PROFILE_DIR)/httpapi.test \
		-cpuprofile $(PROFILE_DIR)/cpu.out -memprofile $(PROFILE_DIR)/mem.out $(PKG)
	go tool pprof -top -nodecount=15 $(PROFILE_DIR)/httpapi.test $(PROFILE_DIR)/cpu.out
	go tool pprof -top -nodecount=10 -sample_index=alloc_space $(PROFILE_DIR)/httpapi.test $(PROFILE_DIR)/mem.out
