package metadata_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/metadata"
	"github.com/meme-radar/radar/internal/solana"
)

func TestChainSourceReadsRecordedURIs(t *testing.T) {
	var accounts struct {
		Samples []struct {
			Mint, PDA string
			Response  json.RawMessage
		}
	}
	raw, err := os.ReadFile("testdata/metaplex_accounts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &accounts); err != nil {
		t.Fatal(err)
	}
	var doppler struct {
		Samples []struct {
			Address  string
			Response json.RawMessage
		}
	}
	raw, err = os.ReadFile("testdata/doppler_token_uri.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doppler); err != nil {
		t.Fatal(err)
	}
	mint, err := solana.DecodeBase58(accounts.Samples[1].Mint)
	if err != nil {
		t.Fatal(err)
	}
	evm, err := hex.DecodeString(strings.TrimPrefix(doppler.Samples[0].Address, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, family, method, params, want string
		address                            []byte
		response                           json.RawMessage
		noSource                           bool
	}{
		{"metaplex", "solana", "getAccountInfo", accounts.Samples[1].PDA, "https://static.jup.ag/jup/metadata.json", mint, accounts.Samples[1].Response, false},
		{"doppler", "evm", "eth_call", "0x3c130d90", "https://ipfs.io/ipfs/QmWwZM7CR4wJ1boTsD76nrv2nxeMyMJ8PmRiJNCu85HPAp", evm, doppler.Samples[0].Response, false},
		{"missing_account", "solana", "getAccountInfo", accounts.Samples[1].PDA, "", mint, json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"value":null}}`), true},
		{"reverted_call", "evm", "eth_call", "0x3c130d90", "", evm, json.RawMessage(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted"}}`), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Method string
					Params json.RawMessage
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Method != tc.method || !strings.Contains(string(req.Params), tc.params) {
					t.Errorf("RPC request=%s %s; want %s containing %s", req.Method, req.Params, tc.method, tc.params)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(tc.response)
			}))
			defer srv.Close()
			source := metadata.NewChainSource(tc.family, jsonrpc.New(srv.URL, srv.Client(), nil, nil))
			got, err := source.URI(context.Background(), tc.address)
			if tc.noSource {
				if !errors.Is(err, metadata.ErrNoSource) {
					t.Errorf("URI=%q err=%v; want ErrNoSource", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("URI=%q err=%v; want %q", got, err, tc.want)
			}
		})
	}
}
