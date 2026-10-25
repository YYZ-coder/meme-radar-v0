package pgapi_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/store"
)

func TestPgTokenSmartMoneyLastTradeAt(t *testing.T) {
	f := newFixture(t)
	token, pool, quote := raw(20, 0x61), raw(20, 0xa6), raw(20, 0xc6)
	six := int16(6)
	created := f.now.Add(-31 * 24 * time.Hour)
	f.apply(t, "bsc", 1, created, parse.Facts{Tokens: []parse.Token{{Address: token, Created: true, Decimals: &six, TotalSupply: "1000000000000000"}, {Address: raw(20, 0x62), Created: true, Decimals: &six}}, Pools: []parse.Pool{{ID: pool, Token: token, Quote: quote, Protocol: "pancake_v2", Dex: "pancake_v2", Created: true}}})
	trade := func(owner, token []byte) parse.Trade {
		return parse.Trade{Tx: 1, Ix: 1, TxHash: raw(32, 1), Pool: pool, Token: token, Wallet: owner, Side: "buy", Attribution: "confirmed", TokenAmount: "1", QuoteAmount: "1", Decoder: "test", Version: 1}
	}
	// Independent half-open range [now-30d,now). The newer wrong-token,
	// wrong-chain, exactly-now and future trades must not replace M1's result.
	for i, tc := range []struct {
		at           time.Time
		owner, token []byte
	}{
		{f.now.Add(-30*24*time.Hour - time.Second), raw(20, 0x14), token},
		{f.now.Add(-30 * 24 * time.Hour), raw(20, 0x13), token},
		{f.now.Add(-time.Minute), raw(20, 0x11), token},
		{f.now.Add(-10 * time.Second), raw(20, 0x11), raw(20, 0x62)},
		{f.now, raw(20, 0x11), token},
		{f.now.Add(time.Second), raw(20, 0x11), token},
	} {
		f.apply(t, "bsc", uint64(i+2), tc.at, parse.Facts{Trades: []parse.Trade{trade(tc.owner, tc.token)}})
	}
	f.apply(t, "base", 1, f.now.Add(-5*time.Second), parse.Facts{Tokens: []parse.Token{{Address: token, Decimals: &six}}, Pools: []parse.Pool{{ID: pool, Token: token, Quote: quote, Protocol: "uniswap_v2", Dex: "uniswap_v2", Created: true}}, Trades: []parse.Trade{trade(raw(20, 0x11), token)}})
	blocks, err := f.st.UnbalancedBlocks(t.Context(), "bsc", balances.Version, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range blocks {
		b := store.BalanceBlock{Chain: "bsc", Num: block.Num, Time: block.Time, Version: balances.Version}
		if block.Num == 1 {
			for _, owner := range []byte{0x11, 0x13, 0x14, 0x15} {
				b.Deltas = append(b.Deltas, parse.Balance{Token: token, Owner: raw(20, owner), Delta: "100000000"})
			}
		}
		if err := f.st.ApplyBalances(t.Context(), []store.BalanceBlock{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.RefreshCoverage(t.Context(), "bsc"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RefreshBalances(t.Context(), "bsc", f.now, balances.Version); err != nil {
		t.Fatal(err)
	}
	queueHolderMembers(t, f, "chain,address,label\n"+
		"bsc,0x0000000000000000000000000000000000000011,M1\n"+
		"bsc,0x0000000000000000000000000000000000000013,Lower boundary\n"+
		"bsc,0x0000000000000000000000000000000000000014,Expired\n"+
		"bsc,0x0000000000000000000000000000000000000015,No history\n")
	buildHolderMembers(t, f)
	var got api.TokenSmartMoney
	getPG(t, f.handler(), "/api/v1/tokens/{chain}/{address}/smart-money", holdingsToken+"/smart-money", 200, &got)
	if len(got.Items) != 4 {
		t.Fatalf("items=%+v", got.Items)
	}
	want := map[string]*time.Time{}
	recent, lower := f.now.Add(-time.Minute), f.now.Add(-30*24*time.Hour)
	want["0x0000000000000000000000000000000000000011"] = &recent
	want["0x0000000000000000000000000000000000000013"] = &lower
	want["0x0000000000000000000000000000000000000014"] = nil
	want["0x0000000000000000000000000000000000000015"] = nil
	for _, row := range got.Items {
		at, ok := want[row.Wallet.Address]
		if !ok {
			t.Fatalf("unexpected wallet %+v", row.Wallet)
		}
		if at == nil {
			if row.LastTradeAt != nil {
				t.Errorf("%s history=%s;want NULL", row.Wallet.Address, *row.LastTradeAt)
			}
		} else if row.LastTradeAt == nil || *row.LastTradeAt != at.Format("2006-01-02T15:04:05.000Z") {
			t.Errorf("%s last_trade_at=%v;want %s", row.Wallet.Address, row.LastTradeAt, at.Format(time.RFC3339))
		}
		// H4 is now installed. M1 has an unpriced recent buy; the others
		// have no 24h trades. No member has a sell in the complete window.
		if row.SellVolumeUSD == nil || *row.SellVolumeUSD != "0" {
			t.Errorf("no 24h sells must be known zero: %+v", row)
		}
		if row.Wallet.Address == "0x0000000000000000000000000000000000000011" {
			if row.BuyVolumeUSD != nil {
				t.Errorf("unpriced recent buy must be NULL: %+v", row)
			}
		} else if row.BuyVolumeUSD == nil || *row.BuyVolumeUSD != "0" {
			t.Errorf("no 24h buys must be known zero: %+v", row)
		}
	}
}
