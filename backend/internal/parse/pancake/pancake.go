// Package pancake decodes PancakeSwap swaps of tracked tokens on BSC. Pools
// are recognised without registries: a V2 pair is the CREATE2 address of its
// two tokens, taken from the transaction's transfers (06-parse §7). Tracked
// tokens are Four.meme tokens: a vanity suffix gate, then getTokenInfo.
package pancake

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

// Decoder names and version recorded on facts.
const (
	V2Name  = "pancake_v2"
	Version = 2
)

// tokenDecimals of Four.meme tokens.
const tokenDecimals = 18

var (
	evTransfer = evm.Topic("Transfer(address,address,uint256)")
	evV2Swap   = evm.Topic("Swap(address,uint256,uint256,uint256,uint256,address)")
	evV2Sync   = evm.Topic("Sync(uint112,uint112)")
)

// Lookup identifies tracked tokens and quote metadata.
type Lookup interface {
	FourMeme(ctx context.Context, token evm.Address) (chaininfo.FourMeme, error)
	ERC20(ctx context.Context, token evm.Address) (chaininfo.ERC20, error)
	InfinityPoolKey(ctx context.Context, manager evm.Address, id evm.Hash) (chaininfo.PoolKey, error)
}

// Config holds the pool deployers and the chain's quote assets.
type Config struct {
	V2Factory             evm.Address
	V2InitCodeHash        evm.Hash
	V3Deployer            evm.Address
	V3InitCodeHash        evm.Hash
	V3Fees                []uint32
	InfinityCLPoolManager evm.Address
	// VanitySuffixes gate the Four.meme lookup by address suffix; nil means
	// the Four.meme suffixes …4444 and …ffff.
	VanitySuffixes [][]byte
	WrappedNative  evm.Address
	NativeSymbol   string
	QuoteSymbols   map[evm.Address]string
}

// V2 decodes PancakeSwap V2 swaps.
type V2 struct {
	cfg    Config
	lookup Lookup
}

// NewV2 returns a V2 decoder.
func NewV2(cfg Config, lookup Lookup) *V2 { return &V2{cfg: cfg, lookup: lookup} }

// Decode returns the facts of tx.
func (d *V2) Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error) {
	var f parse.Facts
	if tx.Failed {
		return f, nil
	}
	for _, l := range tx.Logs {
		if len(l.Topics) > 0 && (l.Topics[0] == evV2Mint || l.Topics[0] == evV2Burn) && len(l.Data) >= 64 {
			t0, t1, ok := d.pairTokens(tx, l.Address)
			if !ok {
				continue
			}
			w := words(l.Data)
			if err := liquidity(ctx, &f, d.lookup, d.cfg, tx, l, V2Name, b(l.Address[:]), t0, t1, w[0], w[1], l.Topics[0] == evV2Mint); err != nil {
				return parse.Facts{}, err
			}
			continue
		}
		if len(l.Topics) != 3 || l.Topics[0] != evV2Swap || len(l.Data) < 128 {
			continue
		}
		t0, t1, ok := d.pairTokens(tx, l.Address)
		if !ok {
			continue
		}
		tok, quote, ok, err := track(ctx, d.lookup, d.cfg, t0, t1)
		if err != nil {
			return parse.Facts{}, err
		}
		if !ok {
			continue
		}
		w := words(l.Data)
		in0, in1, out0, out1 := w[0], w[1], w[2], w[3]
		tin, tout, qin, qout := in0, out0, in1, out1
		if tok == t1 {
			tin, tout, qin, qout = in1, out1, in0, out0
		}
		buy := tout.Cmp(tin) > 0
		tokAmt := new(big.Int).Sub(tin, tout)
		if buy {
			tokAmt.Neg(tokAmt)
		}
		qAmt := new(big.Int).Abs(new(big.Int).Sub(qin, qout))
		q, err := quoteOf(ctx, d.lookup, d.cfg, quote)
		if err != nil {
			f.Failures = append(f.Failures, failure(tx, l, V2Name, err))
			continue
		}
		rt, rq := d.reserves(tx, l, tok == t0)
		dec := int16(tokenDecimals)
		f.Tokens = append(f.Tokens, parse.Token{Address: b(tok[:]), Decimals: &dec, LaunchPlatform: "four_meme"})
		f.Pools = append(f.Pools, parse.Pool{ID: b(l.Address[:]), Token: b(tok[:]), Quote: b(quote[:]), QuoteSymbol: q.symbol,
			Protocol: V2Name, Dex: "pancakeswap", ReserveToken: rt, ReserveQuote: rq, Tx: tx.Index, Ix: l.Index})
		f.Trades = append(f.Trades, trade(tx, l, V2Name, tok, q, buy, tokAmt, qAmt))
	}
	return f, nil
}

