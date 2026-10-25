package mockdata

import (
	"context"

	"github.com/meme-radar/radar/internal/api"
)

// InflowProjection returns every token traded in window, for chain ("" = all).
func (d *Data) InflowProjection(_ context.Context, chain api.Chain, window string, native bool) (api.Projection, error) {
	now := d.ms()
	p := api.Projection{AsOf: iso(now), ListVersion: d.activeVersion()}
	for _, t := range universe {
		if chain != "" && t.chain != chain {
			continue
		}
		row := inflowRow(t, window, native, now)
		if row.Trades == nil || *row.Trades == 0 {
			continue // the list only carries tokens traded in the window
		}
		p.Rows = append(p.Rows, api.ProjectionRow{Row: row, DexIDs: t.dexes, Platform: t.platform, DevHoldingPct: t.devHoldingPct})
	}
	return p, nil
}

// InflowRows returns current rows for the given tokens; unknown or untraded tokens are omitted.
func (d *Data) InflowRows(_ context.Context, window string, native bool, keys []api.TokenKey) ([]api.InflowRow, error) {
	now := d.ms()
	out := []api.InflowRow{}
	for _, k := range keys {
		if t := findToken(k.Chain, k.Address); t != nil {
			out = append(out, inflowRow(t, window, native, now))
		}
	}
	return out, nil
}
