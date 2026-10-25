package pgapi

import (
	"context"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

// NativePrices returns configured native coins in configuration order.
func (b *Backend) NativePrices(ctx context.Context) (api.NativePrices, error) {
	symbols := make([]string, 0, len(b.cfg.Chains))
	for _, chain := range b.cfg.Chains {
		symbols = append(symbols, chain.Native)
	}
	quotes, err := b.st.APIQuotePrices(ctx, symbols)
	if err != nil {
		return api.NativePrices{}, err
	}
	out := api.NativePrices{Items: make([]api.NativePrice, 0, len(quotes))}
	now := b.now()
	for i, q := range quotes {
		price := api.NativePrice{Chain: api.Chain(b.cfg.Chains[i].Name), Symbol: q.Symbol,
			PriceUSD: q.PriceUSD, Change24hPct: q.Change24hPct, Stale: true,
			AsOf: now.UTC().Format("2006-01-02T15:04:05.000Z")}
		if q.UpdatedAt != nil {
			price.Stale = now.Sub(*q.UpdatedAt) > 60*time.Second
			price.AsOf = q.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		}
		out.Items = append(out.Items, price)
	}
	return out, nil
}
