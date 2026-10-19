// Package store provides the PostgreSQL connection pool and database migrations.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver registered as "pgx"
	"github.com/pressly/goose/v3"

	"github.com/meme-radar/radar/migrations"
)

// Open opens a PostgreSQL connection pool using the given DSN and verifies
// connectivity with a ping. The caller is responsible for calling db.Close().
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return db, nil
}

// Migrate runs all pending goose migrations from the embedded migrations package.
// It is idempotent: running it on an already-migrated database is a no-op.
func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose set dialect: %w", err)
	}
	// "." is the root of migrations.Files, which contains all *.sql files.
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
