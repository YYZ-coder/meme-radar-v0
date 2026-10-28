package pgapi

import (
	"context"
	"net/http"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

// TokenDetail maps current projections and H1's observed all-pool windows.
func (b *Backend) TokenDetail(ctx context.Context, chain api.Chain, address string, native bool) (api.TokenDetail, error) {
	out, _, err := b.readTokenDetailObserved(ctx, chain, address, native)
	return out, err
}

func (b *Backend) readTokenDetailObserved(ctx context.Context, chain api.Chain, address string, native bool) (api.TokenDetail, time.Time, error) {
	token, err := tokenAddress(chain, address)
	if err != nil {
		return api.TokenDetail{}, time.Time{}, err
	}
	now := b.now().UTC()
	data, found, err := b.st.ReadTokenDetail(ctx, string(chain), token, native, now)
	t := data.Token
	if err != nil {
		return api.TokenDetail{}, time.Time{}, err
	}
	if !found {
		return api.TokenDetail{}, time.Time{}, api.Errorf(http.StatusNotFound, "not_found", "", "token not found")
	}
	if t.Decimals == nil {
		return api.TokenDetail{}, time.Time{}, api.Errorf(http.StatusServiceUnavailable, "api_unavailable", "", "token decimals unknown")
	}
	holdings, asOf, windows, same := data.Holdings, data.AsOf, data.Windows, data.SameName
	stamp := asOf.UTC().Format("2006-01-02T15:04:05.000Z")
	first := t.FirstSeenAt.UTC().Format("2006-01-02T15:04:05.000Z")
	out := api.TokenDetail{Token: api.TokenRef{Chain: chain, Address: displayAddress(string(chain), token), Symbol: t.Symbol, Name: t.Name, LaunchPlatform: t.LaunchPlatform, LaunchAt: optionalTime(t.LaunchAt)},
		Currency: b.currency(chain, native), AsOf: stamp, Decimals: int64(*t.Decimals), TotalSupply: t.Supply, Price: t.Price, MarketCap: t.MarketCap, Liquidity: t.Liquidity, ATHMarketCap: t.ATHMarketCap, ATHSince: &first,
		Holders: holdings.Holders, SMHolders: holdings.SMHolders, HolderCoverage: api.Coverage{Status: holdings.Coverage, Since: optionalTime(holdings.Since), AsOf: stamp}, Windows: []api.WindowSummary{}, SameName: []api.SearchToken{}}
	if holdings.AsOf != nil {
		out.HolderCoverage.AsOf = *optionalTime(holdings.AsOf)
	}
	if holdings.Since != nil {
		out.ATHSince = optionalTime(holdings.Since)
	}
	if len(t.Creator) > 0 {
		out.Creator = &api.Creator{Address: displayAddress(string(chain), t.Creator), HoldingPct: holdings.CreatorHoldingPct}
	}
	if len(t.MainPool) > 0 && t.Dex != nil {
		out.MainPool = &api.PoolRef{Address: displayAddress(string(chain), t.MainPool), Dex: *t.Dex, QuoteSymbol: t.QuoteSymbol}
	}
	for i, w := range windows {
		buys, sells := w.Buys, w.Sells
		out.Windows = append(out.Windows, api.WindowSummary{Window: []string{"5m", "1h", "4h", "24h"}[i], Volume: w.Volume, NetInflow: w.NetInflow, Traders: w.Traders, Buys: &buys, Sells: &sells, BuyVolume: w.BuyVolume, SellVolume: w.SellVolume, PriceChangePct: w.PriceChangePct})
		if w.Window == 24*time.Hour {
			out.Volume24h, out.PriceChange24hPct = w.Volume, w.PriceChangePct
		}
	}
	for _, r := range same {
		out.SameName = append(out.SameName, api.SearchToken{TokenRef: api.TokenRef{Chain: api.Chain(r.Chain), Address: displayAddress(r.Chain, r.Address), Symbol: r.Symbol, Name: r.Name, LaunchPlatform: r.LaunchPlatform, LaunchAt: optionalTime(r.LaunchAt)}, MarketCapUSD: r.MarketCapUSD, AgeSeconds: r.AgeSeconds, LastTradeAt: optionalTime(r.LastTradeAt)})
	}
	return out, data.ReadAt, nil
}
