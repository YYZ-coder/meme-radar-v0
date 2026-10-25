package compute

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/solana"
	"github.com/meme-radar/radar/internal/store"
)

// Decimals reads a token's decimals on chain.
type Decimals interface {
	Decimals(ctx context.Context, asset []byte) (int16, error)
}

// stablecoins are priced at 1 USD (D135; USD1 per D149).
var stablecoins = map[string]bool{"USDC": true, "USDT": true, "USD1": true}

// QuoteAssets registers a chain's configured quote assets: the native coin
// (EVM zero address, 18 decimals) and its wrapped token are priced as the
// native symbol, USDC/USDT/USD1 as stablecoins; decimals are read on chain.
func QuoteAssets(ctx context.Context, ch config.Chain, look Decimals) ([]store.QuoteAsset, error) {
	addr := func(s string) ([]byte, error) {
		if ch.Family == "solana" {
			return solana.DecodeBase58(s)
		}
		a, err := evm.ParseAddress(s)
		return a[:], err
	}
	wrapped, err := addr(ch.WrappedNative)
	if err != nil {
		return nil, fmt.Errorf("compute: %s wrapped_native: %w", ch.Name, err)
	}
	symbols := make([]string, 0, len(ch.QuoteAssets))
	for s := range ch.QuoteAssets {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	var out []store.QuoteAsset
	for _, sym := range symbols {
		a, err := addr(ch.QuoteAssets[sym])
		if err != nil {
			return nil, fmt.Errorf("compute: %s quote asset %s: %w", ch.Name, sym, err)
		}
		q := store.QuoteAsset{Chain: ch.Name, Asset: a, Symbol: sym}
		switch {
		case ch.Family == "evm" && bytes.Equal(a, make([]byte, len(a))):
			q.Symbol, q.Kind, q.Decimals = ch.Native, "native", 18
			out = append(out, q)
			continue
		case bytes.Equal(a, wrapped) || sym == ch.Native:
			q.Symbol, q.Kind = ch.Native, "native"
		case stablecoins[sym]:
			q.Kind = "stable"
		default:
			return nil, fmt.Errorf("compute: %s quote asset %s is neither the native coin nor a stablecoin", ch.Name, sym)
		}
		if q.Decimals, err = look.Decimals(ctx, a); err != nil {
			return nil, fmt.Errorf("compute: %s %s decimals: %w", ch.Name, sym, err)
		}
		out = append(out, q)
	}
	return out, nil
}
