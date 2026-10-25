package mockdata

import (
	"context"
	"math"
	"slices"

	"github.com/meme-radar/radar/internal/api"
)

// activityAt is the k-th most recent activity before anchor (ms).
func (d *Data) activityAt(t *token, k int, anchor float64) (api.Activity, float64) {
	gap := math.Max(1, jsRound(3600/math.Max(1, t.tradesPerHour)))
	at := anchor - float64(k)*gap*1000 - math.Floor(unit(t.address+":gap:"+num(float64(k)))*gap*500)
	key := num(anchor) + ":" + num(float64(k))
	r := rng(hash(t.address + ":act:" + key))
	price := priceAt(t, at)
	liquidity := k%17 == 9
	buy := r() < 0.56
	scale := 0.2
	if t.baseLiquidity != nil {
		scale = 1
	}
	value := (20 + r()*r()*4000) * scale
	pools := poolsOf(t, at)
	active := 0
	for _, p := range pools {
		if p.Status == "active" {
			active++
		}
	}
	pool := pools[k%max(1, active)]
	wallet := d.walletFor(t, num(math.Floor(at/1000))+":"+num(float64(k)), 0.06)
	tokenAmount := value * 1000
	if price != nil && *price != 0 {
		tokenAmount = value / *price
	}
	a := api.Activity{
		ID:   num(math.Floor(at/400)) + ":" + num(float64(k%200)) + ":" + map[bool]string{true: "1", false: "0"}[liquidity],
		Time: iso(at), TokenAmount: dec(tokenAmount, 10), QuoteAmount: ptr(dec(value/nativeUSD[t.chain], 8)),
		QuoteSymbol: pool.QuoteSymbol, TxHash: txHash(string(t.chain), t.address+":"+key), Pool: pool.Address, Dex: pool.Dex,
	}
	if price != nil {
		a.ValueUSD = ptr(dec(value, 8))
	}
	if liquidity {
		a.Kind = map[bool]string{true: "add", false: "remove"}[buy]
		a.Wallet = &wallet
		return a, at // price, market cap and attribution are null for liquidity rows (data model §8.2)
	}
	a.Kind = map[bool]string{true: "buy", false: "sell"}[buy]
	if r() >= 0.04 {
		a.Wallet = &wallet
		a.Attribution = ptr(map[bool]string{true: "confirmed", false: "inferred"}[r() < 0.9])
	}
	if price != nil {
		a.PriceUSD = ptr(dec(*price, 8))
		a.MarketCapUSD = ptr(dec(*price*t.supply, 8))
	}
	return a, at
}

// Activities lists trades and liquidity changes newest first.
func (d *Data) Activities(_ context.Context, chain api.Chain, address string, q api.ActivityQuery) (api.Activities, error) {
	t, err := d.token(chain, address)
	if err != nil {
		return api.Activities{}, err
	}
	now := d.ms()
	anchor, offset, err := decodeCursor(q.Cursor)
	if err != nil {
		return api.Activities{}, err
	}
	if q.Cursor == "" {
		anchor = math.Floor(now/5000) * 5000
	}
	items := []api.Activity{}
	k := int(offset)
	maxScan := k + q.Limit*60
	for ; k < maxScan && len(items) < q.Limit; k++ {
		a, at := d.activityAt(t, k, anchor)
		if at < t.launchAt || (q.From != nil && at < float64(*q.From)*1000) {
			break
		}
		if q.To != nil && at >= float64(*q.To)*1000 {
			continue
		}
		trade := a.Kind == "buy" || a.Kind == "sell"
		if (q.Kind == "trade" && !trade) || (q.Kind == "liquidity" && trade) {
			continue
		}
		if q.Label != "all" && (a.Wallet == nil || !slices.Contains(a.Wallet.Labels, q.Label)) {
			continue
		}
		items = append(items, a)
	}
	out := api.Activities{Coverage: "complete", LabelCountsCoverage: "complete", Items: items}
	if len(items) == q.Limit && k < maxScan {
		out.NextCursor = encodeCursor(anchor, float64(k))
	}
	total := metrics(t, "24h", now).trades
	out.LabelCounts = map[string]*int64{"all": ptr(total), "smart_money": ptr(int64(jsRound(float64(total) * t.smShare)))}
	return out, nil
}

