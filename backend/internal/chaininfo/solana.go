package chaininfo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/solana"
)

// Solana answers read-only Solana RPC questions.
type Solana struct{ rpc *jsonrpc.Client }

func NewSolana(rpc *jsonrpc.Client) *Solana { return &Solana{rpc: rpc} }

// MintDecimals returns an SPL mint's decimals (getTokenSupply).
func (s *Solana) MintDecimals(ctx context.Context, mint []byte) (int16, error) {
	_, d, err := s.MintSupply(ctx, mint)
	return d, err
}

// MintSupply returns an SPL mint's current supply (atomic units) and decimals.
func (s *Solana) MintSupply(ctx context.Context, mint []byte) (string, int16, error) {
	res, err := s.rpc.Call(ctx, "getTokenSupply", []any{solana.EncodeBase58(mint)})
	if err != nil {
		return "", 0, err
	}
	var v struct {
		Value *struct {
			Amount   string
			Decimals int16
		}
	}
	if err := json.Unmarshal(res, &v); err != nil || v.Value == nil {
		return "", 0, fmt.Errorf("chaininfo: getTokenSupply %.80s: %v", res, err)
	}
	return v.Value.Amount, v.Value.Decimals, nil
}
