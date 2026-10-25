package store_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

// Expectations come from an independent Python base58 implementation
// (backend/testdata/expect/expect_addr.py); the strings are well-known Solana program IDs.
var addrCases = []struct {
	chain, hex, text string
}{
	{"solana", "0000000000000000000000000000000000000000000000000000000000000000", "11111111111111111111111111111111"},
	{"solana", "0156e0f693665acf44db1568bf175baa5189cb97f5d2ff3b655d2bb6fd6d18b0", "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P"},
	{"solana", "0c14defc825ec67694250818bb654065f4298d3156d571b4d4f8090c18e9a863", "pAMMBay6oceH9fJKBRHGP5D4bD4sWpmSwMn52FMfXEA"},
	{"solana", "069b8857feab8184fb687f634618c035dac439dc1aeb3b5598a0f00000000001", "So11111111111111111111111111111111111111112"},
	{"solana", "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f40",
		"2Ana1pUpv2ZbMVkwF5FXapYeBEjdxDatLn7nvJkhgTSXbs59SyZSx866bXirPgj8QQVB57uxHJBG1YFvkRbFj4T"},
	{"solana", "000001", "112"},
	{"bsc", "ec4549cadce5da21df6e6422d448034b5233bfbc", "0xec4549cadce5da21df6e6422d448034b5233bfbc"},
	{"base", "", "0x"},
}

func TestAddressDisplayFunctionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	url := testdb.New(t)
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	for _, c := range addrCases {
		raw, _ := hex.DecodeString(c.hex)
		var text string
		if err := db.QueryRow(ctx, `SELECT addr($1::chain_id, $2)`, c.chain, raw).Scan(&text); err != nil {
			t.Fatalf("addr(%s, %s): %v", c.chain, c.hex, err)
		}
		if text != c.text {
			t.Errorf("addr(%s, %s) = %q; want %q", c.chain, c.hex, text, c.text)
		}
		var back []byte
		if err := db.QueryRow(ctx, `SELECT from_addr($1::chain_id, $2)`, c.chain, c.text).Scan(&back); err != nil {
			t.Fatalf("from_addr(%s, %s): %v", c.chain, c.text, err)
		}
		if !bytes.Equal(back, raw) {
			t.Errorf("from_addr(%s, %s) = %x; want %s", c.chain, c.text, back, c.hex)
		}
	}
	// EVM input is accepted in checksum case and without the prefix.
	var back []byte
	db.QueryRow(ctx, `SELECT from_addr('bsc', 'EC4549caDcE5DA21Df6E6422d448034B5233bFbC')`).Scan(&back)
	if hex.EncodeToString(back) != "ec4549cadce5da21df6e6422d448034b5233bfbc" {
		t.Errorf("from_addr(bsc, checksum, no 0x) = %x", back)
	}
	// Characters outside the base58 alphabet are an error, not a wrong key.
	for _, bad := range []string{"0OIl", "6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P "} {
		if err := db.QueryRow(ctx, `SELECT from_addr('solana', $1)`, bad).Scan(&back); err == nil || err == pgx.ErrNoRows {
			t.Errorf("from_addr(solana, %q) = %x, %v; want an error", bad, back, err)
		}
	}
}
