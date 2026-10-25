package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseIngestPresets(t *testing.T) {
	db := env(map[string]string{"RADAR_DATABASE_URL": "postgres://radar@127.0.0.1/radar"})
	// Free-tier presets (D144): Solana follows the head, EVM follows in order
	// with 12 confirmations. Gaps are filled by the backfill role, so the
	// forward follower only uses the window (Solana 24h, EVM 7 days) for
	// jumps and its startup gap. Raw blocks and the ledger expire after 30 days.
	const month = 30 * 24 * time.Hour
	for _, tc := range []struct {
		args []string
		want ingestConfig
	}{
		{[]string{"ingest", "--chain=solana"}, ingestConfig{chain: "solana", family: "solana", rpcURL: "https://api.mainnet-beta.solana.com",
			dbURL: "postgres://radar@127.0.0.1/radar", rps: 9, blockRPS: 0.45, followHead: true, backfillBlocks: 216000, skipFill: true,
			rawRetention: month, ledgerRetention: month, poll: 400 * time.Millisecond}},
		{[]string{"ingest", "--chain=base"}, ingestConfig{chain: "base", family: "evm", rpcURL: "https://mainnet.base.org",
			dbURL: "postgres://radar@127.0.0.1/radar", rps: 5, confirmations: 12, reorgDepth: 128, backfillBlocks: 302400, skipFill: true,
			rawRetention: month, ledgerRetention: month, prefetch: 4, poll: 2 * time.Second}},
		{[]string{"ingest", "--chain=bsc"}, ingestConfig{chain: "bsc", family: "evm", rpcURL: "https://bsc-rpc.publicnode.com",
			dbURL: "postgres://radar@127.0.0.1/radar", rps: 16, confirmations: 12, reorgDepth: 128, backfillBlocks: 1344000, maxLag: 6000, skipFill: true,
			rawRetention: month, ledgerRetention: month, prefetch: 10, poll: 2 * time.Second}},
		{[]string{"ingest", "--chain=bsc", "--rpc-url=https://example.org/rpc", "--rps=3", "--prefetch=1"},
			ingestConfig{chain: "bsc", family: "evm", rpcURL: "https://example.org/rpc", dbURL: "postgres://radar@127.0.0.1/radar",
				rps: 3, confirmations: 12, reorgDepth: 128, backfillBlocks: 1344000, maxLag: 6000, skipFill: true, rawRetention: month, ledgerRetention: month, prefetch: 1, poll: 2 * time.Second}},
	} {
		got, err := parseIngest(tc.args, db)
		if err != nil || got != tc.want {
			t.Errorf("parseIngest(%q) = %+v, %v; want %+v", tc.args, got, err, tc.want)
		}
	}
}

func TestParseBackfillUsesTheChainsBackfillSettings(t *testing.T) {
	db := env(map[string]string{"RADAR_DATABASE_URL": "postgres://x"})
	for _, tc := range []struct {
		chain string
		want  backfillConfig
	}{
		{"bsc", backfillConfig{chain: "bsc", family: "evm", rpcURL: "https://bsc-dataseed.bnbchain.org", dbURL: "postgres://x", rps: 6,
			confirmations: 12, window: 1344000, pauseLag: 50, prefetch: 8, poll: 2 * time.Second}},
		{"solana", backfillConfig{chain: "solana", family: "solana", rpcURL: "https://api.mainnet-beta.solana.com", dbURL: "postgres://x", rps: 1,
			blockRPS: 0.1, window: 216000, pauseLag: 50, prefetch: 1, poll: 400 * time.Millisecond}},
	} {
		got, err := parseBackfill([]string{"backfill", "--chain=" + tc.chain}, db)
		if err != nil || got != tc.want {
			t.Errorf("parseBackfill(%s) = %+v, %v; want %+v", tc.chain, got, err, tc.want)
		}
	}
	p := filepath.Join(t.TempDir(), "radar.yaml")
	os.WriteFile(p, []byte("chains: [{name: arb, family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}}]\n"), 0o600)
	for _, args := range [][]string{{"backfill"}, {"backfill", "--chain=arb", "--config=" + p}} { // no backfill section
		if _, err := parseBackfill(args, db); err == nil {
			t.Errorf("parseBackfill(%q) succeeded; want error", args)
		}
	}
}

func TestParsePruneRaw(t *testing.T) {
	db := env(map[string]string{"RADAR_DATABASE_URL": "postgres://x"})
	got, err := parsePruneRaw([]string{"prune-raw", "--before=2026-10-01", "--chain=bsc"}, db)
	if err != nil || got.chain != "bsc" || !got.before.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || got.dbURL != "postgres://x" {
		t.Errorf("parsePruneRaw = %+v, %v", got, err)
	}
	for _, args := range [][]string{{"prune-raw"}, {"prune-raw", "--before=yesterday"}, {"prune-raw", "--before=2026-10-01", "--chain=eth"}} {
		if _, err := parsePruneRaw(args, db); err == nil {
			t.Errorf("parsePruneRaw(%q) succeeded; want error", args)
		}
	}
}

func TestParseIngestRejectsBadInput(t *testing.T) {
	db := env(map[string]string{"RADAR_DATABASE_URL": "postgres://x"})
	for _, tc := range []struct {
		args []string
		env  func(string) string
	}{
		{[]string{"ingest", "--chain=solana"}, env(nil)}, // no database URL
		{[]string{"ingest"}, db},
		{[]string{"ingest", "--chain=eth"}, db},
		{[]string{"ingest", "--chain=base", "--backfill-hours=1"}, db}, // the backfill role owns gap filling
		{[]string{"ingest", "--chain=base", "--rpc-url=ftp://x"}, db},
		{[]string{"ingest", "--chain=base", "--rps=0"}, db},
		{[]string{"ingest", "--chain=base", "--debug-addr=0.0.0.0:6060"}, db},
		{[]string{"ingest", "--chain=base", "--prefetch=0"}, db},
		{[]string{"ingest", "--chain=base", "--prefetch=65"}, db},
	} {
		if _, err := parseIngest(tc.args, tc.env); err == nil {
			t.Errorf("parseIngest(%q) succeeded; want error", tc.args)
		}
	}
}

func TestParseIngestChainFromConfigFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "radar.yaml")
	os.WriteFile(p, []byte(`
chains:
  - name: arbitrum
    family: evm
    ingest: {rpc_url: "https://arb1.arbitrum.io/rpc", rps: 5, confirmations: 20, reorg_depth: 64, prefetch: 2, block_interval: 250ms, poll: 1s}
    backfill: {hours: 1, rps: 1}
`), 0o600)
	db := env(map[string]string{"RADAR_DATABASE_URL": "postgres://x", "RADAR_CONFIG": p})
	got, err := parseIngest([]string{"ingest", "--chain=arbitrum"}, db)
	want := ingestConfig{chain: "arbitrum", family: "evm", rpcURL: "https://arb1.arbitrum.io/rpc", dbURL: "postgres://x", rps: 5,
		confirmations: 20, reorgDepth: 64, backfillBlocks: 14400, skipFill: true, prefetch: 2, poll: time.Second}
	if err != nil || got != want {
		t.Fatalf("parseIngest = %+v, %v; want %+v", got, err, want)
	}
	if _, err := parseIngest([]string{"ingest", "--chain=solana"}, db); err == nil {
		t.Error("solana accepted although the configuration file does not define it")
	}
	if _, err := parseIngest([]string{"ingest", "--chain=arbitrum", "--config=/nonexistent.yaml"}, db); err == nil {
		t.Error("missing --config file accepted")
	}
}
