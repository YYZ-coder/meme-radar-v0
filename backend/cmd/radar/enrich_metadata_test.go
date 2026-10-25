package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

func TestEnrichRunsMetadataStepWithConfiguredGateway(t *testing.T) {
	ctx := context.Background()
	dbURL := testdb.New(t)
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	address := bytes.Repeat([]byte{6}, 20)
	if err := st.Commit(ctx, store.Commit{Chain: "base", Block: store.Block{Num: 1, Hash: []byte{1}, Time: now, Status: store.StatusConfirmed, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyParsed(ctx, store.Parsed{Chain: "base", Num: 1, Time: now, Version: 1, Facts: parse.Facts{Tokens: []parse.Token{{Address: address, Created: true, TotalSupply: "1000000000000000000", LaunchPlatform: "clanker", URI: "ipfs://QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr"}}}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
metadata:
  ipfs_gateways: [https://configured.example/ipfs/]
  fourmeme_api_url: ''
  rps: 7
  per_host_rps: 1
  fourmeme_rps: 1
chains:
  - name: base
    family: evm
    ingest: {rpc_url: 'https://unused.example', rps: 1, block_interval: 2s, poll: 1s}
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseEnrich([]string{"enrich", "--config=" + path, "--interval=1h"}, env(map[string]string{"RADAR_DATABASE_URL": dbURL}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.metadata.RPS != 7 {
		t.Errorf("enrich lost configured metadata RPS: %+v", cfg.metadata)
	}
	db, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runEnrich(runCtx, cfg, slog.New(slog.NewJSONHandler(io.Discard, nil))) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("enrich shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("enrich did not stop")
		}
	}()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		var image *string
		err := db.QueryRow(ctx, `SELECT image_url FROM token_metadata WHERE chain='base' AND address=$1`, address).Scan(&image)
		if err == nil && image != nil {
			if *image != "https://configured.example/ipfs/QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr" {
				t.Errorf("configured image URI=%q", *image)
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("enrich never persisted its metadata step")
		case <-poll.C:
		}
	}
}
