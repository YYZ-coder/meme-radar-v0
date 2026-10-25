package pgapi

import (
	"context"
	"net/http"
	"time"

	"github.com/meme-radar/radar/internal/api"
)

func unixTime(seconds *int64) *time.Time {
	if seconds == nil {
		return nil
	}
	at := time.Unix(*seconds, 0).UTC()
	return &at
}

// Activities returns observed trades and liquidity facts; labels await H3/H4.
func (b *Backend) Activities(ctx context.Context, chain api.Chain, address string, q api.ActivityQuery) (api.Activities, error) {
	token, _, err := b.checkedToken(ctx, chain, address, true)
	if err != nil {
		return api.Activities{}, err
	}
	after, err := decodePosition(q.Cursor)
	if err != nil {
		return api.Activities{}, err
	}
	rows, found, err := b.st.APIActivities(ctx, string(chain), token, after, q.Kind, unixTime(q.From), unixTime(q.To), q.Label == "" || q.Label == "all", q.Limit+1)
	if err != nil {
		return api.Activities{}, err
	}
	if !found {
		return api.Activities{}, api.Errorf(http.StatusNotFound, "not_found", "", "token not found")
	}
	coverage, err := b.recentCoverage(ctx, chain)
	if err != nil {
		return api.Activities{}, err
	}
	out := api.Activities{Coverage: coverage, LabelCountsCoverage: "partial", LabelCounts: map[string]*int64{"all": nil, "smart_money": nil}, Items: []api.Activity{}}
	if len(rows) > q.Limit {
		out.NextCursor, err = encodePosition(rows[q.Limit-1].Position)
		if err != nil {
			return api.Activities{}, err
		}
		rows = rows[:q.Limit]
	}
	for _, r := range rows {
		if r.TokenAmount == nil {
			return api.Activities{}, unavailable()
		}
		out.Items = append(out.Items, api.Activity{ID: positionID(r.Position), Time: r.Position.Time.Format("2006-01-02T15:04:05.000Z"), Kind: r.Kind,
			TokenAmount: *r.TokenAmount, QuoteAmount: r.QuoteAmount, QuoteSymbol: r.QuoteSymbol, ValueUSD: r.ValueUSD, Wallet: walletRef(chain, r.Wallet),
			TxHash: displayAddress(string(chain), r.TxHash), Pool: displayAddress(string(chain), r.Pool), Dex: r.Dex, Attribution: r.Attribution, PriceUSD: r.PriceUSD, MarketCapUSD: r.MarketCapUSD})
	}
	return out, nil
}
