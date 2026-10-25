// Package fourmeme decodes Four.meme bonding-curve events of TokenManager V1
// and TokenManager2 on BSC (official ABIs: github.com/four-meme-community/fourmeme-docs,
// MIT). The curve of a token is identified by the token address; its quote
// asset comes from TokenManagerHelper3.getTokenInfo (06-parse §2).
package fourmeme

import (
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
	Name    = "four_meme"
	Version = 2
)

// tokenDecimals is fixed for tokens created by the TokenManagers.
const tokenDecimals = 18

var (
	evCreate   = evm.Topic("TokenCreate(address,address,uint256,string,string,uint256,uint256,uint256)")
	evBuyV2    = evm.Topic("TokenPurchase(address,address,uint256,uint256,uint256,uint256,uint256,uint256)")
	evSellV2   = evm.Topic("TokenSale(address,address,uint256,uint256,uint256,uint256,uint256,uint256)")
	evBuyV1    = evm.Topic("TokenPurchase(address,address,uint256,uint256,uint256)")
	evSellV1   = evm.Topic("TokenSale(address,address,uint256,uint256,uint256)")
	evGraduate = evm.Topic("LiquidityAdded(address,uint256,address,uint256)")
	// V1 closes the curve with TradeStop and never emits LiquidityAdded.
	evStopV1 = evm.Topic("TradeStop(address)")
)

// Lookup answers token questions the events do not (quote asset, decimals).
type Lookup interface {
	FourMeme(ctx context.Context, token evm.Address) (chaininfo.FourMeme, error)
	ERC20(ctx context.Context, token evm.Address) (chaininfo.ERC20, error)
}

// Config locates the contracts and the chain's quote assets.
type Config struct {
	ManagerV1, ManagerV2 evm.Address
	WrappedNative        evm.Address
	NativeSymbol         string
	QuoteSymbols         map[evm.Address]string
}

// Decoder decodes one chain's Four.meme events.
type Decoder struct {
	cfg    Config
	lookup Lookup
}

// New returns a decoder.
func New(cfg Config, lookup Lookup) *Decoder { return &Decoder{cfg: cfg, lookup: lookup} }

type quote struct {
	asset    evm.Address
	symbol   string
	decimals int
	native   bool
}

// Decode returns the facts of tx. Lookup failures that may succeed later
// wrap parse.ErrRetry.
func (d *Decoder) Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error) {
	var f parse.Facts
	if tx.Failed {
		return f, nil
	}
	for _, l := range tx.Logs {
		if len(l.Topics) == 0 || (l.Address != d.cfg.ManagerV1 && l.Address != d.cfg.ManagerV2) {
			continue
		}
		v2 := l.Address == d.cfg.ManagerV2
		var err error
		switch t := l.Topics[0]; {
		case t == evCreate:
			err = d.create(ctx, &f, tx, l)
		case (v2 && (t == evBuyV2 || t == evSellV2)) || (!v2 && (t == evBuyV1 || t == evSellV1)):
			err = d.trade(ctx, &f, tx, l, v2, t == evBuyV2 || t == evBuyV1)
		case t == evGraduate || (!v2 && t == evStopV1):
			err = d.graduate(ctx, &f, l)
		}
		if errors.Is(err, parse.ErrRetry) {
			return parse.Facts{}, err
		}
		if err != nil {
			f.Failures = append(f.Failures, parse.Failure{Tx: tx.Index, TxHash: tx.Hash[:], Decoder: Name,
				Err: fmt.Sprintf("log %d: %v", l.Index, err)})
		}
	}
	return f, nil
}

func (d *Decoder) create(ctx context.Context, f *parse.Facts, tx evm.Tx, l evm.Log) error {
	w := words(l.Data)
	if len(w) < 8 {
		return errors.New("short TokenCreate")
	}
	name, err := abiString(l.Data, w[3])
	if err != nil {
		return err
	}
	symbol, err := abiString(l.Data, w[4])
	if err != nil {
		return err
	}
	token := address(w[1])
	q, err := d.quote(ctx, token)
	if err != nil {
		return err
	}
	dec := int16(tokenDecimals)
	f.Tokens = append(f.Tokens, parse.Token{Address: token[:], Created: true, Name: name, Symbol: symbol, Decimals: &dec,
		TotalSupply: new(big.Int).SetBytes(w[5]).String(), Creator: bytesOf(address(w[0])), LaunchPlatform: Name})
	f.Pools = append(f.Pools, d.pool(tx, l, token, q, true, "active", "", ""))
	return nil
}

