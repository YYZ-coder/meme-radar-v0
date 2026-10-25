package pgapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/store"
)

// Overview serves exact active-member statistics at the request's cutoff.
func (b *Backend) Overview(ctx context.Context, chain api.Chain, window string) (api.Overview, error) {
	w, err := time.ParseDuration(window)
	if err != nil {
		return api.Overview{}, err
	}
	now := b.now().UTC()
	v, err := b.st.InflowOverview(ctx, string(chain), w, now)
	if err != nil {
		return api.Overview{}, err
	}
	coverage := "partial"
	if v.Complete {
		coverage = "complete"
	}
	return api.Overview{AsOf: now.Format("2006-01-02T15:04:05.000Z"), ListVersion: strconv.FormatInt(v.Version, 10), Window: window, Coverage: coverage, ActiveWallets: v.ActiveWallets, LargeBuyVolumeUSD: v.Large, SmallBuyVolumeUSD: v.Small, LargeBuySharePct: v.LargeShare, SmallBuySharePct: v.SmallShare, TrackedTokens: v.TrackedTokens, NewTokens: v.NewTokens, NetInflowUSD: v.Net, BuyVolumeUSD: v.Buy, SellVolumeUSD: v.Sell, NetInflowChangePct: v.Change, BuySharePct: v.BuyShare, InflowBars: v.Bars}, nil
}

// InflowProjection supplies current persisted rows to the existing HTTP
// filtering, sorting, snapshot and pagination implementation.
func (b *Backend) InflowProjection(ctx context.Context, chain api.Chain, window string, native bool) (api.Projection, error) {
	data, err := b.st.ReadInflow(ctx, string(chain), window, native, nil)
	if errors.Is(err, store.ErrProjectionPending) {
		return api.Projection{}, unavailable()
	}
	if err != nil {
		return api.Projection{}, err
	}
	out := api.Projection{AsOf: b.now().UTC().Format("2006-01-02T15:04:05.000Z"), ListVersion: strconv.FormatInt(data.ListVersion, 10), Rows: []api.ProjectionRow{}}
	var latest time.Time
	if chain == "" && len(data.Heads) > 0 {
		out.ChainAsOf = api.ChainTimes{}
	}
	for _, head := range data.Heads {
		if latest.IsZero() || head.AsOf.After(latest) {
			latest = head.AsOf
		}
		if out.ChainAsOf != nil {
			out.ChainAsOf[api.Chain(head.Chain)] = head.AsOf.Format("2006-01-02T15:04:05.000Z")
		}
	}
	if !latest.IsZero() {
		out.AsOf = latest.Format("2006-01-02T15:04:05.000Z")
	}
	for _, v := range data.Rows {
		row, err := b.inflowWireRow(v, native)
		if err != nil {
			return api.Projection{}, err
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// InflowRows reads only the current rows for the requested batch keys.
func (b *Backend) InflowRows(ctx context.Context, window string, native bool, keys []api.TokenKey) ([]api.InflowRow, error) {
	if len(keys) == 0 {
		return []api.InflowRow{}, nil
	}
	wanted := make([]store.InflowKey, len(keys))
	for i, k := range keys {
		address, err := tokenAddress(k.Chain, k.Address)
		if err != nil {
			return nil, err
		}
		wanted[i] = store.InflowKey{Chain: string(k.Chain), Address: address}
	}
	data, err := b.st.ReadInflow(ctx, "", window, native, wanted)
	if errors.Is(err, store.ErrProjectionPending) {
		return nil, unavailable()
	}
	if err != nil {
		return nil, err
	}
	out := []api.InflowRow{}
	for _, v := range data.Rows {
		row, err := b.inflowWireRow(v, native)
		if err != nil {
			return nil, err
		}
		out = append(out, row.Row)
	}
	return out, nil
}

func (b *Backend) inflowWireRow(v store.InflowDataRow, native bool) (api.ProjectionRow, error) {
	row := api.InflowRow{Token: api.TokenRef{Chain: api.Chain(v.Chain), Address: displayAddress(v.Chain, v.Address), Symbol: v.Symbol, Name: v.Name, ImageURL: v.ImageURL, LaunchPlatform: v.Platform, LaunchAt: optionalTime(v.LaunchAt)},
		Currency: b.currency(api.Chain(v.Chain), native), Price: v.Price, MarketCap: v.MarketCap, PriceChangePct: v.Change, Liquidity: v.Liquidity,
		Trades: v.Trades, Buys: v.Buys, Sells: v.Sells, Observed: api.Observed{Trades: v.ObservedTrades, Buys: v.ObservedBuys, Sells: v.ObservedSells}, Coverage: v.Coverage,
		Volume: v.Volume, NetInflow: v.Net, SMHolders: v.SMHolders, SMTrades: v.SMTrades, Holders: v.Holders, AgeSeconds: v.Age, Dexes: append([]string{}, v.Dexes...), Sparkline: v.Sparkline}
	if len(v.Socials) > 0 {
		if err := json.Unmarshal(v.Socials, &row.Socials); err != nil {
			return api.ProjectionRow{}, err
		}
	}
	for i, id := range v.Dexes {
		row.Dexes[i] = displayName(b.cfg.DisplayNames.Dexes, id)
	}
	out := api.ProjectionRow{Row: row, DexIDs: append([]string{}, v.Dexes...)}
	if v.Platform != nil {
		out.Platform = *v.Platform
	}
	if v.DevHoldingPct != nil {
		pct, err := strconv.ParseFloat(*v.DevHoldingPct, 64)
		if err != nil {
			return api.ProjectionRow{}, err
		}
		out.DevHoldingPct = &pct
	}
	return out, nil
}

// Facets serves exact aggregates with configured names at the published heads.
func (b *Backend) Facets(ctx context.Context, chain api.Chain, window string) (api.Facets, error) {
	values, complete, err := b.st.InflowFacets(ctx, string(chain), window)
	if errors.Is(err, store.ErrProjectionPending) {
		return api.Facets{}, unavailable()
	}
	if err != nil {
		return api.Facets{}, err
	}
	out := api.Facets{Coverage: "partial", Dexes: []api.Facet{}, Platforms: []api.Facet{}}
	if complete {
		out.Coverage = "complete"
	}
	for _, v := range values {
		names := b.cfg.DisplayNames.Dexes
		if v.Kind == "platform" {
			names = b.cfg.DisplayNames.Platforms
		}
		f := api.Facet{ID: v.ID, Chain: api.Chain(v.Chain), Name: displayName(names, v.ID), VolumeUSD: v.Volume, TokenCount: v.Count, ObservedTokenCount: v.Observed}
		if v.Kind == "dex" {
			out.Dexes = append(out.Dexes, f)
		} else {
			out.Platforms = append(out.Platforms, f)
		}
	}
	return out, nil
}

func displayName(names map[string]string, id string) string {
	if name := names[id]; name != "" {
		return name
	}
	return id
}
