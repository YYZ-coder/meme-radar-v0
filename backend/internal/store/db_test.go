package store_test

import (
	"os"
	"testing"

	"github.com/meme-radar/radar/internal/store"
)

// TestOpen_and_Migrate is an integration test that requires a real PostgreSQL
// instance. Set RADAR_TEST_DB_URL to run it, e.g.:
//
//	RADAR_TEST_DB_URL=postgres://radar:radar@localhost:5432/radar_test?sslmode=disable \
//	    go test ./internal/store/...
func TestOpen_and_Migrate(t *testing.T) {
	dsn := os.Getenv("RADAR_TEST_DB_URL")
	if dsn == "" {
		t.Skip("RADAR_TEST_DB_URL not set; skipping integration test")
	}

	db, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	// Must be idempotent: running again must not return an error.
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate() second run error = %v (migrations must be idempotent)", err)
	}
}

func TestOpen_badDSN(t *testing.T) {
	_, err := store.Open("postgres://invalid:invalid@localhost:9999/nodb?sslmode=disable&connect_timeout=1")
	if err == nil {
		t.Fatal("Open() error = nil, want non-nil for unreachable host")
	}
}
