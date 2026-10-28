package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/config"
)

func TestPrivateRPCIsPreferredAndPublicRetained(t *testing.T) {
	t.Setenv("RADAR_TEST_PRIVATE_RPC_URL", "https://private.example/credential/")
	cfg, err := config.Load(write(t, `
chains:
  - name: base
    family: evm
    rpc_url_env: RADAR_TEST_PRIVATE_RPC_URL
    ingest: {rpc_url: "https://mainnet.base.org", rps: 5, block_interval: 2s, poll: 1s}
`))
	if err != nil {
		t.Fatal(err)
	}
	chain, ok := cfg.Chain("base")
	if !ok {
		t.Fatal("configured base chain missing")
	}
	want := []string{"https://private.example/credential/", "https://mainnet.base.org"}
	if got := chain.RPCURLs(""); !reflect.DeepEqual(got, want) {
		t.Fatalf("RPCURLs = %v; want private endpoint first with public fallback %v", got, want)
	}
}

func TestPrivateRPCRejectsInvalidURLWithoutLeakingCredential(t *testing.T) {
	t.Setenv("RADAR_TEST_PRIVATE_RPC_URL", "wss://private.example/not-for-logs")
	_, err := config.Load(write(t, `
chains:
  - name: base
    family: evm
    rpc_url_env: RADAR_TEST_PRIVATE_RPC_URL
    ingest: {rpc_url: "https://mainnet.base.org", rps: 5, block_interval: 2s, poll: 1s}
`))
	if err == nil {
		t.Fatal("Load accepted non-HTTP private RPC; want a configuration error")
	}
	if strings.Contains(err.Error(), "not-for-logs") {
		t.Fatal("configuration error leaked the private endpoint credential")
	}
}

func TestEmbeddedDefaultsUsePrivateRPCAndRetainRolePublicSources(t *testing.T) {
	for _, chain := range []string{"SOLANA", "BASE", "BSC"} {
		t.Setenv("QUICKNODE_"+chain+"_RPC_URL", "https://private.example/"+strings.ToLower(chain)+"/credential/")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ chain, ingest, lookup, backfill string }{
		{"solana", "https://api.mainnet-beta.solana.com", "https://api.mainnet-beta.solana.com", "https://api.mainnet-beta.solana.com"},
		{"base", "https://mainnet.base.org", "https://mainnet.base.org", "https://mainnet.base.org"},
		{"bsc", "https://bsc-rpc.publicnode.com", "https://bsc-dataseed.bnbchain.org", "https://bsc-dataseed.bnbchain.org"},
	} {
		chain, ok := cfg.Chain(tc.chain)
		if !ok {
			t.Fatalf("missing configured chain %s", tc.chain)
		}
		for _, role := range []struct{ name, configured, public string }{
			{"ingest", chain.Ingest.RPCURL, tc.ingest},
			{"lookup", chain.LookupRPCURL, tc.lookup},
			{"backfill", chain.Backfill.RPCURL, tc.backfill},
		} {
			want := []string{"https://private.example/" + tc.chain + "/credential/", role.public}
			if got := chain.RPCURLs(role.configured); !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s endpoints=%v; want %v", tc.chain, role.name, got, want)
			}
		}
	}
}

func TestPrivateRPCRejectsMalformedHTTPURLWithoutLeakingValue(t *testing.T) {
	for _, value := range []string{"https://", "https://private.example/credential%zz", "https://private.example/credential#fragment"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("RADAR_TEST_PRIVATE_RPC_URL", value)
			_, err := config.Load(write(t, `
chains:
  - name: base
    family: evm
    rpc_url_env: RADAR_TEST_PRIVATE_RPC_URL
    ingest: {rpc_url: "https://mainnet.base.org", rps: 5, block_interval: 2s, poll: 1s}
`))
			if err == nil {
				t.Fatal("Load accepted a malformed private RPC URL")
			}
			if strings.Contains(err.Error(), "credential") {
				t.Fatal("configuration error exposed the private URL")
			}
		})
	}
}
