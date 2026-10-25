// Package testdb creates throwaway PostgreSQL databases for integration tests.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// EnvURL names the variable holding an admin connection URL of the test
// server, e.g. postgres://radar:radar@127.0.0.1:55432/postgres.
const EnvURL = "RADAR_TEST_DATABASE_URL"

var seq atomic.Int64

// New creates an empty database, returns its connection URL and drops it when
// the test ends. It skips the test when EnvURL is unset.
func New(t testing.TB) string {
	t.Helper()
	admin := os.Getenv(EnvURL)
	if admin == "" {
		t.Skipf("%s is unset; skipping PostgreSQL integration test", EnvURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect %s: %v", EnvURL, err)
	}
	defer conn.Close(ctx)
	name := fmt.Sprintf("radar_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), seq.Add(1))
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Errorf("drop database %s: %v", name, err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvURL, err)
	}
	u.Path = "/" + name
	return u.String()
}
