package parser_test

import (
	"context"
	"testing"

	"encoding/json"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parser"
	"github.com/meme-radar/radar/internal/solana"
)

func TestBalancePassParsesOnlyBalances(t *testing.T) {
	ctx := context.Background()
	s, c := setup(t)
	var recorded struct {
		BlockTime    int64             `json:"blockTime"`
		Transactions []json.RawMessage `json:"transactions"`
	}
	if err := json.Unmarshal(recordedBlock(t), &recorded); err != nil {
		t.Fatal(err)
	}
	// Slot 460261066, tx 7: two token accounts share an owner. Python meta
	// sums give +5818 and -5818, not one row per token account.
	payload, err := json.Marshal(map[string]any{"blockTime": recorded.BlockTime, "transactions": recorded.Transactions[7:8]})
	if err != nil {
		t.Fatal(err)
	}
	mint := solana.MustKey("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")
	for _, num := range []uint64{11, 12, 13} {
		commit(t, s, num, payload)
	}
	main := parser.New(parser.Config{Chain: "solana", Store: s, Version: 7, Batch: 10, Decode: func(context.Context, []byte) (parser.Decoded, error) {
		return parser.Decoded{Facts: parse.Facts{
			Tokens: []parse.Token{{Address: mint[:]}},
			Trades: []parse.Trade{{Tx: 0, Ix: 1, TxHash: []byte{1}, Pool: []byte{1}, Token: mint[:], Side: "buy", Attribution: "confirmed", TokenAmount: "1", QuoteAmount: "1", Decoder: "fixture", Version: 1}},
		}}, nil
	}})
	if _, err := main.Step(ctx); err != nil {
		t.Fatal(err)
	}
	pass := parser.NewBalances(parser.BalanceConfig{Chain: "solana", Family: "solana", Store: s, Version: 1, Batch: 1})
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	for sql, want := range map[string]string{
		"SELECT count(*)::text FROM trades": "3",
		"SELECT min(parsed_version)::text || '|' || max(parsed_version)::text FROM chain_blocks":                           "7|7",
		"SELECT coalesce(string_agg(block_num::text,',' ORDER BY block_num),'') FROM chain_blocks WHERE balance_version=1": "13",
		"SELECT count(*)::text FROM balance_deltas":                                                                        "2",
		"SELECT string_agg(delta::text,',' ORDER BY delta) FROM balance_deltas":                                            "-5818,5818",
	} {
		if got := query(t, c, sql); got != want {
			t.Errorf("%s = %s;want %s", sql, got, want)
		}
	}
	if _, err := pass.Step(ctx); err != nil {
		t.Fatal(err)
	}
	newer := parser.NewBalances(parser.BalanceConfig{Chain: "solana", Family: "solana", Store: s, Version: 2, Batch: 1})
	if _, err := newer.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got := query(t, c, "SELECT coalesce(string_agg(block_num::text,','),'') FROM chain_blocks WHERE balance_version=2"); got != "13" {
		t.Errorf("balance version upgrade ran blocks %s;want 13", got)
	}
	if got := query(t, c, "SELECT min(parsed_version)::text FROM chain_blocks"); got != "7" {
		t.Errorf("balance pass changed main progress=%s", got)
	}
	before := query(t, c, "SELECT string_agg(balance_parsed_at::text,',' ORDER BY block_num) FROM chain_blocks")
	if _, err := s.ResetParsed(ctx, "solana", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := main.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if got := query(t, c, "SELECT string_agg(balance_parsed_at::text,',' ORDER BY block_num) FROM chain_blocks"); got != before {
		t.Errorf("main pass changed balance progress: %s -> %s", before, got)
	}
}