// pairTokens finds the two tokens transferred to or from pair whose CREATE2
// pair address is pair.
func (d *V2) pairTokens(tx evm.Tx, pair evm.Address) (evm.Address, evm.Address, bool) {
	toks := counterparties(tx, pair)
	for i, a := range toks {
		for _, c := range toks[i+1:] {
			t0, t1 := sorted(a, c)
			salt := evm.Keccak256(t0[:], t1[:])
			addr := evm.Keccak256([]byte{0xff}, d.cfg.V2Factory[:], salt[:], d.cfg.V2InitCodeHash[:])
			if evm.Address(addr[12:]) == pair {
				return t0, t1, true
			}
		}
	}
	return evm.Address{}, evm.Address{}, false
}

// reserves are those of the pair's last Sync before the swap.
func (d *V2) reserves(tx evm.Tx, swap evm.Log, tokenIsZero bool) (string, string) {
	var last *evm.Log
	for i := range tx.Logs {
		l := &tx.Logs[i]
		if l.Address == swap.Address && len(l.Topics) == 1 && l.Topics[0] == evV2Sync && l.Index < swap.Index && len(l.Data) >= 64 {
			last = l
		}
	}
	if last == nil {
		return "", ""
	}
	w := words(last.Data)
	if tokenIsZero {
		return w[0].String(), w[1].String()
	}
	return w[1].String(), w[0].String()
}

// counterparties are the distinct token contracts with a transfer from or to addr.
func counterparties(tx evm.Tx, addr evm.Address) []evm.Address {
	var out []evm.Address
	for _, l := range tx.Logs {
		if len(l.Topics) != 3 || l.Topics[0] != evTransfer {
			continue
		}
		if evm.Address(l.Topics[1][12:]) != addr && evm.Address(l.Topics[2][12:]) != addr {
			continue
		}
		dup := false
		for _, o := range out {
			dup = dup || o == l.Address
		}
		if !dup {
			out = append(out, l.Address)
		}
	}
	return out
}

func sorted(a, c evm.Address) (evm.Address, evm.Address) {
	if bytes.Compare(a[:], c[:]) < 0 {
		return a, c
	}
	return c, a
}

// track picks the tracked token of a pair: a Four.meme token (vanity suffix,
// then getTokenInfo); with two, the one quoted in the other on its curve.
func track(ctx context.Context, lookup Lookup, cfg Config, t0, t1 evm.Address) (tok, quote evm.Address, ok bool, err error) {
	i0, err := fourMeme(ctx, lookup, cfg, t0)
	if err != nil {
		return tok, quote, false, err
	}
	i1, err := fourMeme(ctx, lookup, cfg, t1)
	if err != nil {
		return tok, quote, false, err
	}
	switch {
	case i0.Found && i1.Found:
		if i1.Quote == t0 {
			return t1, t0, true, nil
		}
		return t0, t1, true, nil
	case i0.Found:
		return t0, t1, true, nil
	case i1.Found:
		return t1, t0, true, nil
	}
	return tok, quote, false, nil
}

// vanity suffixes of Four.meme token addresses (…4444 earlier, …ffff now).
var suffixes = [][]byte{{0x44, 0x44}, {0xff, 0xff}}

