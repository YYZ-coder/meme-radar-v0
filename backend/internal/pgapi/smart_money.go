package pgapi

import (
	"context"
	"strconv"

	"github.com/meme-radar/radar/internal/api"
)

// TokenSmartMoney serves active members with positive observed token balances.
// Buy/sell amounts use H4's active-version 24-hour trade copies.
func (b *Backend) TokenSmartMoney(ctx context.Context, chain api.Chain, address string) (api.TokenSmartMoney, error) {
	token, _, err := b.checkedToken(ctx, chain, address, true)
	if err != nil {
		return api.TokenSmartMoney{}, err
	}
	data, err := b.st.APISmartMoney(ctx, string(chain), token, b.now().UTC())
	if err != nil {
		return api.TokenSmartMoney{}, err
	}
	out := api.TokenSmartMoney{ListVersion: strconv.FormatInt(data.Version, 10), Items: []api.TokenSmartMoneyEntry{}}
	if data.Known {
		count := int64(len(data.Rows))
		out.Holders = &count
	}
	for _, row := range data.Rows {
		wallet := *walletRef(chain, row.Owner)
		if row.Dev {
			wallet.Labels = append(wallet.Labels, "dev")
		}
		wallet.Labels = append(wallet.Labels, "smart_money")
		wallet.DisplayName = row.Name
		out.Items = append(out.Items, api.TokenSmartMoneyEntry{Wallet: wallet, Balance: row.Amount, SharePct: row.SharePct, LastTradeAt: optionalTime(row.LastTradeAt), BuyVolumeUSD: row.BuyVolumeUSD, SellVolumeUSD: row.SellVolumeUSD})
	}
	return out, nil
}
