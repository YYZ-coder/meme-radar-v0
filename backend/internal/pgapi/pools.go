package pgapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/solana"
)

func tokenAddress(chain api.Chain, address string) ([]byte, error) {
	if chain == api.Solana {
		if key, err := solana.ParseKey(address); err == nil {
			return key[:], nil
		}
	} else if (chain == api.Base || chain == api.BSC) && len(address) == 42 && strings.HasPrefix(address, "0x") {
		if key, err := evm.ParseAddress(address); err == nil {
			return key[:], nil
		}
	}
	return nil, api.Errorf(http.StatusBadRequest, "invalid_param", "address", "malformed address")
}

// Pools returns original pool metadata and exact whole-unit reserves.
func (b *Backend) Pools(ctx context.Context, chain api.Chain, address string) (api.Pools, error) {
	token, err := tokenAddress(chain, address)
	if err != nil {
		return api.Pools{}, err
	}
	rows, found, err := b.st.APITokenPools(ctx, string(chain), token)
	if err != nil {
		return api.Pools{}, err
	}
	if !found {
		return api.Pools{}, api.Errorf(http.StatusNotFound, "not_found", "", "token not found")
	}
	out := api.Pools{Items: make([]api.Pool, 0, len(rows))}
	for _, p := range rows {
		out.Items = append(out.Items, api.Pool{Address: displayAddress(string(chain), p.ID), Dex: p.Dex, Protocol: p.Protocol,
			QuoteSymbol: p.QuoteSymbol, LiquidityUSD: p.LiquidityUSD, ReserveToken: p.ReserveToken, ReserveQuote: p.ReserveQuote,
			CreatedAt: optionalTime(p.CreatedAt), Status: p.Status, IsMain: p.IsMain})
	}
	return out, nil
}
