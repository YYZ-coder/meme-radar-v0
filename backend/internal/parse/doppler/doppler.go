// Package doppler decodes swaps of Doppler launches (github.com/whetstoneresearch/doppler)
// on Uniswap v4 pools of Doppler's fixed hooks, recognised by recomputing the
// PoolId from the transaction's tokens; the Doppler token is the side for
// which Airlock.getAssetData answers (06-parse §7).
package doppler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
)

// Decoder name and version recorded on facts.
const (
	Name    = "doppler"
	Version = 1
)

const tokenDecimals = 18

// Pool parameters tried when recomputing PoolIds: the dynamic-fee flag and
// fixed fee tiers, with the tick spacings Doppler launches use.
var (
	fees     = []int64{0x800000, 100, 500, 3000, 10000}
	spacings = []int64{1, 10, 60, 100, 200}
)

var (
	evSwap     = evm.Topic("Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24)")
	evTransfer = evm.Topic("Transfer(address,address,uint256)")
)

// Lookup identifies Doppler assets and quote metadata.
type Lookup interface {
	DopplerAsset(ctx context.Context, airlock, token evm.Address) (chaininfo.DopplerAsset, error)
	ERC20(ctx context.Context, token evm.Address) (chaininfo.ERC20, error)
}

// Config locates the contracts and the chain's quote assets.
type Config struct {
	Airlock, PoolManager evm.Address
	Hooks                []evm.Address // fixed-address Doppler hooks
	WrappedNative        evm.Address
	NativeSymbol         string
	QuoteSymbols         map[evm.Address]string // includes the zero address
}

// Decoder decodes Doppler v4 swaps.
type Decoder struct {
	cfg    Config
	lookup Lookup
	// known remembers recognised pools: in a v4 multi-hop swap the middle
	// token never moves, so its pool can only be recognised from earlier.
	mu    sync.Mutex
	known map[evm.Hash][2]evm.Address
}

// New returns a decoder.
func New(cfg Config, lookup Lookup) *Decoder {
	return &Decoder{cfg: cfg, lookup: lookup, known: map[evm.Hash][2]evm.Address{}}
}

// Decode returns the facts of tx.
func (d *Decoder) Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error) {
	var f parse.Facts
	if tx.Failed {
		return f, nil
	}
	var cands []evm.Address
	for _, l := range tx.Logs {
		if l.Address != d.cfg.PoolManager || len(l.Topics) != 3 || l.Topics[0] != evSwap || len(l.Data) < 64 {
			continue
		}
		c0, c1, ok := d.remembered(l.Topics[1])
		if !ok {
			if cands == nil {
				var err error
				if cands, err = d.candidates(ctx, tx); err != nil {
					return parse.Facts{}, err
				}
			}
			if c0, c1, ok = d.recognise(l.Topics[1], cands); !ok {
				continue
			}
			d.remember(l.Topics[1], c0, c1)
		}
		tok, quoteAddr, ok, err := d.side(ctx, c0, c1)
		if err != nil {
			return parse.Facts{}, err
		}
		if !ok {
			continue
		}
		if err := d.swap(ctx, &f, tx, l, c0, tok, quoteAddr); err != nil {
			return parse.Facts{}, err
		}
	}
	return f, nil
}

func (d *Decoder) remembered(id evm.Hash) (evm.Address, evm.Address, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.known[id]
	return p[0], p[1], ok
}

func (d *Decoder) remember(id evm.Hash, c0, c1 evm.Address) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.known[id] = [2]evm.Address{c0, c1}
}

// candidates are the transaction's transferred tokens, the chain's quote
// assets and the numeraires of the Doppler assets among them.
func (d *Decoder) candidates(ctx context.Context, tx evm.Tx) ([]evm.Address, error) {
	seen := map[evm.Address]bool{}
	var out []evm.Address
	add := func(a evm.Address) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for q := range d.cfg.QuoteSymbols {
		add(q)
	}
	var transferred []evm.Address
	for _, l := range tx.Logs {
		if len(l.Topics) > 0 && l.Topics[0] == evTransfer && !seen[l.Address] {
			add(l.Address)
			transferred = append(transferred, l.Address)
		}
	}
	for _, t := range transferred {
		if _, ok := d.cfg.QuoteSymbols[t]; ok {
			continue
		}
		a, err := d.lookup.DopplerAsset(ctx, d.cfg.Airlock, t)
		if err != nil {
			return nil, fmt.Errorf("getAssetData %s: %w: %w", t.Hex(), parse.ErrRetry, err)
		}
		if a.Found {
			add(a.Numeraire)
		}
	}
	return out, nil
}