func fourMeme(ctx context.Context, lookup Lookup, cfg Config, t evm.Address) (chaininfo.FourMeme, error) {
	gated, sfx := false, cfg.VanitySuffixes
	if sfx == nil {
		sfx = suffixes
	}
	for _, s := range sfx {
		gated = gated || bytes.HasSuffix(t[:], s)
	}
	if !gated {
		return chaininfo.FourMeme{}, nil
	}
	info, err := lookup.FourMeme(ctx, t)
	if err != nil {
		return info, fmt.Errorf("getTokenInfo %s: %w: %w", t.Hex(), parse.ErrRetry, err)
	}
	return info, nil
}

type quote struct {
	asset    evm.Address
	symbol   string
	decimals int
	native   bool
}

// quoteOf resolves symbol and decimals; the wrapped coin uses the native symbol.
func quoteOf(ctx context.Context, lookup Lookup, cfg Config, a evm.Address) (quote, error) {
	m, err := lookup.ERC20(ctx, a)
	if errors.Is(err, chaininfo.ErrReverted) {
		return quote{}, fmt.Errorf("quote %s is not an ERC-20: %w", a.Hex(), err)
	}
	if err != nil {
		return quote{}, fmt.Errorf("quote %s metadata: %w: %w", a.Hex(), parse.ErrRetry, err)
	}
	q := quote{asset: a, symbol: m.Symbol, decimals: int(m.Decimals), native: a == cfg.WrappedNative}
	if s, ok := cfg.QuoteSymbols[a]; ok {
		q.symbol = s
	}
	if q.native {
		q.symbol = cfg.NativeSymbol
	}
	return q, nil
}

// trade builds the fact; the wallet is tx.from when its net token change
// matches the side, else the trade is ambiguous (bots, aggregators).
func trade(tx evm.Tx, l evm.Log, decoder string, tok evm.Address, q quote, buy bool, tokAmt, qAmt *big.Int) parse.Trade {
	delta := new(big.Int)
	for _, x := range tx.Logs {
		if x.Address != tok || len(x.Topics) != 3 || x.Topics[0] != evTransfer || len(x.Data) < 32 {
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
	t := parse.Trade{Tx: tx.Index, Ix: l.Index, TxHash: b(tx.Hash[:]), Pool: b(l.Address[:]), Token: b(tok[:]),
		Attribution: "ambiguous", Side: "sell", TokenAmount: tokAmt.String(), QuoteAmount: qAmt.String(),
		QuoteSymbol: q.symbol, Decoder: decoder, Version: Version}
	if buy {
		t.Side = "buy"
	}
	if (buy && delta.Sign() > 0) || (!buy && delta.Sign() < 0) {
		t.Attribution, t.Wallet = "confirmed", b(tx.From[:])
	}
	if tokAmt.Sign() > 0 {
		t.PriceNative = ratio(new(big.Int).Mul(qAmt, pow10(tokenDecimals)), new(big.Int).Mul(tokAmt, pow10(q.decimals)), 18)
	}
	if q.native {
		t.ValueNative = ratio(qAmt, pow10(18), 18)
	}
	return t
}

func failure(tx evm.Tx, l evm.Log, decoder string, err error) parse.Failure {
	return parse.Failure{Tx: tx.Index, TxHash: b(tx.Hash[:]), Decoder: decoder, Err: fmt.Sprintf("log %d: %v", l.Index, err)}
}

func words(data []byte) []*big.Int {
	var out []*big.Int
	for i := 0; i+32 <= len(data); i += 32 {
		out = append(out, new(big.Int).SetBytes(data[i:i+32]))
	}
	return out
}

func ratio(num, den *big.Int, places int) string {
	return new(big.Rat).SetFrac(num, den).FloatString(places)
}
func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }
func b(x []byte) []byte    { return append([]byte(nil), x...) }

// V3 decodes PancakeSwap V3 swaps. A pool is the CREATE2 address of its
// tokens and fee tier; swap amounts are signed from the pool's side
// (positive into the pool). V3 has no reserve events, so reserves are unset.
type V3 struct {
	cfg    Config
	lookup Lookup
}

// NewV3 returns a V3 decoder.
func NewV3(cfg Config, lookup Lookup) *V3 { return &V3{cfg: cfg, lookup: lookup} }

// Decoder names of V3 and Infinity CL facts.
const (
	V3Name         = "pancake_v3"
	InfinityCLName = "pancake_infinity_cl"
)

var (
	evV2Mint = evm.Topic("Mint(address,uint256,uint256)")
	evV2Burn = evm.Topic("Burn(address,uint256,uint256,address)")
	evV3Mint = evm.Topic("Mint(address,address,int24,int24,uint128,uint256,uint256)")
	evV3Burn = evm.Topic("Burn(address,int24,int24,uint128,uint256,uint256)")
	evV3Swap = evm.Topic("Swap(address,address,int256,int256,uint160,uint128,int24,uint128,uint128)")
	evCLSwap = evm.Topic("Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24,uint16)")
)

// Decode returns the facts of tx.
func (d *V3) Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error) {
	var f parse.Facts
	if tx.Failed {
		return f, nil
	}
	for _, l := range tx.Logs {
		mint := len(l.Topics) == 4 && l.Topics[0] == evV3Mint && len(l.Data) >= 128
		burn := len(l.Topics) == 4 && l.Topics[0] == evV3Burn && len(l.Data) >= 96
		if mint || burn {
			t0, t1, ok := d.poolTokens(tx, l.Address)
			if !ok {
				continue
			}
			// Mint data: sender, amount, amount0, amount1; Burn: amount, amount0, amount1.
			w, i := words(l.Data), 1
			if mint {
				i = 2
			}
			if err := liquidity(ctx, &f, d.lookup, d.cfg, tx, l, V3Name, b(l.Address[:]), t0, t1, w[i], w[i+1], mint); err != nil {
				return parse.Facts{}, err
			}
			continue
		}
		if len(l.Topics) != 3 || l.Topics[0] != evV3Swap || len(l.Data) < 64 {
			continue
		}
		t0, t1, ok := d.poolTokens(tx, l.Address)
		if !ok {
			continue
		}
		w := signedWords(l.Data)
		// Pool side: the user buys the token when it leaves the pool.
		if err := swap(ctx, &f, d.lookup, d.cfg, tx, l, V3Name, b(l.Address[:]), t0, t1, w[0], w[1], true); err != nil {
			return parse.Facts{}, err
		}
	}
	return f, nil
}

