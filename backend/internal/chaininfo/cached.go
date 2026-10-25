package chaininfo

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/store"
)

// Cached answers from store.TokenLookups first and asks rpc only once per
// token; results never change, negative Four.meme answers included.
type Cached struct {
	chain string
	store *store.Store
	rpc   *RPC
	mem   sync.Map // kind + address → decoded answer
}

// NewCached returns a cached lookup for chain.
func NewCached(chain string, st *store.Store, rpc *RPC) *Cached {
	return &Cached{chain: chain, store: st, rpc: rpc}
}

// FourMeme implements fourmeme.Lookup.
func (c *Cached) FourMeme(ctx context.Context, token evm.Address) (FourMeme, error) {
	return cached(ctx, c, "four_meme", token, c.rpc.FourMeme)
}

// ERC20 implements fourmeme.Lookup.
func (c *Cached) ERC20(ctx context.Context, token evm.Address) (ERC20, error) {
	return cached(ctx, c, "erc20", token, c.rpc.ERC20)
}

func cached[T any](ctx context.Context, c *Cached, kind string, token evm.Address, ask func(context.Context, evm.Address) (T, error)) (T, error) {
	return cachedKey(ctx, c, kind, token[:], func(ctx context.Context) (T, error) { return ask(ctx, token) })
}

func cachedKey[T any](ctx context.Context, c *Cached, kind string, id []byte, ask func(context.Context) (T, error)) (T, error) {
	key := kind + string(id)
	if v, ok := c.mem.Load(key); ok {
		return v.(T), nil
	}
	var v T
	raw, ok, err := c.store.TokenLookup(ctx, c.chain, kind, id)
	if err != nil {
		return v, err
	}
	if ok {
		if err := json.Unmarshal(raw, &v); err != nil {
			return v, fmt.Errorf("chaininfo: cached %s %x: %w", kind, id, err)
		}
		c.mem.Store(key, v)
		return v, nil
	}
	if v, err = ask(ctx); err != nil {
		return v, err
	}
	raw, err = json.Marshal(v)
	if err != nil {
		return v, err
	}
	if err := c.store.SaveTokenLookup(ctx, c.chain, kind, id, raw); err != nil {
		return v, err
	}
	c.mem.Store(key, v)
	return v, nil
}

// InfinityPoolKey implements pancake.Lookup.
func (c *Cached) InfinityPoolKey(ctx context.Context, manager evm.Address, id evm.Hash) (PoolKey, error) {
	return cachedKey(ctx, c, "infinity_pool", id[:], func(ctx context.Context) (PoolKey, error) {
		return c.rpc.InfinityPoolKey(ctx, manager, id)
	})
}

// DopplerAsset implements the Doppler lookup.
func (c *Cached) DopplerAsset(ctx context.Context, airlock, token evm.Address) (DopplerAsset, error) {
	return cached(ctx, c, "doppler_asset", token, func(ctx context.Context, t evm.Address) (DopplerAsset, error) {
		return c.rpc.DopplerAsset(ctx, airlock, t)
	})
}
