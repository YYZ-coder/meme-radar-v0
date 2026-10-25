package pgapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/store"
)

// H2-3 plus the user's 2026-10-24 decision: a missing native price retains
// its chain row, NULL values, stale=true, and uses the request's now for as_of.
func TestPgNativePricesMissing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.SaveQuotePrices(ctx, []store.QuotePrice{
		{Symbol: "SOL", Minute: apiTime, USD: "100", Source: "coinbase"},
		{Symbol: "SOL", Minute: apiTime.Add(-24 * time.Hour), USD: "80", Source: "coinbase"},
		{Symbol: "ETH", Minute: apiTime, USD: "2500", Source: "coinbase"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, "UPDATE quote_prices SET updated_at=minute"); err != nil {
		t.Fatal(err)
	}
	var got api.NativePrices
	const route = "/api/v1/market/natives"
	getPG(t, f.handler(), route, route, 200, &got)
	if len(got.Items) != 3 {
		t.Fatalf("items=%+v, want all three chains", got.Items)
	}
	bnb := got.Items[2]
	if bnb.Chain != api.BSC || bnb.Symbol != "BNB" || bnb.PriceUSD != nil || bnb.Change24hPct != nil || !bnb.Stale || bnb.AsOf != "2026-10-23T10:00:30.000Z" {
		t.Errorf("BNB=%+v, want BSC/BNB NULL prices, stale, request as_of 10:00:30Z", bnb)
	}
	if got.Items[0].PriceUSD == nil || *got.Items[0].PriceUSD != "100" || got.Items[0].AsOf != "2026-10-23T10:00:00.000Z" {
		t.Errorf("missing BNB must preserve known SOL price/time: %+v", got.Items[0])
	}
}