// recognise finds the pair and pool parameters whose PoolId is id.
func (d *Decoder) recognise(id evm.Hash, cands []evm.Address) (evm.Address, evm.Address, bool) {
	for i, a := range cands {
		for _, c := range cands[i+1:] {
			c0, c1 := sorted(a, c)
			for _, h := range d.cfg.Hooks {
				for _, fee := range fees {
					for _, ts := range spacings {
						if poolID(c0, c1, fee, ts, h) == id {
							return c0, c1, true
						}
					}
				}
			}
		}
	}
	return evm.Address{}, evm.Address{}, false
}

// side picks the Doppler asset of a pool; quote assets are never looked up.
func (d *Decoder) side(ctx context.Context, c0, c1 evm.Address) (tok, quote evm.Address, ok bool, err error) {
	a0, err := d.asset(ctx, c0)
	if err != nil {
		return tok, quote, false, err
	}
	a1, err := d.asset(ctx, c1)
	if err != nil {
		return tok, quote, false, err
	}
	switch {
	case a0 && !a1:
		return c0, c1, true, nil
	case a1 && !a0:
		return c1, c0, true, nil
	}
	return tok, quote, false, nil
}

func (d *Decoder) asset(ctx context.Context, t evm.Address) (bool, error) {
	if _, ok := d.cfg.QuoteSymbols[t]; ok {
		return false, nil
	}
	a, err := d.lookup.DopplerAsset(ctx, d.cfg.Airlock, t)
	if err != nil {
		return false, fmt.Errorf("getAssetData %s: %w: %w", t.Hex(), parse.ErrRetry, err)
	}
	return a.Found, nil
}

func (d *Decoder) swap(ctx context.Context, f *parse.Facts, tx evm.Tx, l evm.Log, c0, tok, quoteAddr evm.Address) error {
	a0, a1 := signed(word(l.Data, 0)), signed(word(l.Data, 1))
	at, aq := a0, a1
	if tok != c0 {
		at, aq = a1, a0
	}
	q, err := d.quote(ctx, quoteAddr)
	if errors.Is(err, parse.ErrRetry) {
		return err
	}
	if err != nil {
		f.Failures = append(f.Failures, failure(tx, l, err))
		return nil
	}
	id := l.Topics[1]
	buy := at.Sign() > 0 // amounts are the swapper's: positive received
	tokAmt, qAmt := new(big.Int).Abs(at), new(big.Int).Abs(aq)
	dec := int16(tokenDecimals)
	f.Tokens = append(f.Tokens, parse.Token{Address: b(tok[:]), Decimals: &dec, LaunchPlatform: "doppler"})
	f.Pools = append(f.Pools, parse.Pool{ID: b(id[:]), Token: b(tok[:]), Quote: b(q.asset[:]), QuoteSymbol: q.symbol,
		Protocol: "uniswap_v4", Dex: "uniswap", Tx: tx.Index, Ix: l.Index})
	t := parse.Trade{Tx: tx.Index, Ix: l.Index, TxHash: b(tx.Hash[:]), Pool: b(id[:]), Token: b(tok[:]),
		Attribution: "ambiguous", Side: "sell", TokenAmount: tokAmt.String(), QuoteAmount: qAmt.String(),
		QuoteSymbol: q.symbol, Decoder: Name, Version: Version}
	if buy {
		t.Side = "buy"
	}
	if delta := netDelta(tx, tok); (buy && delta.Sign() > 0) || (!buy && delta.Sign() < 0) {
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

// poolID is keccak256(abi.encode(currency0, currency1, fee, tickSpacing, hooks)).
func poolID(c0, c1 evm.Address, fee, spacing int64, hooks evm.Address) evm.Hash {
	var enc [160]byte
	copy(enc[12:32], c0[:])
	copy(enc[44:64], c1[:])
	big.NewInt(fee).FillBytes(enc[64:96])
	big.NewInt(spacing).FillBytes(enc[96:128])
	copy(enc[140:160], hooks[:])
	return evm.Keccak256(enc[:])
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