func (d *V3) poolTokens(tx evm.Tx, pool evm.Address) (evm.Address, evm.Address, bool) {
	toks := counterparties(tx, pool)
	for i, a := range toks {
		for _, c := range toks[i+1:] {
			t0, t1 := sorted(a, c)
			for _, fee := range d.cfg.V3Fees {
				var enc [96]byte
				copy(enc[12:32], t0[:])
				copy(enc[44:64], t1[:])
				new(big.Int).SetUint64(uint64(fee)).FillBytes(enc[64:96])
				salt := evm.Keccak256(enc[:])
				addr := evm.Keccak256([]byte{0xff}, d.cfg.V3Deployer[:], salt[:], d.cfg.V3InitCodeHash[:])
				if evm.Address(addr[12:]) == pool {
					return t0, t1, true
				}
			}
		}
	}
	return evm.Address{}, evm.Address{}, false
}

// InfinityCL decodes PancakeSwap Infinity concentrated-liquidity swaps from
// the singleton CLPoolManager. Currencies come from poolIdToPoolKey (cached);
// amounts are signed from the swapper's side (positive received).
type InfinityCL struct {
	cfg    Config
	lookup Lookup
}

// NewInfinityCL returns an Infinity CL decoder.
func NewInfinityCL(cfg Config, lookup Lookup) *InfinityCL {
	return &InfinityCL{cfg: cfg, lookup: lookup}
}

// Decode returns the facts of tx.
func (d *InfinityCL) Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error) {
	var f parse.Facts
	if tx.Failed {
		return f, nil
	}
	for _, l := range tx.Logs {
		if l.Address != d.cfg.InfinityCLPoolManager || len(l.Topics) != 3 || l.Topics[0] != evCLSwap || len(l.Data) < 64 {
			continue
		}
		key, err := d.lookup.InfinityPoolKey(ctx, l.Address, l.Topics[1])
		if err != nil {
			return parse.Facts{}, fmt.Errorf("poolIdToPoolKey %x: %w: %w", l.Topics[1], parse.ErrRetry, err)
		}
		c0, c1 := key.Currency0, key.Currency1
		if c0 == (evm.Address{}) {
			c0 = d.cfg.WrappedNative
		}
		if c1 == (evm.Address{}) {
			c1 = d.cfg.WrappedNative
		}
		w := signedWords(l.Data)
		id := l.Topics[1]
		if err := swap(ctx, &f, d.lookup, d.cfg, tx, l, InfinityCLName, b(id[:]), c0, c1, w[0], w[1], false); err != nil {
			return parse.Facts{}, err
		}
	}
	return f, nil
}

