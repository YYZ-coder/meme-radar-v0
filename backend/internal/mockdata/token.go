package mockdata

import (
	"context"
	"encoding/base64"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/meme-radar/radar/internal/api"
)

// lowerBoundMs is how far before now holder balances are fully replayed.
const lowerBoundMs = 30 * day

func (d *Data) token(chain api.Chain, address string) (*token, error) {
	t := findToken(chain, address)
	if t == nil {
		return nil, api.Errorf(http.StatusNotFound, "not_found", "", "token not found")
	}
	return t, nil
}

func decodeCursor(c string) (anchor, offset float64, err error) {
	if c == "" {
		return 0, 0, nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(c)
	parts := strings.Split(string(raw), ":")
	if e == nil && len(parts) == 2 {
		a, e1 := strconv.ParseFloat(parts[0], 64)
		o, e2 := strconv.ParseFloat(parts[1], 64)
		if e1 == nil && e2 == nil && o >= 0 {
			return a, o, nil
		}
	}
	return 0, 0, api.Errorf(http.StatusBadRequest, "invalid_param", "cursor", "malformed cursor")
}

func encodeCursor(anchor, offset float64) *string {
	return ptr(base64.RawURLEncoding.EncodeToString([]byte(num(anchor) + ":" + num(offset))))
}

func (d *Data) smWallets(chain api.Chain) []api.Member {
	d.lock()
	defer d.mu.Unlock()
	var out []api.Member
	for _, m := range d.members {
		if m.Chain == chain && !m.Excluded {
			out = append(out, m)
		}
	}
	return out
}

func smRef(m api.Member) api.WalletRef {
	labels := []string{"smart_money"}
	if slices.Contains(m.Tags, "kol") {
		labels = append(labels, "kol")
	}
	return api.WalletRef{Address: m.Address, Labels: labels, DisplayName: m.Label}
}

// smWalletAt is the i-th distinct smart-money wallet of a token.
func (d *Data) smWalletAt(t *token, i int) *api.WalletRef {
	pool := d.smWallets(t.chain)
	if i >= len(pool) {
		return nil
	}
	w := smRef(pool[(int(t.seed)+i)%len(pool)])
	return &w
}

func (d *Data) walletFor(t *token, key string, smChance float64) api.WalletRef {
	r := rng(hash(t.address + ":" + key))
	pool := d.smWallets(t.chain)
	if len(pool) > 0 && r() < smChance {
		return smRef(pool[int(r()*float64(len(pool)))])
	}
	n := num(math.Floor(r() * 400))
	addr := evmAddress("w"+n, "")
	if t.chain == api.Solana {
		addr = solanaAddress("w"+n, "")
	}
	return api.WalletRef{Address: addr, Labels: []string{}}
}

func coverage(t *token, now float64) api.Coverage {
	lower := now - lowerBoundMs
	if t.launchAt >= lower {
		return api.Coverage{Status: "full", Since: ptr(iso(t.launchAt)), AsOf: iso(now)}
	}
	return api.Coverage{Status: "partial", Since: ptr(iso(lower)), AsOf: iso(now)}
}

// address derives a chain-shaped address from key.
func address(chain api.Chain, key string) string {
	if chain == api.Solana {
		return solanaAddress(key, "")
	}
	return evmAddress(key, "")
}

func curvePool(t *token) string { return address(t.chain, "curve:"+t.address) }

func mainPool(t *token) string { return address(t.chain, "pool:"+t.address+":"+t.dexes[0]) }

func poolsOf(t *token, now float64) []api.Pool {
	m := metrics(t, "24h", now)
	var out []api.Pool
	for i, id := range t.dexes {
		dx := dexByID(id)
		share := 1.0
		if len(t.dexes) > 1 {
			share = map[bool]float64{true: 0.72, false: 0.28}[i == 0]
		}
		p := api.Pool{
			Address: address(t.chain, "pool:"+t.address+":"+id), Dex: dx.name, Protocol: dx.protocol, QuoteSymbol: ptr(dx.quote),
			CreatedAt: ptr(iso(t.launchAt + float64(i)*3_600_000*6)), Status: "active", IsMain: i == 0,
		}
		if m.liquidity != nil {
			liq := *m.liquidity * share
			p.LiquidityUSD = ptr(dec(liq, 8))
			p.ReserveQuote = ptr(dec(liq/2/nativeUSD[t.chain], 8))
			if m.price != nil {
				p.ReserveToken = ptr(dec(liq/2 / *m.price, 10))
			}
		}
		out = append(out, p)
	}
	if pl := platformByID(t.platform); t.graduated && pl != nil && pl.curve {
		name := map[string]string{"pump_fun": "Pump", "raydium_launchlab": "LaunchLab", "four_meme": "Four.meme"}[pl.id]
		out = append(out, api.Pool{
			Address: curvePool(t), Dex: name, Protocol: pl.id + "_curve", QuoteSymbol: ptr(nativeSymbol[t.chain]),
			LiquidityUSD: ptr("0"), ReserveToken: ptr("0"), ReserveQuote: ptr("0"),
			CreatedAt: ptr(iso(t.launchAt)), Status: "graduated",
		})
	}
	return out
}

func windowSummary(t *token, w string, native bool, now float64) api.WindowSummary {
	m := metrics(t, w, now)
	c := func(v *float64) *string { return decp(conv(v, t.chain, native), 8) }
	s := api.WindowSummary{Window: w, Volume: c(m.volume), Traders: ptr(m.traders), Buys: ptr(m.buys), Sells: ptr(m.sells), PriceChangePct: pctChange(m.price, m.priceStart)}
	if m.volume != nil {
		buy := *m.volume * float64(m.buys) / math.Max(1, float64(m.trades))
		sell := *m.volume - buy
		net := buy - sell
		s.BuyVolume, s.SellVolume, s.NetInflow = c(&buy), c(&sell), c(&net)
	}
	return s
}

// TokenDetail returns the token page header and side bar.
func (d *Data) TokenDetail(_ context.Context, chain api.Chain, address string, native bool) (api.TokenDetail, error) {
	t, err := d.token(chain, address)
	if err != nil {
		return api.TokenDetail{}, err
	}
	now := d.ms()
	m := metrics(t, "24h", now)
	c := func(v *float64) *string { return decp(conv(v, t.chain, native), 8) }
	out := api.TokenDetail{
		Token: tokenRef(t), Currency: currencyOf(t.chain, native), AsOf: iso(now), Decimals: t.decimals,
		TotalSupply: ptr(dec(t.supply, 14)), Socials: t.socials.api(),
		Creator: &api.Creator{Address: t.creator, HoldingPct: decp(t.devHoldingPct, 4)},
		Price:   c(m.price), MarketCap: c(m.marketCap), Liquidity: c(m.liquidity), Volume24h: c(m.volume),
		PriceChange24hPct: pctChange(m.price, m.priceStart), Holders: ptr(t.holders), SMHolders: ptr(t.smHolders),
		ATHSince: ptr(iso(math.Max(t.launchAt, now-lowerBoundMs))), HolderCoverage: coverage(t, now),
		SameName: []api.SearchToken{},
	}
	for _, p := range poolsOf(t, now) {
		if p.IsMain {
			out.MainPool = &api.PoolRef{Address: p.Address, Dex: p.Dex, QuoteSymbol: p.QuoteSymbol}
		}
	}
	if t.basePrice != nil {
		ath := *t.basePrice * 1.9 * t.supply
		out.ATHMarketCap = c(&ath)
	}
	for _, w := range []string{"5m", "1h", "4h", "24h"} {
		out.Windows = append(out.Windows, windowSummary(t, w, native, now))
	}
	for _, o := range universe {
		if o != t && strings.EqualFold(o.symbol, t.symbol) && len(out.SameName) < 5 {
			out.SameName = append(out.SameName, searchToken(o, now))
		}
	}
	return out, nil
}

// Pools lists a token's pools.
func (d *Data) Pools(_ context.Context, chain api.Chain, address string) (api.Pools, error) {
	t, err := d.token(chain, address)
	if err != nil {
		return api.Pools{}, err
	}
	return api.Pools{Items: poolsOf(t, d.ms())}, nil
}

var intervalSeconds = map[string]float64{"1s": 1, "30s": 30, "1m": 60, "5m": 300, "1h": 3600, "1d": 86400}

// Candles returns main-pool bars.
func (d *Data) Candles(_ context.Context, chain api.Chain, address string, q api.CandleQuery) (api.Candles, error) {
	t, err := d.token(chain, address)
	if err != nil {
		return api.Candles{}, err
	}
	now := d.ms()
	step := intervalSeconds[q.Interval]
	toSec := math.Floor(now / 1000)
	if q.To != nil {
		toSec = float64(*q.To)
	}
	end := math.Floor(toSec/step) * step
	fromSec := end - float64(q.Limit-1)*step
	if q.From != nil {
		fromSec = float64(*q.From)
	}
	start := math.Max(math.Ceil(fromSec/step)*step, math.Floor(t.launchAt/1000/step)*step)
	scale := func(p float64) float64 {
		if q.Metric == "market_cap" {
			p *= t.supply
		}
		if q.Native {
			p /= nativeUSD[t.chain]
		}
		return p
	}
	out := api.Candles{
		Interval: q.Interval, Metric: q.Metric, Currency: currencyOf(t.chain, q.Native), AsOf: iso(end * 1000),
		Coverage: "complete", Items: []api.Candle{}, ObservedItems: []api.ObservedCandle{}, Gaps: []api.CandleGap{}, PoolSwitches: []api.PoolSwitch{},
	}
	base := 0.0
	if t.basePrice != nil {
		base = *t.basePrice
	}
	for ts := math.Max(start, end-float64(q.Limit)*step); ts < end; ts += step {
		var ps []float64
		for _, f := range []float64{0, 0.25, 0.5, 0.75, 1} {
			p := priceAt(t, (ts+f*step)*1000)
			if p == nil {
				break
			}
			ps = append(ps, *p)
		}
		if len(ps) < 5 {
			continue
		}
		noise := 1 + 0.004*unit(t.address+":"+q.Interval+":"+num(ts))
		vol := t.tradesPerHour * step / 3600 * (40 + t.supply*base/60_000) * (0.4 + unit("v:"+t.address+":"+num(ts)))
		v := *conv(&vol, t.chain, q.Native)
		out.Items = append(out.Items, api.Candle{
			T: int64(ts), O: dec(scale(ps[0]), 8), H: dec(scale(slices.Max(ps)*noise), 8),
			L: dec(scale(slices.Min(ps)/noise), 8), C: dec(scale(ps[4]), 8), V: dec(v, 6),
		})
	}
	if len(out.Items) == 0 {
		out.EmptyReason = ptr("no_trades")
	}
	if pl := platformByID(t.platform); t.graduated && pl != nil && pl.curve {
		sw := math.Floor(t.launchAt/1000) + 5400
		if sw >= start && sw < end {
			out.PoolSwitches = append(out.PoolSwitches, api.PoolSwitch{T: int64(sw), FromPool: ptr(curvePool(t)), ToPool: mainPool(t), Reason: "graduation"})
		}
	}
	return out, nil
}
