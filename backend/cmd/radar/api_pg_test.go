package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/testdb"
)

// The command starts on an empty local test database and must migrate before
// serving PostgreSQL health. H2-3 native prices preserve empty database values.
func TestPgAPIStartup(t *testing.T) {
	db := testdb.New(t)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := parseConfig([]string{"api", "--store=pg", "--addr=" + addr}, env(map[string]string{"RADAR_DATABASE_URL": db}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, c, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("API stopped with error: %v", err)
			}
		case <-time.After(6 * time.Second):
			t.Error("API failed to stop after cancellation")
		}
	})
	client := &http.Client{Timeout: time.Second}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err := contracttest.Validate(t, http.MethodGet, "/healthz", resp.StatusCode, data); err != nil {
				t.Fatal(err)
			}
			var health api.Health
			if err := json.Unmarshal(data, &health); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 || health.Status != "ok" || health.DB != "ok" {
				t.Fatalf("started API health = %d %s, want 200 with status=ok, db=ok", resp.StatusCode, data)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("API did not become ready within five seconds")
		case <-poll.C:
		}
	}
	resp, err := client.Get("http://" + addr + "/api/v1/market/natives")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("X-Radar-Mock") != "" {
		t.Fatalf("pg native prices = %d mock=%q, want 200 without mock marker", resp.StatusCode, resp.Header.Get("X-Radar-Mock"))
	}
	var prices api.NativePrices
	if err := json.Unmarshal(data, &prices); err != nil {
		t.Fatal(err)
	}
	if len(prices.Items) != 3 {
		t.Fatalf("empty database native prices = %+v, want three rows", prices.Items)
	}
	for i, chain := range []api.Chain{api.Solana, api.Base, api.BSC} {
		price := prices.Items[i]
		if price.Chain != chain || price.PriceUSD != nil || price.Change24hPct != nil || !price.Stale {
			t.Errorf("empty database native price = %+v, want %s with NULL values and stale=true", price, chain)
		}
	}
	if err := contracttest.Validate(t, http.MethodGet, "/api/v1/market/natives", resp.StatusCode, data); err != nil {
		t.Fatal(err)
	}
	// H2 §2-2 disables the PG admin API when the token is unconfigured;
	// presenting the mock default must not grant access.
	for _, authorization := range []string{"", "Bearer mock-admin"} {
		req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/api/v1/admin/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", authorization)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unconfigured PG admin = %d %s, want 401", resp.StatusCode, body)
		}
		if err := contracttest.Validate(t, http.MethodGet, "/api/v1/admin/health", resp.StatusCode, body); err != nil {
			t.Fatal(err)
		}
		var failure api.ErrorBody
		if err := json.Unmarshal(body, &failure); err != nil {
			t.Fatal(err)
		}
		if failure.Error.Code != "unauthorized" {
			t.Errorf("unconfigured PG admin code = %q, want unauthorized", failure.Error.Code)
		}
	}
	conn, err := pgx.Connect(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var migrated bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Error("API did not apply startup migrations")
	}
}
