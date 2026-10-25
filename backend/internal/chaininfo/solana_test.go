package chaininfo_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/solana"
)

// getTokenSupply answers recorded from api.mainnet-beta.solana.com on
// 2026-10-23 (testdata/solana_token_supply.json).
func TestMintDecimalsReadsRecordedTokenSupply(t *testing.T) {
	sol := recordedSolana(t)
	// The recorded USDC supply and decimals, read from the response by hand.
	usdc, _ := solana.DecodeBase58("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")
	if amount, dec, err := sol.MintSupply(context.Background(), usdc); err != nil || amount != "7849020959490626" || dec != 6 {
		t.Errorf("MintSupply(USDC) = %s, %d, %v; want 7849020959490626, 6", amount, dec, err)
	}
	mintDecimals(t, sol)
}

func recordedSolana(t *testing.T) *chaininfo.Solana {
	b, _ := os.ReadFile("testdata/solana_token_supply.json")
	var rec []struct {
		Mint     string
		Response json.RawMessage
	}
	json.Unmarshal(b, &rec)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		for _, c := range rec {
			if strings.Contains(string(body), `"getTokenSupply"`) && strings.Contains(string(body), c.Mint) {
				w.Write(c.Response)
				return
			}
		}
		t.Errorf("unrecorded request %s", body)
	}))
	t.Cleanup(srv.Close)
	return chaininfo.NewSolana(jsonrpc.New(srv.URL, srv.Client(), nil, nil))
}

func mintDecimals(t *testing.T, sol *chaininfo.Solana) {
	// Well-known mint decimals: USDC and USDT 6, wrapped SOL 9.
	for mint, want := range map[string]int16{
		"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v": 6,
		"Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB": 6,
		"So11111111111111111111111111111111111111112":  9,
	} {
		raw, _ := solana.DecodeBase58(mint)
		got, err := sol.MintDecimals(context.Background(), raw)
		if err != nil || got != want {
			t.Errorf("MintDecimals(%s) = %d, %v; want %d", mint, got, err, want)
		}
	}
}
