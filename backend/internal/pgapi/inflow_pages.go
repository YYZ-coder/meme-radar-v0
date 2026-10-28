package pgapi

import (
	"context"
	"errors"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/store"
)

// InflowTradePages serves unfiltered descending smart-trade order using bounded
// server pagination. Query metadata and later pages pin immutable publications.
func (b *Backend) InflowTradePages(ctx context.Context, chain api.Chain, window string, native bool, limit int) (api.InflowPages, error) {
	data, err := b.st.ReadInflowTradePages(ctx, string(chain), window, native, limit)
	if errors.Is(err, store.ErrProjectionPending) {
		return api.InflowPages{}, unavailable()
	}
	if err != nil {
		return api.InflowPages{}, err
	}
	proj, err := b.inflowProjection(data.InflowSnapshot, chain, native)
	if err != nil {
		return api.InflowPages{}, err
	}
	first := make([]api.InflowRow, len(proj.Rows))
	for i, v := range proj.Rows {
		first[i] = v.Row
	}
	out := api.InflowPages{AsOf: proj.AsOf, ChainAsOf: proj.ChainAsOf, ListVersion: proj.ListVersion, Total: min(data.Count, 2000), Truncated: data.Count > 2000}
	heads := append([]store.InflowHead(nil), data.Heads...)
	total := out.Total
	out.LoadPage = func(ctx context.Context, offset, limit int) ([]api.InflowRow, error) {
		end := min(offset+limit, total)
		if offset >= end {
			return []api.InflowRow{}, nil
		}
		if end <= len(first) {
			return append([]api.InflowRow{}, first[offset:end]...), nil
		}
		values, err := b.st.ReadInflowTradePage(ctx, window, native, heads, offset, limit)
		if err != nil {
			return nil, err
		}
		rows := make([]api.InflowRow, 0, len(values))
		for _, v := range values {
			r, err := b.inflowWireRow(v, native)
			if err != nil {
				return nil, err
			}
			rows = append(rows, r.Row)
		}
		return rows, nil
	}
	return out, nil
}
