package mockdata

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

type chainAnchor struct {
	anchor    float64
	perSecond float64
}

var anchors = map[api.Chain]chainAnchor{
	api.Solana: {457_713_550, 2.5},
	api.Base:   {52_690_400, 0.5},
	api.BSC:    {128_014_990, 2.2},
}

var anchorAt = float64(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC).UnixMilli())

func seedFailures() []api.ParseFailure {
	f := func(id string, chain api.Chain, module, position, err string) api.ParseFailure {
		return api.ParseFailure{ID: id, Chain: chain, Module: module, Position: position, Error: err, Attempts: 3, Status: "failed", LastAttemptAt: iso(adminStart + 3_600_000)}
	}
	return []api.ParseFailure{
		f("f1", api.Solana, "pumpswap", "457713912:118:2", "conservation check failed: quote delta"),
		f("f2", api.Base, "uniswap_v4", "52690612:41:0", "unknown hook 0x…a8c0 on pool"),
		f("f3", api.BSC, "fourmeme", "128015230:7:1", "decode: unknown event topic"),
	}
}

// Health reports stream cursors, decoder progress, failures and budget.
func (d *Data) Health(_ context.Context) (api.AdminHealth, error) {
	now := d.ms()
	lags := []float64{4, 9, 6}
	out := api.AdminHealth{AsOf: iso(now), Chains: []api.ChainHealth{}, Parsers: []api.ParserHealth{}}
	forward := map[api.Chain]int64{}
	for i, chain := range api.Chains {
		a := anchors[chain]
		elapsed := (now - anchorAt) / 1000
		head := int64(math.Floor(a.anchor + elapsed*a.perSecond))
		backlog := int64(jsRound(lags[i] * a.perSecond))
		lower := int64(math.Floor(a.anchor - 86400*a.perSecond)) // 24h backfill (D134)
		forward[chain] = head - backlog
		c := api.ChainHealth{
			Chain: chain, Head: ptr(head), Anchor: int64(a.anchor), LowerBound: lower,
			ForwardPosition: ptr(head - backlog), HistoryPosition: ptr(lower), BacklogBlocks: ptr(backlog),
			LagSeconds: ptr(int64(lags[i])), GapsOpen: []int64{1, 0, 2}[i],
			GapsByReason: []map[string]int64{{"stream_reconnect": 1}, {}, {"retryable": 2}}[i],
		}
		if chain == api.BSC {
			c.LastError = &api.LastError{Kind: "retryable", Message: "429 Too Many Requests from RPC provider", At: iso(now - 95_000)}
		}
		out.Chains = append(out.Chains, c)
	}
	d.lock()
	defer d.mu.Unlock()
	mods := [][3]string{{"solana", "pump", "1"}, {"solana", "pumpswap", "1"}, {"solana", "launchlab", "1"}, {"solana", "jupiter", "1"}, {"solana", "raydium_cpmm", "1"}, {"base", "clanker", "1"}, {"base", "uniswap_v4", "1"}, {"bsc", "fourmeme", "1"}, {"bsc", "pancakeswap", "1"}}
	quarantined := []int64{4, 9, 0, 0, 1, 2, 3, 0, 0}
	unknown := []int64{0, 1, 0, 6, 0, 0, 2, 0, 0}
	for i, m := range mods {
		var failures int64
		for _, f := range d.failures {
			if f.Module == m[1] && f.Status == "failed" {
				failures++
			}
		}
		chain := api.Chain(m[0])
		out.Parsers = append(out.Parsers, api.ParserHealth{
			Chain: chain, Module: m[1], Version: m[2], Position: ptr(forward[chain] - int64(i*3)), LagBlocks: ptr(int64(i * 3)),
			Quarantined: quarantined[i], Failures: failures, UnknownDiscriminators: unknown[i],
		})
	}
	out.Failures = append([]api.ParseFailure{}, d.failures...)
	out.Budget = api.Budget{Month: time.UnixMilli(int64(now)).UTC().Format("2006-01"), SpentUSD: "0", LimitUSD: "100", State: "normal"}
	return out, nil
}

// RetryFailure re-queues a failed decode task.
func (d *Data) RetryFailure(_ context.Context, id string) (api.ParseFailure, error) {
	now := d.lock()
	defer d.mu.Unlock()
	for i := range d.failures {
		if d.failures[i].ID == id {
			d.failures[i].Status = "queued"
			d.failures[i].LastAttemptAt = iso(now)
			return d.failures[i], nil
		}
	}
	return api.ParseFailure{}, api.Errorf(http.StatusNotFound, "not_found", "", "failure not found")
}