// LiquidityEvents lists liquidity changes newest first.
func (d *Data) LiquidityEvents(ctx context.Context, chain api.Chain, address, cursor string, limit int) (api.LiquidityEvents, error) {
	res, err := d.Activities(ctx, chain, address, api.ActivityQuery{Kind: "liquidity", Label: "all", Cursor: cursor, Limit: limit})
	if err != nil {
		return api.LiquidityEvents{}, err
	}
	out := api.LiquidityEvents{Coverage: res.Coverage, Items: []api.LiquidityEvent{}, NextCursor: res.NextCursor}
	for _, a := range res.Items {
		out.Items = append(out.Items, api.LiquidityEvent{ID: a.ID, Time: a.Time, Kind: a.Kind, TokenAmount: a.TokenAmount, QuoteAmount: a.QuoteAmount, QuoteSymbol: a.QuoteSymbol, ValueUSD: a.ValueUSD, Wallet: a.Wallet, TxHash: a.TxHash, Pool: a.Pool, Dex: a.Dex})
	}
	return out, nil
}

// Holders ranks current balances. Average prices and PnL stay null until M2 (data model §8.2).
func (d *Data) Holders(_ context.Context, chain api.Chain, address, label, cursor string, limit int) (api.Holders, error) {
	t, err := d.token(chain, address)
	if err != nil {
		return api.Holders{}, err
	}
	_, off, err := decodeCursor(cursor)
	if err != nil {
		return api.Holders{}, err
	}
	now := d.ms()
	cov := coverage(t, now)
	total := t.holders
	if label == "smart_money" {
		total = min(t.smHolders, int64(len(d.smWallets(t.chain))))
	}
	n := int(min(total, 500))
	weights := make([]float64, n)
	sum := 0.0
	for i := range weights {
		weights[i] = 1 / math.Pow(float64(i)+1.6, 1.15)
		sum += weights[i]
	}
	sum /= 0.82
	out := api.Holders{Coverage: cov, Items: []api.Holder{}}
	if cov.Status == "full" {
		out.Total = ptr(total)
	}
	offset := int(off)
	for i := offset; i < min(offset+limit, n); i++ {
		share := weights[i] / sum * 100
		var w api.WalletRef
		if label == "smart_money" {
			w = *d.smWalletAt(t, i)
		} else {
			w = d.walletFor(t, "holder-"+num(float64(i)), 0)
		}
		r := rng(hash(t.address + ":h:" + num(float64(i))))
		h := api.Holder{Rank: int64(i + 1), Wallet: w, Amount: dec(share/100*t.supply, 10), SharePct: ptr(dec(share, 6))}
		if r() >= 0.15 {
			h.NativeBalance = ptr(dec(r()*r()*400, 6))
		}
		out.Items = append(out.Items, h)
	}
	if offset+limit < n {
		out.NextCursor = encodeCursor(0, float64(offset+limit))
	}
	return out, nil
}

// TokenSmartMoney lists smart-money holders of a token with 24h buy/sell volume.
func (d *Data) TokenSmartMoney(_ context.Context, chain api.Chain, address string) (api.TokenSmartMoney, error) {
	t, err := d.token(chain, address)
	if err != nil {
		return api.TokenSmartMoney{}, err
	}
	now := d.ms()
	m := metrics(t, "24h", now)
	count := int(min(t.smHolders, 12, int64(len(d.smWallets(t.chain)))))
	out := api.TokenSmartMoney{ListVersion: d.activeVersion(), Holders: ptr(t.smHolders), Items: []api.TokenSmartMoneyEntry{}}
	for i := 0; i < count; i++ {
		r := rng(hash(t.address + ":smh:" + num(float64(i))))
		share := 2.4 / float64(i+1) * (0.6 + r())
		e := api.TokenSmartMoneyEntry{Wallet: *d.smWalletAt(t, i), Balance: ptr(dec(share/100*t.supply, 10)), SharePct: ptr(dec(share, 6))}
		if m.smBuyVolume != nil {
			e.BuyVolumeUSD = ptr(dec(*m.smBuyVolume/float64(count)*(0.5+r()), 8))
			e.SellVolumeUSD = ptr(dec(*m.smSellVolume/float64(count)*r(), 8))
		}
		e.LastTradeAt = ptr(iso(now - math.Floor(r()*6*3_600_000)))
		out.Items = append(out.Items, e)
	}
	return out, nil
}
