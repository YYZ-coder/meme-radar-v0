package pgapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/store"
)

// Independent arithmetic from H2-3: (100-80)/80*100 = 25 percent.
func TestPgNativePrices(t *testing.T) {
	f := newFixture(t)
	// First slice covers prices that exist. The no-row as_of policy is pending.
	f.cfg.Chains = []config.Chain{{Name: "solana", Native: "SOL"}, {Name: "base", Native: "ETH"}}
	ctx := context.Background()
	if err := f.st.SaveQuotePrices(ctx, []store.QuotePrice{
		{Symbol: "SOL", Minute: apiTime, USD: "100", Source: "coinbase"},
		{Symbol: "SOL", Minute: apiTime.Add(-24 * time.Hour), USD: "80", Source: "coinbase"},
		{Symbol: "ETH", Minute: apiTime, USD: "2500", Source: "coinbase"},
	}); err != nil {
		t.Fatal(err)
	}
	// SaveQuotePrices owns value writes; only the database-generated fixture clock
	// is set here so stale/as_of assertions do not depend on the host's wall time.
	if _, err := f.conn.Exec(ctx, "UPDATE quote_prices SET updated_at=minute"); err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	var got api.NativePrices
	const route = "/api/v1/market/natives"
	getPG(t, h, route, route, 200, &got)
	if len(got.Items) != 2 {
		t.Fatalf("items=%+v, want two configured chains", got.Items)
	}
	sol, eth := got.Items[0], got.Items[1]
	if sol.Chain != api.Solana || sol.Symbol != "SOL" || sol.PriceUSD == nil || *sol.PriceUSD != "100" || sol.Change24hPct == nil || *sol.Change24hPct != "25" || sol.Stale || sol.AsOf != "2026-10-23T10:00:00.000Z" {
		t.Errorf("SOL=%+v, want 100, 25%%, fresh at 10:00Z", sol)
	}
	if eth.Chain != api.Base || eth.Symbol != "ETH" || eth.PriceUSD == nil || *eth.PriceUSD != "2500" || eth.Change24hPct != nil || eth.Stale {
		t.Errorf("ETH=%+v, want 2500, unknown change, fresh", eth)
	}
	f.now = apiTime.Add(60 * time.Second)
	getPG(t, h, route, route, 200, &got)
	if got.Items[0].Stale {
		t.Error("exactly 60s is fresh; stale requires >60s")
	}
	f.now = apiTime.Add(2 * time.Minute)
	getPG(t, h, route, route, 200, &got)
	if !got.Items[0].Stale {
		t.Error("at 10:02 SOL must be stale")
	}
}
