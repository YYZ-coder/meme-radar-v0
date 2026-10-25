package main

import (
	"strings"
	"testing"
)

// H2 §2-2: PostgreSQL requires a URL and never inherits the mock admin token.
func TestParseConfigAcceptsPgStore(t *testing.T) {
	const db = "postgres://localhost/radar_test"
	t.Run("pg", func(t *testing.T) {
		c, err := parseConfig([]string{"api", "--store=pg"}, env(map[string]string{"RADAR_DATABASE_URL": db}))
		if err != nil {
			t.Fatal(err)
		}
		if c.store != "pg" || c.dbURL != db || c.adminToken != "" {
			t.Errorf("store=%q URL configured=%t admin=%q, want pg, configured, empty", c.store, c.dbURL == db, c.adminToken)
		}
	})
	t.Run("missing_url", func(t *testing.T) {
		_, err := parseConfig([]string{"api", "--store=pg"}, env(nil))
		if err == nil || !strings.Contains(err.Error(), "RADAR_DATABASE_URL") {
			t.Errorf("missing URL error = %v, want RADAR_DATABASE_URL required", err)
		}
	})
	t.Run("explicit_admin", func(t *testing.T) {
		c, err := parseConfig([]string{"api", "--store=pg", "--addr=127.0.0.1:8081"}, env(map[string]string{"RADAR_DATABASE_URL": db, "RADAR_ADMIN_TOKEN": "test-admin"}))
		if err != nil {
			t.Fatal(err)
		}
		if c.addr != "127.0.0.1:8081" || c.adminToken != "test-admin" {
			t.Errorf("pg explicit addr/admin = %q/%q", c.addr, c.adminToken)
		}
	})
	t.Run("mock_unchanged", func(t *testing.T) {
		c, err := parseConfig([]string{"api", "--store=mock"}, env(nil))
		if err != nil {
			t.Fatal(err)
		}
		if c.adminToken != "mock-admin" || c.dbURL != "" {
			t.Errorf("mock defaults = token %q, URL configured %t", c.adminToken, c.dbURL != "")
		}
	})
}
