package mockdata

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/meme-radar/radar/internal/api"
)

// NativePrices returns the bottom-bar native coin prices.
func (d *Data) NativePrices(_ context.Context) (api.NativePrices, error) {
	now := d.ms()
	changes := []float64{2.84, -1.12, 0.64}
	out := api.NativePrices{}
	for i, chain := range api.Chains {
		drift := 1 + 0.004*math.Sin(now/600_000+float64(len(chain)))
		out.Items = append(out.Items, api.NativePrice{
			Chain: chain, Symbol: nativeSymbol[chain],
			PriceUSD:     ptr(dec(nativeUSD[chain]*drift, 8)),
			Change24hPct: ptr(dec(changes[i]+drift-1, 4)),
			AsOf:         iso(math.Floor(now/60_000) * 60_000),
		})
	}
	return out, nil
}

func searchToken(t *token, now float64) api.SearchToken {
	m := metrics(t, "24h", now)
	return api.SearchToken{
		TokenRef:     tokenRef(t),
		MarketCapUSD: decp(m.marketCap, 8),
		AgeSeconds:   ptr(int64(math.Floor((now - t.launchAt) / 1000))),
		LastTradeAt:  ptr(iso(now - math.Floor(unit(t.address)*90_000))),
	}
}

// Search matches tokens by address, symbol or name and wallets by address or label.
func (d *Data) Search(_ context.Context, q string, chain api.Chain, limit int) (api.Search, error) {
	now := d.ms()
	lower := strings.ToLower(q)
	type hit struct {
		t     *token
		score int
	}
	var hits []hit
	for _, t := range universe {
		if chain != "" && t.chain != chain {
			continue
		}
		addr := strings.ToLower(t.address)
		sym := strings.ToLower(t.symbol)
		score := -1
		switch {
		case addr == lower:
			score = 0
		case sym == lower:
			score = 1
		case strings.HasPrefix(sym, lower):
			score = 2
		case strings.Contains(strings.ToLower(t.name), lower):
			score = 3
		case strings.HasPrefix(addr, lower) && len(lower) >= 4:
			score = 4
		}
		if score >= 0 {
			hits = append(hits, hit{t, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return hits[i].t.holders > hits[j].t.holders
	})
	out := api.Search{Tokens: []api.SearchToken{}, Wallets: []api.SearchWallet{}}
	for i := 0; i < len(hits) && i < limit; i++ {
		out.Tokens = append(out.Tokens, searchToken(hits[i].t, now))
	}
	d.lock()
	defer d.mu.Unlock()
	for _, m := range d.members {
		if len(out.Wallets) == 5 {
			break
		}
		label := ""
		if m.Label != nil {
			label = strings.ToLower(*m.Label)
		}
		if (chain == "" || m.Chain == chain) && (strings.ToLower(m.Address) == lower || strings.Contains(label, lower)) {
			out.Wallets = append(out.Wallets, api.SearchWallet{Chain: m.Chain, Address: m.Address, Label: m.Label, IsSmartMoney: !m.Excluded})
		}
	}
	return out, nil
}
