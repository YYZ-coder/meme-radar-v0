package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/config"
)

func TestDefaultsDescribeTheThreeChains(t *testing.T) {
	c, err := config.Load("")
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	for _, tc := range []struct {
		name, family, rpc string
		poll              time.Duration
	}{
		{"solana", "solana", "https://api.mainnet-beta.solana.com", 400 * time.Millisecond},
		{"base", "evm", "https://mainnet.base.org", 2 * time.Second},
		{"bsc", "evm", "https://bsc-rpc.publicnode.com", 2 * time.Second},
	} {
		ch, ok := c.Chain(tc.name)
		if !ok || ch.Family != tc.family || ch.Ingest.RPCURL != tc.rpc || time.Duration(ch.Ingest.Poll) != tc.poll {
			t.Errorf("Chain(%s) = %+v, %v; want family %s rpc %s poll %s", tc.name, ch, ok, tc.family, tc.rpc, tc.poll)
		}
	}
	if sol, _ := c.Chain("solana"); len(sol.Protocols) != 2 || sol.Protocols[0].Type != "pump_fun" {
		t.Errorf("solana protocols = %+v; want pump_fun and pumpswap", sol.Protocols)
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "radar.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileAddsAChain(t *testing.T) {
	c, err := config.Load(write(t, `
chains:
  - name: arbitrum
    family: evm
    native: ETH
    wrapped_native: "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"
    ingest: {rpc_url: "https://arb1.arbitrum.io/rpc", rps: 5, confirmations: 20, block_interval: 250ms, poll: 1s}
    protocols:
      - type: uniswap_v3
        addresses: {factory: "0x1F98431c8aD98523631AE4a59f267346ea31F984"}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ch, ok := c.Chain("arbitrum")
	if !ok || ch.Ingest.Confirmations != 20 || time.Duration(ch.Ingest.BlockInterval) != 250*time.Millisecond ||
		ch.Protocols[0].Addresses["factory"] != "0x1F98431c8aD98523631AE4a59f267346ea31F984" {
		t.Fatalf("arbitrum = %+v, %v", ch, ok)
	}
	if _, ok := c.Chain("solana"); ok {
		t.Error("a configuration file replaces the defaults; solana should be absent")
	}
}

func TestRejectsInvalidConfiguration(t *testing.T) {
	for name, body := range map[string]string{
		"bad name":         "chains: [{name: 'Arb-1', family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}}]",
		"unknown family":   "chains: [{name: arb, family: cosmos, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}}]",
		"duplicate":        "chains: [{name: arb, family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}}, {name: arb, family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}}]",
		"no rpc url":       "chains: [{name: arb, family: evm, ingest: {rps: 1, block_interval: 1s, poll: 1s}}]",
		"bad duration":     "chains: [{name: arb, family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: soon, poll: 1s}}]",
		"unknown protocol": "chains: [{name: arb, family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}, protocols: [{type: sushiswap}]}]",
		"bad address":      "chains: [{name: arb, family: evm, wrapped_native: '0x12', ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}}]",
		"unknown field":    "chains: [{name: arb, famliy: evm}]",
	} {
		if _, err := config.Load(write(t, body)); err == nil {
			t.Errorf("%s: Load succeeded; want an error", name)
		} else if strings.Contains(err.Error(), "not implemented") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDefaultsConfigureSourcesBackfillAndRetention(t *testing.T) {
	c, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	// Price sources are tried in file order (D147); Pyth stays commented out until a key exists.
	if len(c.Prices.Sources) != 2 || c.Prices.Sources[0].Type != "binance" || c.Prices.Sources[0].URL != "https://api.binance.com" ||
		c.Prices.Sources[1].Type != "coinbase" || c.Prices.Sources[1].URL != "https://api.exchange.coinbase.com" {
		t.Errorf("prices = %+v; want binance then coinbase", c.Prices)
	}
	for _, tc := range []struct {
		chain, url    string
		hours, blkRPS float64
		lookupRPS     float64
	}{
		{"solana", "", 24, 0.1, 2},
		{"base", "", 168, 0, 5},
		{"bsc", "https://bsc-dataseed.bnbchain.org", 168, 0, 10},
	} {
		ch, _ := c.Chain(tc.chain)
		if want := map[string]uint64{"bsc": 6000}[tc.chain]; ch.Ingest.MaxLagBlocks != want {
			t.Errorf("%s ingest.max_lag_blocks = %d; want %d", tc.chain, ch.Ingest.MaxLagBlocks, want)
		}
		b := ch.Backfill
		if b.Hours != tc.hours || b.RPCURL != tc.url || b.BlockRPS != tc.blkRPS || b.RPS <= 0 || ch.LookupRPS != tc.lookupRPS {
			t.Errorf("%s backfill = %+v lookup_rps %v; want %v hours from %q, block_rps %v, lookup_rps %v", tc.chain, b, ch.LookupRPS, tc.hours, tc.url, tc.blkRPS, tc.lookupRPS)
		}
	}
	// Raw blocks, the ledger and facts expire after 30 days (D150); earlier
	// raw deletion is by hand (scripts/prune-raw.sh).
	if c.Retention.RawBlocksDays != 30 || c.Retention.LedgerDays != 30 {
		t.Errorf("retention = %+v; want 30 days for raw blocks and the ledger", c.Retention)
	}
}

func TestPriceSourcesAreValidated(t *testing.T) {
	chain := "chains: [{name: arb, family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}}]\n"
	ok := chain + `prices: {sources: [{type: pyth, url: "https://hermes.pyth.network", api_key_env: RADAR_PYTH_API_KEY, feeds: {ETH: "ff61491a931112ddf1bd8147cd1b641375f79f5825126d665480874634fd0ace"}}]}`
	c, err := config.Load(write(t, ok))
	if err != nil || c.Prices.Sources[0].APIKeyEnv != "RADAR_PYTH_API_KEY" || c.Prices.Sources[0].APIKeyHeader != "x-api-key" {
		t.Errorf("pyth source = %+v, %v; want the key from RADAR_PYTH_API_KEY sent as x-api-key by default", c.Prices, err)
	}
	for name, body := range map[string]string{
		"unknown type":   `prices: {sources: [{type: kraken, url: "https://api.kraken.com"}]}`,
		"no url":         `prices: {sources: [{type: binance}]}`,
		"pyth no feeds":  `prices: {sources: [{type: pyth, url: "https://hermes.pyth.network"}]}`,
		"key in file":    `prices: {sources: [{type: pyth, url: "https://h", api_key: "secret", feeds: {ETH: "ff"}}]}`,
		"backfill url":   "",
		"negative hours": "",
	} {
		full := chain + body
		switch name {
		case "backfill url":
			full = "chains: [{name: arb, family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}, backfill: {hours: 1, rps: 1, rpc_url: 'ws://x'}}]"
		case "negative hours":
			full = "chains: [{name: arb, family: evm, ingest: {rpc_url: 'https://x', rps: 1, block_interval: 1s, poll: 1s}, backfill: {hours: -1, rps: 1}}]"
		}
		if _, err := config.Load(write(t, full)); err == nil {
			t.Errorf("%s: Load succeeded; want an error", name)
		}
	}
}
