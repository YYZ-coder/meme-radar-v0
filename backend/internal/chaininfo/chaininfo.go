// Package chaininfo answers read-only questions about EVM tokens with
// eth_call at the latest block (Four.meme token info, ERC-20 metadata),
// for decoders that cannot learn them from logs. Answers never change for a
// token, so callers cache them (store.TokenLookups).
package chaininfo

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
)

// ErrReverted reports a call the contract rejected (e.g. not an ERC-20).
var ErrReverted = errors.New("chaininfo: call reverted")

var (
	selGetTokenInfo = evm.Keccak256([]byte("getTokenInfo(address)"))
	selDecimals     = evm.Keccak256([]byte("decimals()"))
	selSymbol       = evm.Keccak256([]byte("symbol()"))
)

// call runs eth_call at the latest block and returns the raw return data.
func (r *RPC) call(ctx context.Context, to evm.Address, data []byte) ([]byte, error) {
	res, err := r.rpc.Call(ctx, "eth_call", []any{map[string]string{"to": to.Hex(), "data": "0x" + hex.EncodeToString(data)}, "latest"})
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) && (rpcErr.Code == 3 || strings.Contains(rpcErr.Message, "revert")) {
		return nil, fmt.Errorf("%w: %s", ErrReverted, rpcErr.Message)
	}
	if err != nil {
		return nil, err
	}
	var h string
	if err := json.Unmarshal(res, &h); err != nil {
		return nil, fmt.Errorf("chaininfo: eth_call result %.80s: %w", res, err)
	}
	return hex.DecodeString(strings.TrimPrefix(h, "0x"))
}

func word(b []byte, i int) []byte {
	if len(b) < 32*(i+1) {
		return nil
	}
	return b[32*i : 32*(i+1)]
}

func addressWord(w []byte) evm.Address {
	var a evm.Address
	if w != nil {
		copy(a[:], w[12:])
	}
	return a
}

// FourMeme is TokenManagerHelper3.getTokenInfo of a token.
type FourMeme struct {
	Found          bool // false: not a Four.meme token
	Version        int  // 1 or 2 (TokenManager V1 / TokenManager2)
	Manager        evm.Address
	Quote          evm.Address // zero for BNB
	LiquidityAdded bool        // graduated
}

// ERC20 is a token's metadata.
type ERC20 struct {
	Decimals uint8
	Symbol   string
}

// RPC answers from a JSON-RPC endpoint.
type RPC struct {
	rpc     *jsonrpc.Client
	helper3 evm.Address
}

// New returns an RPC lookup; helper3 is the Four.meme TokenManagerHelper3.
func New(rpc *jsonrpc.Client, helper3 evm.Address) *RPC { return &RPC{rpc: rpc, helper3: helper3} }

// FourMeme queries getTokenInfo(token).
func (r *RPC) FourMeme(ctx context.Context, token evm.Address) (FourMeme, error) {
	data := append(append([]byte{}, selGetTokenInfo[:4]...), make([]byte, 12)...)
	out, err := r.call(ctx, r.helper3, append(data, token[:]...))
	if errors.Is(err, ErrReverted) {
		return FourMeme{}, nil
	}
	if err != nil {
		return FourMeme{}, err
	}
	if len(out) < 12*32 {
		return FourMeme{}, fmt.Errorf("chaininfo: getTokenInfo returned %d bytes", len(out))
	}
	version := new(big.Int).SetBytes(word(out, 0))
	if version.Sign() == 0 || !version.IsInt64() {
		return FourMeme{}, nil
	}
	return FourMeme{Found: true, Version: int(version.Int64()), Manager: addressWord(word(out, 1)),
		Quote: addressWord(word(out, 2)), LiquidityAdded: new(big.Int).SetBytes(word(out, 11)).Sign() != 0}, nil
}

// ERC20 queries decimals() and symbol().
func (r *RPC) ERC20(ctx context.Context, token evm.Address) (ERC20, error) {
	d, err := r.call(ctx, token, selDecimals[:4])
	if err != nil {
		return ERC20{}, err
	}
	if len(d) < 32 || new(big.Int).SetBytes(d[:32]).Cmp(big.NewInt(255)) > 0 {
		return ERC20{}, fmt.Errorf("%w: decimals() returned %x", ErrReverted, d)
	}
	sym, err := r.call(ctx, token, selSymbol[:4])
	if err != nil {
		return ERC20{}, err
	}
	return ERC20{Decimals: d[31], Symbol: symbol(sym)}, nil
}

