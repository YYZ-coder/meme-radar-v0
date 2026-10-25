package mockdata

import (
	"context"
	"math"
	"sort"
	"strconv"

	"github.com/meme-radar/radar/internal/api"
)

// ActiveListVersion returns the active smart-money list version.
func (d *Data) ActiveListVersion(_ context.Context) (string, error) { return d.activeVersion(), nil }

func (d *Data) activeVersion() string {
	d.lock()
	defer d.mu.Unlock()
	return strconv.Itoa(d.active)
}

// Overview returns the smart-money summary cards for chain ("" = all) and window.
func (d *Data) Overview(_ context.Context, chain api.Chain, window string) (api.Overview, error) {
	now := d.ms()
	seconds := windowSeconds[window]
	var net, prev, buy, sell float64
	var traded, fresh int64
	bars := make([]float64, 16)
	for _, t := range universe {
		if chain != "" && t.chain != chain {
			continue
		}
		m := metrics(t, window, now)
		if m.trades == 0 {
			continue
		}
		traded++
		if now-t.launchAt < seconds*1000 {
			fresh++
		}
		val := func(p *float64) float64 {
			if p == nil {
				return 0
			}
			return *p
		}
		net += val(m.netInflow)
		buy += val(m.smBuyVolume)
		sell += val(m.smSellVolume)
		prev += val(metrics(t, window, now-seconds*1000).netInflow)
		share := val(m.netInflow) / 16
		for i := range bars {
			bars[i] += share * (0.4 + 1.2*unit(t.address+":bar:"+num(float64(i))+":"+window))
		}
	}
	d.lock()
	var members int
	for _, m := range d.members {
		if !m.Excluded && (chain == "" || m.Chain == chain) {
			members++
		}
	}
	d.mu.Unlock()
	o := api.Overview{
		AsOf: iso(now), ListVersion: d.activeVersion(), Window: window, Coverage: "complete",
		NetInflowUSD:  ptr(dec(net, 10)),
		ActiveWallets: ptr(int64(jsRound(float64(members) * 9 * math.Min(1, 0.25+seconds/14400)))),
		TrackedTokens: ptr(traded), NewTokens: ptr(fresh),
		BuyVolumeUSD: ptr(dec(buy, 10)), SellVolumeUSD: ptr(dec(sell, 10)),
	}
	if prev != 0 {
		o.NetInflowChangePct = ptr(dec((net-prev)/math.Abs(prev)*100, 6))
	}
	if buy+sell != 0 {
		o.BuySharePct = ptr(dec(buy/(buy+sell)*100, 6))
	}
	// Per-token totals cannot be split by single-trade size; the split stays unknown (D131).
	for _, b := range bars {
		o.InflowBars = append(o.InflowBars, ptr(dec(b, 8)))
	}
	return o, nil
}

// Facets returns DEX and launch platform options with window volume.
func (d *Data) Facets(_ context.Context, chain api.Chain, window string) (api.Facets, error) {
	now := d.ms()
	agg := func(ids func(*token) []string, list []api.Facet) []api.Facet {
		out := []api.Facet{}
		vols := map[string]float64{}
		for _, x := range list {
			if chain != "" && x.Chain != chain {
				continue
			}
			var volume float64
			var count int64
			for _, t := range universe {
				match := false
				for _, id := range ids(t) {
					match = match || id == x.ID
				}
				if !match {
					continue
				}
				m := metrics(t, window, now)
				if m.trades == 0 {
					continue
				}
				count++
				if m.volume != nil {
					volume += *m.volume
				}
			}
			x.VolumeUSD, x.TokenCount, x.ObservedTokenCount = ptr(dec(volume, 10)), ptr(count), count
			vols[x.ID] = volume
			out = append(out, x)
		}
		sort.SliceStable(out, func(i, j int) bool { return vols[out[i].ID] > vols[out[j].ID] })
		return out
	}
	var dexList, platList []api.Facet
	for _, x := range dexes {
		dexList = append(dexList, api.Facet{ID: x.id, Name: x.name, Chain: x.chain})
	}
	for _, x := range platforms {
		platList = append(platList, api.Facet{ID: x.id, Name: x.name, Chain: x.chain})
	}
	return api.Facets{
		Coverage:  "complete",
		Dexes:     agg(func(t *token) []string { return t.dexes }, dexList),
		Platforms: agg(func(t *token) []string { return []string{t.platform} }, platList),
	}, nil
}
