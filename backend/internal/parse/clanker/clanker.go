// Package clanker decodes Clanker v4 launches (factory TokenCreated) and
// swaps of their Uniswap v4 pools on Base (github.com/clanker-devco/v4-contracts).
// A pool is known from TokenCreated in the same transaction, or recognised
// by recomputing its PoolId from the transaction's tokens with Clanker's
// fixed pool parameters (dynamic fee, tick spacing 200, Clanker hooks).
package clanker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
)

// Decoder name and version recorded on facts.
const (
	Name    = "clanker_v4"
	Version = 1
)

// Clanker pool parameters and token decimals.
const (
	dynamicFee    = 0x800000
	tickSpacing   = 200
	tokenDecimals = 18
)

var (
	evCreated  = evm.Topic("TokenCreated(address,address,address,string,string,string,string,string,int24,address,bytes32,address,address,address,uint256,address[])")
	evSwap     = evm.Topic("Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24)")
	evTransfer = evm.Topic("Transfer(address,address,uint256)")
)

// Lookup gives metadata of quote assets other than the native coin.
type Lookup interface {
	ERC20(ctx context.Context, token evm.Address) (chaininfo.ERC20, error)
}

// Config locates the contracts and the chain's quote assets.
type Config struct {
	Factory, PoolManager evm.Address
	Hooks                []evm.Address
	WrappedNative        evm.Address
	NativeSymbol         string
	QuoteSymbols         map[evm.Address]string // includes the zero address for the native coin
}

// Decoder decodes one chain's Clanker v4 events.
type Decoder struct {
	cfg    Config
	lookup Lookup
}

// New returns a decoder.
func New(cfg Config, lookup Lookup) *Decoder { return &Decoder{cfg: cfg, lookup: lookup} }

type pool struct{ token, quote evm.Address }

// Decode returns the facts of tx.
func (d *Decoder) Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error) {
	var f parse.Facts
	if tx.Failed {
		return f, nil
	}
	known := map[evm.Hash]pool{}
	for _, l := range tx.Logs {
		if l.Address != d.cfg.Factory || len(l.Topics) != 3 || l.Topics[0] != evCreated {
			continue
		}
		if err := d.created(ctx, &f, tx, l, known); err != nil {
			f.Failures = append(f.Failures, failure(tx, l, err))
		}
	}
	var cands []evm.Address
	for _, l := range tx.Logs {
		if l.Address == d.cfg.PoolManager && len(l.Topics) == 3 && l.Topics[0] == evSwap && len(l.Data) >= 64 {
			if cands == nil {
				cands = candidates(tx)
			}
			if err := d.swap(ctx, &f, tx, l, known, cands); err != nil {
				return parse.Facts{}, err
			}
		}
	}
	return f, nil
}

func (d *Decoder) created(ctx context.Context, f *parse.Facts, tx evm.Tx, l evm.Log, known map[evm.Hash]pool) error {
	w := l.Data
	if len(w) < 14*32 {
		return errors.New("short TokenCreated")
	}
	str := func(i int) (string, error) { return abiString(w, word(w, i)) }
	image, err := str(1)
	if err != nil {
		return err
	}
	name, err := str(2)
	if err != nil {
		return err
	}
	symbol, err := str(3)
	if err != nil {
		return err
	}
	token, admin := evm.Address(l.Topics[1][12:]), evm.Address(l.Topics[2][12:])
	poolID := evm.Hash(word(w, 8))
	paired := evm.Address(word(w, 9)[12:])
	q, err := d.quote(ctx, paired)
	if err != nil {
		return err
	}
	known[poolID] = pool{token: token, quote: paired}
	dec := int16(tokenDecimals)
	f.Tokens = append(f.Tokens, parse.Token{Address: b(token[:]), Created: true, Name: name, Symbol: symbol, URI: image,
		Decimals: &dec, Creator: b(admin[:]), LaunchPlatform: "clanker"})
	f.Pools = append(f.Pools, parse.Pool{ID: b(poolID[:]), Token: b(token[:]), Quote: b(q.asset[:]), QuoteSymbol: q.symbol,
		Protocol: "uniswap_v4", Dex: "uniswap", Created: true, Status: "active", Tx: tx.Index, Ix: l.Index})
	return nil
}

