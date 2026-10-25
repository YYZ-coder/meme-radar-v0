package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
)

func TestTokensToEnrichIncludesMissingDecimals(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	missing, known, pool := []byte("missing-decimals"), []byte("known"), []byte("pool")
	six := int16(6)
	applyWindowBlock(t, s, "bsc", 1, now.Add(-time.Minute), parse.Facts{
		Tokens: []parse.Token{{Address: missing, Created: true, TotalSupply: "1000000000"}, {Address: known, Created: true, TotalSupply: "1000000000", Decimals: &six}},
		Pools:  []parse.Pool{{ID: pool, Token: missing, Quote: []byte("USDC"), Protocol: "pancake_v2", Dex: "pancake_v2"}},
		Trades: []parse.Trade{
			{Tx: 1, Ix: 1, TxHash: []byte("tx1"), Pool: pool, Token: missing, Attribution: "ambiguous", Side: "buy", TokenAmount: "1", QuoteAmount: "1", Decoder: "test", Version: 1},
			{Tx: 2, Ix: 1, TxHash: []byte("tx2"), Pool: pool, Token: known, Attribution: "ambiguous", Side: "buy", TokenAmount: "1", QuoteAmount: "1", Decoder: "test", Version: 1},
		},
	})
	got, err := s.TokensToEnrich(ctx, "bsc", now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]byte{missing}; !reflect.DeepEqual(got, want) {
		t.Errorf("TokensToEnrich=%q, want only %q (supply exists but decimals missing)", got, want)
	}
}