func (d *Decoder) trade(ctx context.Context, f *parse.Facts, tx evm.Tx, l evm.Log, v2, buy bool) error {
	w := words(l.Data)
	var amount, cost *big.Int
	reserveToken, reserveQuote := "", ""
	switch {
	case v2 && len(w) >= 8: // token, account, price, amount, cost, fee, offers, funds
		amount, cost = new(big.Int).SetBytes(w[3]), new(big.Int).SetBytes(w[4])
		reserveToken, reserveQuote = new(big.Int).SetBytes(w[6]).String(), new(big.Int).SetBytes(w[7]).String()
	case !v2 && len(w) >= 5: // token, account, tokenAmount, etherAmount, fee
		amount, cost = new(big.Int).SetBytes(w[2]), new(big.Int).SetBytes(w[3])
	default:
		return errors.New("short trade event")
	}
	token, account := address(w[0]), address(w[1])
	q, err := d.quote(ctx, token)
	if err != nil {
		return err
	}
	dec := int16(tokenDecimals)
	f.Tokens = append(f.Tokens, parse.Token{Address: token[:], Decimals: &dec, LaunchPlatform: Name})
	f.Pools = append(f.Pools, d.pool(tx, l, token, q, false, "", reserveToken, reserveQuote))
	attribution := "inferred"
	if account == tx.From {
		attribution = "confirmed"
	}
	side := "sell"
	if buy {
		side = "buy"
	}
	t := parse.Trade{Tx: tx.Index, Ix: l.Index, TxHash: bytesOf32(tx.Hash), Pool: bytesOf(token), Token: bytesOf(token),
		Wallet: bytesOf(account), Attribution: attribution, Side: side, TokenAmount: amount.String(), QuoteAmount: cost.String(),
		QuoteSymbol: q.symbol, Decoder: Name, Version: Version}
	if amount.Sign() > 0 {
		t.PriceNative = ratio(new(big.Int).Mul(cost, pow10(tokenDecimals)), new(big.Int).Mul(amount, pow10(q.decimals)), 18)
	}
	if q.native {
		t.ValueNative = ratio(cost, pow10(18), 18)
	}
	f.Trades = append(f.Trades, t)
	return nil
}

func (d *Decoder) graduate(ctx context.Context, f *parse.Facts, l evm.Log) error {
	w := words(l.Data)
	if len(w) < 1 {
		return errors.New("short graduation event")
	}
	token := address(w[0])
	q, err := d.quote(ctx, token)
	if err != nil {
		return err
	}
	f.Pools = append(f.Pools, parse.Pool{ID: bytesOf(token), Token: bytesOf(token), Quote: bytesOf(q.asset), QuoteSymbol: q.symbol,
		Protocol: "four_meme_curve", Dex: Name, Status: "graduated", Ix: l.Index})
	return nil
}

func (d *Decoder) pool(tx evm.Tx, l evm.Log, token evm.Address, q quote, created bool, status, rt, rq string) parse.Pool {
	return parse.Pool{ID: bytesOf(token), Token: bytesOf(token), Quote: bytesOf(q.asset), QuoteSymbol: q.symbol,
		Protocol: "four_meme_curve", Dex: Name, Created: created, Status: status, ReserveToken: rt, ReserveQuote: rq, Tx: tx.Index, Ix: l.Index}
}

// quote resolves a token's quote asset; BNB is recorded as the wrapped coin.
func (d *Decoder) quote(ctx context.Context, token evm.Address) (quote, error) {
	info, err := d.lookup.FourMeme(ctx, token)
	if err != nil {
		return quote{}, fmt.Errorf("getTokenInfo %s: %w: %w", token.Hex(), parse.ErrRetry, err)
	}
	if !info.Found {
		return quote{}, fmt.Errorf("token %s is unknown to TokenManagerHelper3", token.Hex())
	}
	if info.Quote == (evm.Address{}) {
		return quote{asset: d.cfg.WrappedNative, symbol: d.cfg.NativeSymbol, decimals: 18, native: true}, nil
	}
	m, err := d.lookup.ERC20(ctx, info.Quote)
	if errors.Is(err, chaininfo.ErrReverted) {
		return quote{}, fmt.Errorf("quote %s is not an ERC-20: %w", info.Quote.Hex(), err)
	}
	if err != nil {
		return quote{}, fmt.Errorf("quote %s metadata: %w: %w", info.Quote.Hex(), parse.ErrRetry, err)
	}
	sym, ok := d.cfg.QuoteSymbols[info.Quote]
	if !ok {
		sym = m.Symbol
	}
	return quote{asset: info.Quote, symbol: sym, decimals: int(m.Decimals)}, nil
}

func words(b []byte) [][]byte {
	out := make([][]byte, 0, len(b)/32)
	for i := 0; i+32 <= len(b); i += 32 {
		out = append(out, b[i:i+32])
	}
	return out
}

func address(w []byte) evm.Address { return evm.Address(w[12:32]) }

func abiString(data, offset []byte) (string, error) {
	o := new(big.Int).SetBytes(offset)
	if !o.IsInt64() || o.Int64()+32 > int64(len(data)) {
		return "", errors.New("string offset out of range")
	}
	n := new(big.Int).SetBytes(data[o.Int64() : o.Int64()+32])
	end := o.Int64() + 32 + n.Int64()
	if !n.IsInt64() || end > int64(len(data)) {
		return "", errors.New("string length out of range")
	}
	return string(data[o.Int64()+32 : end]), nil
}

func ratio(num, den *big.Int, places int) string {
	return new(big.Rat).SetFrac(num, den).FloatString(places)
}
func pow10(n int) *big.Int         { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }
func bytesOf(a evm.Address) []byte { return append([]byte(nil), a[:]...) }
func bytesOf32(h evm.Hash) []byte  { return append([]byte(nil), h[:]...) }
