package compute_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

// open returns a migrated store and a plain connection for checking tables.
func open(t *testing.T) (*store.Store, *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	url := testdb.New(t)
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return s, c
}

func one(t *testing.T, c *pgx.Conn, sql string, args ...any) string {
	t.Helper()
	var v *string
	if err := c.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if v == nil {
		return "NULL"
	}
	return *v
}

// apply commits block num at time at and writes facts f as parse would.
func apply(t *testing.T, s *store.Store, chain string, num uint64, at time.Time, f parse.Facts) {
	t.Helper()
	ctx := context.Background()
	if err := s.Commit(ctx, store.Commit{Chain: chain, Block: store.Block{Num: num, Hash: []byte{byte(num), byte(num >> 8), 1}, ParentHash: []byte{byte(num - 1), byte((num - 1) >> 8), 1},
		Time: at, Status: store.StatusConfirmed, TxCount: 1, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyParsed(ctx, store.Parsed{Chain: chain, Num: num, Time: at, Version: 1, Facts: f}); err != nil {
		t.Fatal(err)
	}
}

// trade is a minimal confirmed buy in pool of token.
func trade(tx int, pool, token []byte, tok, quote, sym string) parse.Trade {
	return parse.Trade{Tx: tx, Ix: 1, TxHash: []byte{byte(tx), 7}, Pool: pool, Token: token, Wallet: []byte("w"), Attribution: "confirmed",
		Side: "buy", TokenAmount: tok, QuoteAmount: quote, QuoteSymbol: sym, Decoder: "test", Version: 1}
}

var day = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
