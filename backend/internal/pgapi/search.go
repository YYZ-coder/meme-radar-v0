package pgapi

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/solana"
)

func displayAddress(chain string, raw []byte) string {
	if chain == string(api.Solana) {
		return solana.EncodeBase58(raw)
	}
	return "0x" + hex.EncodeToString(raw)
}

func optionalTime(at *time.Time) *string {
	if at == nil {
		return nil
	}
	s := at.UTC().Format("2006-01-02T15:04:05.000Z")
	return &s
}

// Search uses exact chain-shaped addresses or a literal case-insensitive prefix.
func (b *Backend) Search(ctx context.Context, q string, chain api.Chain, limit int) (api.Search, error) {
	var family string
	var address []byte
	if len(q) == 42 && strings.HasPrefix(q, "0x") {
		if a, err := evm.ParseAddress(q); err == nil {
			family, address = "evm", a[:]
		}
	}
	if family == "" {
		if a, err := solana.ParseKey(q); err == nil {
			family, address = "solana", a[:]
		}
	}
	rows, err := b.st.APISearchTokens(ctx, q, string(chain), family, address, limit, b.now())
	if err != nil {
		return api.Search{}, err
	}
	out := api.Search{Tokens: make([]api.SearchToken, 0, len(rows)), Wallets: []api.SearchWallet{}}
	for _, r := range rows {
		out.Tokens = append(out.Tokens, api.SearchToken{TokenRef: api.TokenRef{
			Chain: api.Chain(r.Chain), Address: displayAddress(r.Chain, r.Address), Symbol: r.Symbol, Name: r.Name,
			LaunchPlatform: r.LaunchPlatform, LaunchAt: optionalTime(r.LaunchAt)},
			MarketCapUSD: r.MarketCapUSD, AgeSeconds: r.AgeSeconds, LastTradeAt: optionalTime(r.LastTradeAt)})
	}
	wallets, err := b.st.SearchListWallets(ctx, q, string(chain), family, address)
	if err != nil {
		return api.Search{}, err
	}
	for _, w := range wallets {
		out.Wallets = append(out.Wallets, api.SearchWallet{Chain: api.Chain(w.Chain), Address: displayAddress(w.Chain, w.Address), Label: w.Label, IsSmartMoney: !w.Excluded})
	}
	return out, nil
}