func (d *Decoder) swap(ctx context.Context, f *parse.Facts, tx evm.Tx, l evm.Log, known map[evm.Hash]pool, cands []evm.Address) error {
	id := l.Topics[1]
	p, ok := known[id]
	if !ok {
		if p, ok = d.recognise(id, cands); !ok {
			return nil
		}
	}
	c0, _ := sorted(p.token, p.quote)
	a0, a1 := signed(word(l.Data, 0)), signed(word(l.Data, 1))
	at, aq := a0, a1
	if p.token != c0 {
		at, aq = a1, a0
	}
	q, err := d.quote(ctx, p.quote)
	if errors.Is(err, parse.ErrRetry) {
		return err
	}
	if err != nil {
		f.Failures = append(f.Failures, failure(tx, l, err))
		return nil
	}
	buy := at.Sign() > 0 // amounts are the swapper's: positive received
	tokAmt, qAmt := new(big.Int).Abs(at), new(big.Int).Abs(aq)
	dec := int16(tokenDecimals)
	f.Tokens = append(f.Tokens, parse.Token{Address: b(p.token[:]), Decimals: &dec, LaunchPlatform: "clanker"})
	f.Pools = append(f.Pools, parse.Pool{ID: b(id[:]), Token: b(p.token[:]), Quote: b(q.asset[:]), QuoteSymbol: q.symbol,
		Protocol: "uniswap_v4", Dex: "uniswap", Tx: tx.Index, Ix: l.Index})
	t := parse.Trade{Tx: tx.Index, Ix: l.Index, TxHash: b(tx.Hash[:]), Pool: b(id[:]), Token: b(p.token[:]),
		Attribution: "ambiguous", Side: "sell", TokenAmount: tokAmt.String(), QuoteAmount: qAmt.String(),
		QuoteSymbol: q.symbol, Decoder: Name, Version: Version}
	if buy {
		t.Side = "buy"
	}
	if delta := netDelta(tx, p.token); (buy && delta.Sign() > 0) || (!buy && delta.Sign() < 0) {
		t.Attribution, t.Wallet = "confirmed", b(tx.From[:])
	}
	if tokAmt.Sign() > 0 {
		t.PriceNative = ratio(new(big.Int).Mul(qAmt, pow10(tokenDecimals)), new(big.Int).Mul(tokAmt, pow10(q.decimals)), 18)
	}
	if q.native {
		t.ValueNative = ratio(qAmt, pow10(18), 18)
	}
	f.Trades = append(f.Trades, t)
	return nil
}

// recognise recomputes PoolIds of the transaction's token pairs with each
// Clanker hook; the tracked token is the side that is not a quote asset.
func (d *Decoder) recognise(id evm.Hash, cands []evm.Address) (pool, bool) {
	for i, a := range cands {
		for _, c := range cands[i+1:] {
			c0, c1 := sorted(a, c)
			for _, h := range d.cfg.Hooks {
				if poolID(c0, c1, h) != id {
					continue
				}
				_, q0 := d.cfg.QuoteSymbols[c0]
				_, q1 := d.cfg.QuoteSymbols[c1]
				switch {
				case q0 && !q1:
					return pool{token: c1, quote: c0}, true
				case q1 && !q0:
					return pool{token: c0, quote: c1}, true
				}
				return pool{}, false
			}
		}
	}
	return pool{}, false
}