// symbol decodes an ABI string, or a bytes32 as some old tokens return.
func symbol(b []byte) string {
	if len(b) == 32 {
		return string(bytes.TrimRight(b, "\x00"))
	}
	off := new(big.Int).SetBytes(word(b, 0))
	if !off.IsInt64() || off.Int64()+32 > int64(len(b)) {
		return ""
	}
	o := int(off.Int64())
	n := new(big.Int).SetBytes(b[o : o+32])
	if !n.IsInt64() || o+32+int(n.Int64()) > len(b) {
		return ""
	}
	return string(b[o+32 : o+32+int(n.Int64())])
}

// PoolKey is a PancakeSwap Infinity pool key; a zero currency is the native coin.
type PoolKey struct {
	Currency0, Currency1 evm.Address
	Hooks                evm.Address
	Fee                  uint32
}

// InfinityPoolKey queries CLPoolManager.poolIdToPoolKey(id).
func (r *RPC) InfinityPoolKey(ctx context.Context, manager evm.Address, id evm.Hash) (PoolKey, error) {
	out, err := r.call(ctx, manager, append(append([]byte{}, selPoolKey[:4]...), id[:]...))
	if err != nil {
		return PoolKey{}, err
	}
	if len(out) < 5*32 {
		return PoolKey{}, fmt.Errorf("chaininfo: poolIdToPoolKey returned %d bytes", len(out))
	}
	// (currency0, currency1, hooks, poolManager, fee, parameters)
	return PoolKey{Currency0: addressWord(word(out, 0)), Currency1: addressWord(word(out, 1)), Hooks: addressWord(word(out, 2)),
		Fee: uint32(new(big.Int).SetBytes(word(out, 4)).Uint64())}, nil
}

var selPoolKey = evm.Keccak256([]byte("poolIdToPoolKey(bytes32)"))

// DopplerAsset is Airlock.getAssetData of a token.
type DopplerAsset struct {
	Found         bool // false: not a Doppler asset
	Numeraire     evm.Address
	Migrator      evm.Address
	Initializer   evm.Address
	Pool          evm.Address // the initial pool, or its hook for v4 initializers
	MigrationPool evm.Address
}

// DopplerAsset queries Airlock.getAssetData(token).
func (r *RPC) DopplerAsset(ctx context.Context, airlock, token evm.Address) (DopplerAsset, error) {
	data := append(append([]byte{}, selAssetData[:4]...), make([]byte, 12)...)
	out, err := r.call(ctx, airlock, append(data, token[:]...))
	if errors.Is(err, ErrReverted) {
		return DopplerAsset{}, nil
	}
	if err != nil {
		return DopplerAsset{}, err
	}
	if len(out) < 7*32 {
		return DopplerAsset{}, fmt.Errorf("chaininfo: getAssetData returned %d bytes", len(out))
	}
	// (numeraire, timelock, governance, liquidityMigrator, poolInitializer, pool, migrationPool, …)
	a := DopplerAsset{Numeraire: addressWord(word(out, 0)), Migrator: addressWord(word(out, 3)),
		Initializer: addressWord(word(out, 4)), Pool: addressWord(word(out, 5)), MigrationPool: addressWord(word(out, 6))}
	a.Found = a.Initializer != (evm.Address{})
	return a, nil
}

var selAssetData = evm.Keccak256([]byte("getAssetData(address)"))

// BalanceOf queries token.balanceOf(holder) at the latest block.
func (r *RPC) BalanceOf(ctx context.Context, token, holder evm.Address) (*big.Int, error) {
	sel := evm.Topic("balanceOf(address)")
	data := append(sel[:4:4], make([]byte, 12)...)
	b, err := r.call(ctx, token, append(data, holder[:]...))
	if err != nil {
		return nil, err
	}
	if len(b) < 32 {
		return nil, fmt.Errorf("%w: balanceOf returned %x", ErrReverted, b)
	}
	return new(big.Int).SetBytes(b[:32]), nil
}

// TotalSupply queries token.totalSupply() at the latest block.
func (r *RPC) TotalSupply(ctx context.Context, token evm.Address) (*big.Int, error) {
	sel := evm.Topic("totalSupply()")
	b, err := r.call(ctx, token, sel[:4])
	if err != nil {
		return nil, err
	}
	if len(b) < 32 {
		return nil, fmt.Errorf("%w: totalSupply returned %x", ErrReverted, b)
	}
	return new(big.Int).SetBytes(b[:32]), nil
}
