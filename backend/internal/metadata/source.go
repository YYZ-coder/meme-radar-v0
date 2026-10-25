package metadata

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/solana"
)

// ErrNoSource means the chain has no metadata URI for this token.
var ErrNoSource = errors.New("metadata: no source")

// ChainSource looks up a missing URI on the configured chain's RPC budget.
type ChainSource struct {
	family string
	rpc    *jsonrpc.Client
}

// NewChainSource uses a trusted RPC endpoint (not an untrusted metadata URL).
func NewChainSource(family string, rpc *jsonrpc.Client) *ChainSource {
	return &ChainSource{family: family, rpc: rpc}
}

// URI reads Metaplex on Solana or Doppler tokenURI() on EVM.
func (s *ChainSource) URI(ctx context.Context, address []byte) (string, error) {
	if s.family == "solana" {
		if len(address) != 32 {
			return "", ErrNoSource
		}
		mint := solana.Key(address)
		pda, err := MetaplexPDA(mint)
		if err != nil {
			return "", err
		}
		result, err := s.rpc.Call(ctx, "getAccountInfo", []any{pda.String(), map[string]string{"encoding": "base64", "commitment": "confirmed"}})
		if err != nil {
			return "", err
		}
		var account struct {
			Value *struct {
				Owner string
				Data  []string
			}
		}
		if err := json.Unmarshal(result, &account); err != nil {
			return "", err
		}
		if account.Value == nil {
			return "", ErrNoSource
		}
		if account.Value.Owner != metaplexProgram.String() || len(account.Value.Data) != 2 || account.Value.Data[1] != "base64" {
			return "", fmt.Errorf("metadata: invalid Metaplex owner/encoding")
		}
		data, err := base64.StdEncoding.DecodeString(account.Value.Data[0])
		if err != nil {
			return "", err
		}
		meta, err := DecodeMetaplexMetadata(data)
		if err != nil {
			return "", err
		}
		if meta.Mint != mint {
			return "", fmt.Errorf("metadata: Metaplex mint mismatch")
		}
		if meta.URI == "" {
			return "", ErrNoSource
		}
		return meta.URI, nil
	}
	if s.family != "evm" || len(address) != 20 {
		return "", ErrNoSource
	}
	selector := evm.Keccak256([]byte("tokenURI()"))
	result, err := s.rpc.Call(ctx, "eth_call", []any{map[string]string{"to": "0x" + hex.EncodeToString(address), "data": "0x" + hex.EncodeToString(selector[:4])}, "latest"})
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) && (rpcErr.Code == 3 || strings.Contains(strings.ToLower(rpcErr.Message), "revert")) {
		return "", ErrNoSource
	}
	if err != nil {
		return "", err
	}
	var encoded string
	if err := json.Unmarshal(result, &encoded); err != nil {
		return "", err
	}
	if encoded == "0x" {
		return "", ErrNoSource
	}
	data, err := hex.DecodeString(strings.TrimPrefix(encoded, "0x"))
	if err != nil {
		return "", err
	}
	uri, err := abiURI(data)
	if err != nil {
		return "", err
	}
	if uri == "" {
		return "", ErrNoSource
	}
	return uri, nil
}

func abiURI(data []byte) (string, error) {
	bad := fmt.Errorf("metadata: invalid tokenURI ABI string")
	if len(data) < 64 {
		return "", bad
	}
	offset := new(big.Int).SetBytes(data[:32])
	if !offset.IsUint64() || offset.Uint64() > uint64(len(data)-32) {
		return "", bad
	}
	start := offset.Uint64()
	size := new(big.Int).SetBytes(data[start : start+32])
	if !size.IsUint64() || size.Uint64() > uint64(len(data))-start-32 {
		return "", bad
	}
	return strings.TrimSpace(string(data[start+32 : start+32+size.Uint64()])), nil
}