// poolID is keccak256(abi.encode(currency0, currency1, fee, tickSpacing, hooks)).
func poolID(c0, c1, hooks evm.Address) evm.Hash {
	var enc [160]byte
	copy(enc[12:32], c0[:])
	copy(enc[44:64], c1[:])
	big.NewInt(dynamicFee).FillBytes(enc[64:96])
	big.NewInt(tickSpacing).FillBytes(enc[96:128])
	copy(enc[140:160], hooks[:])
	return evm.Keccak256(enc[:])
}

// candidates are the transaction's token contracts plus the native coin.
func candidates(tx evm.Tx) []evm.Address {
	out := []evm.Address{{}}
	seen := map[evm.Address]bool{{}: true}
	for _, l := range tx.Logs {
		if len(l.Topics) > 0 && l.Topics[0] == evTransfer && !seen[l.Address] {
			seen[l.Address] = true
			out = append(out, l.Address)
		}
	}
	return out
}

type quote struct {
	asset    evm.Address
	symbol   string
	decimals int
	native   bool
}

// quote resolves a quote asset; the native coin is recorded as the wrapped
// one and both use the native symbol.
func (d *Decoder) quote(ctx context.Context, a evm.Address) (quote, error) {
	if a == (evm.Address{}) || a == d.cfg.WrappedNative {
		return quote{asset: d.cfg.WrappedNative, symbol: d.cfg.NativeSymbol, decimals: 18, native: true}, nil
	}
	m, err := d.lookup.ERC20(ctx, a)
	if errors.Is(err, chaininfo.ErrReverted) {
		return quote{}, fmt.Errorf("quote %s is not an ERC-20: %w", a.Hex(), err)
	}
	if err != nil {
		return quote{}, fmt.Errorf("quote %s metadata: %w: %w", a.Hex(), parse.ErrRetry, err)
	}
	q := quote{asset: a, symbol: m.Symbol, decimals: int(m.Decimals)}
	if s, ok := d.cfg.QuoteSymbols[a]; ok {
		q.symbol = s
	}
	return q, nil
}

// netDelta is tx.from's net change of token across the transaction's transfers.
func netDelta(tx evm.Tx, token evm.Address) *big.Int {
	delta := new(big.Int)
	for _, x := range tx.Logs {
		if x.Address != token || len(x.Topics) != 3 || x.Topics[0] != evTransfer || len(x.Data) < 32 {
			continue
		}
		v := new(big.Int).SetBytes(x.Data[:32])
		if evm.Address(x.Topics[2][12:]) == tx.From {
			delta.Add(delta, v)
		}
		if evm.Address(x.Topics[1][12:]) == tx.From {
			delta.Sub(delta, v)
		}
	}
	return delta
}

func failure(tx evm.Tx, l evm.Log, err error) parse.Failure {
	return parse.Failure{Tx: tx.Index, TxHash: b(tx.Hash[:]), Decoder: Name, Err: fmt.Sprintf("log %d: %v", l.Index, err)}
}

func word(data []byte, i int) []byte { return data[32*i : 32*(i+1)] }

func signed(w []byte) *big.Int {
	v := new(big.Int).SetBytes(w)
	if w[0]&0x80 != 0 {
		v.Sub(v, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	return v
}

func abiString(data, offset []byte) (string, error) {
	o := new(big.Int).SetBytes(offset)
	if !o.IsInt64() || o.Int64()+32 > int64(len(data)) {
		return "", errors.New("string offset out of range")
	}
	n := new(big.Int).SetBytes(data[o.Int64() : o.Int64()+32])
	if !n.IsInt64() || o.Int64()+32+n.Int64() > int64(len(data)) {
		return "", errors.New("string length out of range")
	}
	return string(data[o.Int64()+32 : o.Int64()+32+n.Int64()]), nil
}

func sorted(a, c evm.Address) (evm.Address, evm.Address) {
	if bytes.Compare(a[:], c[:]) < 0 {
		return a, c
	}
	return c, a
}

func ratio(num, den *big.Int, places int) string {
	return new(big.Rat).SetFrac(num, den).FloatString(places)
}
func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }
func b(x []byte) []byte    { return append([]byte(nil), x...) }