// swap records a signed-amount swap of pool between c0 and c1 when one of
// them is tracked. poolSide says the amounts are the pool's (V3) rather
// than the swapper's (Infinity).
func swap(ctx context.Context, f *parse.Facts, lookup Lookup, cfg Config, tx evm.Tx, l evm.Log, decoder string, pool []byte,
	c0, c1 evm.Address, a0, a1 *big.Int, poolSide bool) error {
	tok, quoteAddr, ok, err := track(ctx, lookup, cfg, c0, c1)
	if err != nil || !ok {
		return err
	}
	at, aq := a0, a1
	if tok == c1 {
		at, aq = a1, a0
	}
	buy := at.Sign() > 0
	if poolSide {
		buy = at.Sign() < 0
	}
	q, err := quoteOf(ctx, lookup, cfg, quoteAddr)
	if err != nil {
		f.Failures = append(f.Failures, failure(tx, l, decoder, err))
		return nil
	}
	dec := int16(tokenDecimals)
	f.Tokens = append(f.Tokens, parse.Token{Address: b(tok[:]), Decimals: &dec, LaunchPlatform: "four_meme"})
	f.Pools = append(f.Pools, parse.Pool{ID: pool, Token: b(tok[:]), Quote: b(quoteAddr[:]), QuoteSymbol: q.symbol,
		Protocol: decoder, Dex: "pancakeswap", Tx: tx.Index, Ix: l.Index})
	t := trade(tx, l, decoder, tok, q, buy, new(big.Int).Abs(at), new(big.Int).Abs(aq))
	t.Pool = pool
	f.Trades = append(f.Trades, t)
	return nil
}

// liquidity records an add or remove of a tracked token's pool; the wallet
// is tx.from (liquidity is not attributed, 02-data-model §4). A burn of no
// tokens (fee collection) is skipped.
func liquidity(ctx context.Context, f *parse.Facts, lookup Lookup, cfg Config, tx evm.Tx, l evm.Log, decoder string, pool []byte,
	c0, c1 evm.Address, a0, a1 *big.Int, add bool) error {
	if a0.Sign() == 0 && a1.Sign() == 0 {
		return nil
	}
	tok, quoteAddr, ok, err := track(ctx, lookup, cfg, c0, c1)
	if err != nil || !ok {
		return err
	}
	at, aq := a0, a1
	if tok == c1 {
		at, aq = a1, a0
	}
	q, err := quoteOf(ctx, lookup, cfg, quoteAddr)
	if err != nil {
		f.Failures = append(f.Failures, failure(tx, l, decoder, err))
		return nil
	}
	e := parse.Liquidity{Tx: tx.Index, Ix: l.Index, TxHash: b(tx.Hash[:]), Pool: pool, Token: b(tok[:]), Wallet: b(tx.From[:]),
		Kind: "remove", TokenAmount: at.String(), QuoteAmount: aq.String(), QuoteSymbol: q.symbol, Decoder: decoder, Version: Version}
	if add {
		e.Kind = "add"
	}
	if q.native {
		e.ValueNative = ratio(aq, pow10(18), 18)
	}
	f.Liquidity = append(f.Liquidity, e)
	return nil
}

// signedWords decodes two's-complement 256-bit words.
func signedWords(data []byte) []*big.Int {
	out := words(data)
	limit := new(big.Int).Lsh(big.NewInt(1), 255)
	full := new(big.Int).Lsh(big.NewInt(1), 256)
	for _, w := range out {
		if w.Cmp(limit) >= 0 {
			w.Sub(w, full)
		}
	}
	return out
}
