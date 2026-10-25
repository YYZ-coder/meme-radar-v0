// Package pump decodes Pump.fun bonding-curve and PumpSwap AMM events from
// their Anchor self-CPI events (layouts from the official IDLs,
// github.com/pump-fun/pump-public-docs, 2026-10-23). Every event is also
// logged as "Program data", but CPI events cannot be truncated like logs.
package pump

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"slices"
	"strconv"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/solana"
)

// Decoder names and versions recorded on facts.
const (
	CurveDecoder = "pump_fun"
	AMMDecoder   = "pumpswap"
	Version      = 1
)

var (
	curveProgram = solana.MustKey("6EF8rrecthR5Dkzon8Nwu78hRvfCKubJ14M5uBEwF6P")
	ammProgram   = solana.MustKey("pAMMBay6oceH9fJKBRHGP5D4bD4sWpmSwMn52FMfXEA")
	wsol         = solana.MustKey("So11111111111111111111111111111111111111112")
)

// quoteAssets are mints treated as the quote side; a PumpSwap pool whose
// base is one of them tracks its quote mint instead.
var quoteAssets = map[solana.Key]string{
	wsol: "SOL",
	solana.MustKey("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"): "USDC",
	solana.MustKey("Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"): "USDT",
}

// pumpTokenDecimals is fixed by the Pump.fun program for every mint.
const pumpTokenDecimals = 6

func disc(s string) [8]byte {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 8 {
		panic(s)
	}
	return [8]byte(b)
}

// Event discriminators (IDL "events").
var (
	evCreate    = disc("1b72a94ddeeb6376")
	evTrade     = disc("bddb7fd34ee661ee")
	evComplete  = disc("5f72619cd42e9808")
	evMigration = disc("bde95db95c94ea94")
	evBuy       = disc("67f4521f2cf57777")
	evSell      = disc("3e2f370aa503dc2a")
	evPool      = disc("b1310cd2a076a774")
	evDeposit   = disc("78f83d531f8e6b90")
	evWithdraw  = disc("1609851aa02c47c0")
)

// curveAccount is the bonding_curve account index of each Pump.fun
// instruction that emits TradeEvent (IDL "instructions").
var curveAccount = map[[8]byte]int{
	disc("66063d1201daebea"): 3,  // buy
	disc("33e685a4017f83ad"): 3,  // sell
	disc("38fc74089edfcd5f"): 3,  // buy_exact_sol_in
	disc("b817ee6167c5d33d"): 10, // buy_v2
	disc("5df6823ce7e940b2"): 10, // sell_v2
	disc("c2ab1c46684d5b2f"): 10, // buy_exact_quote_in_v2
	disc("07051dc4f5176550"): 5,  // buy_v3
	disc("1c92de7726c469d5"): 5,  // sell_v3
	disc("e1f7501ed5b38488"): 5,  // buy_exact_quote_in_v3
}

// bondingCurve is the emitting instruction's bonding_curve account, or the
// curve's program address for instructions without one (multi_hop_curve_swap).
func bondingCurve(em solana.Instruction, mint solana.Key) (solana.Key, error) {
	if len(em.Data) >= 8 {
		if idx, ok := curveAccount[[8]byte(em.Data[:8])]; ok && idx < len(em.Accounts) {
			return em.Accounts[idx], nil
		}
	}
	k, _, err := solana.FindProgramAddress([][]byte{[]byte("bonding-curve"), mint[:]}, curveProgram)
	return k, err
}

// PumpSwap trade and liquidity instructions (except multi_hop_swap) start
// with pool, …, base_mint (3), quote_mint (4).
const (
	ammPool      = 0
	ammBaseMint  = 3
	ammQuoteMint = 4
)

// Decode returns the facts of tx. Failed transactions yield none.
func Decode(tx solana.Tx) parse.Facts {
	var f parse.Facts
	for _, e := range tx.Events(curveProgram) {
		if err := decodeCurve(&f, tx, e); err != nil {
			f.Failures = append(f.Failures, failure(tx, CurveDecoder, e, err))
		}
	}
	var events []ammEvent
	for _, e := range tx.Events(ammProgram) {
		a, ok, err := readAMM(e)
		switch {
		case err != nil:
			f.Failures = append(f.Failures, failure(tx, AMMDecoder, e, err))
		case ok:
			events = append(events, a)
		}
	}
	mints := poolMints(tx, events)
	for _, a := range events {
		m, ok := mints[a.pool]
		if !ok {
			em := tx.Instructions[a.Emitter]
			f.Failures = append(f.Failures, failure(tx, AMMDecoder, a.Event,
				fmt.Errorf("cannot resolve the mints of pool %s from instruction %x", a.pool, em.Data[:min(8, len(em.Data))])))
			continue
		}
		ammFacts(&f, tx, a, m[0], m[1])
	}
	return f
}

func failure(tx solana.Tx, decoder string, e solana.Event, err error) parse.Failure {
	return parse.Failure{Tx: tx.Index, TxHash: tx.Signature[:], Decoder: decoder,
		Err: fmt.Sprintf("event %x at instruction %d: %v", e.Data[:8], e.Index, err)}
}

func decodeCurve(f *parse.Facts, tx solana.Tx, e solana.Event) error {
	r := &borsh{b: e.Data[8:]}
	switch [8]byte(e.Data[:8]) {
	case evCreate:
		name, symbol, uri := r.str(), r.str(), r.str()
		mint, curve, _, creator := r.key(), r.key(), r.key(), r.key()
		r.i64()
		r.u64()
		r.u64()
		realToken, supply := r.u64(), r.u64()
		r.key()       // token_program
		r.skip(2)     // is_mayhem_mode, is_cashback_enabled
		quote := wsol // SOL curve unless a quote mint is set
		if r.more() {
			quote = nativeIfZero(r.key())
		}
		if r.err != nil {
			return r.err
		}
		f.Tokens = append(f.Tokens, parse.Token{Address: k(mint), Created: true, Name: name, Symbol: symbol, URI: uri,
			Decimals: decimals(tx, mint, pumpTokenDecimals), TotalSupply: u(supply), Creator: k(creator), LaunchPlatform: CurveDecoder})
		f.Pools = append(f.Pools, curvePool(tx, e, curve, mint, quote, "active", true, u(realToken), "0"))
	case evTrade:
		mint, solAmount, tokenAmount, isBuy, user := r.key(), r.u64(), r.u64(), r.boolean(), r.key()
		r.i64()
		r.u64()
		r.u64()
		realSol, realToken := r.u64(), r.u64()
		r.key()
		r.u64()
		r.u64()
		r.key()
		r.u64()
		r.u64()
		r.boolean()
		r.u64()
		r.u64()
		r.u64()
		r.i64()
		r.str()   // ix_name
		r.skip(1) // mayhem_mode
		r.skip(32)
		if r.err != nil {
			return r.err
		}
		quote, quoteAmount, realQuote := wsol, solAmount, realSol
		if r.more() { // shareholders, then quote_mint and its amounts
			n := r.take(4)
			if n != nil {
				r.skip(34 * int(uint32(n[0])|uint32(n[1])<<8|uint32(n[2])<<16|uint32(n[3])<<24))
			}
			if q := r.key(); r.err == nil && q != (solana.Key{}) {
				quote, quoteAmount = q, r.u64()
				r.u64()
				realQuote = r.u64()
			}
			if r.err != nil {
				return r.err
			}
		}
		curve, err := bondingCurve(tx.Instructions[e.Emitter], mint)
		if err != nil {
			return err
		}
		dec := decimals(tx, mint, pumpTokenDecimals)
		f.Tokens = append(f.Tokens, parse.Token{Address: k(mint), Decimals: dec, LaunchPlatform: CurveDecoder})
		f.Pools = append(f.Pools, curvePool(tx, e, curve, mint, quote, "", false, u(realToken), u(realQuote)))
		side := "sell"
		if isBuy {
			side = "buy"
		}
		f.Trades = append(f.Trades, trade(tx, e, CurveDecoder, curve, mint, quote, user, side, tokenAmount, quoteAmount, dec))
	case evComplete:
		_, mint, curve := r.key(), r.key(), r.key()
		r.i64()
		quote := wsol
		if r.more() {
			quote = nativeIfZero(r.key())
		}
		if r.err != nil {
			return r.err
		}
		f.Pools = append(f.Pools, curvePool(tx, e, curve, mint, quote, "graduated", false, "", ""))
	case evMigration:
		_, mint := r.key(), r.key()
		r.u64()
		r.u64()
		r.u64()
		curve := r.key()
		r.i64()
		pool := r.key()
		quote := wsol
		if r.more() {
			quote = nativeIfZero(r.key())
		}
		if r.err != nil {
			return r.err
		}
		p := curvePool(tx, e, curve, mint, quote, "graduated", false, "", "")
		p.GraduatedTo = k(pool)
		f.Pools = append(f.Pools, p)
	}
	return nil
}

// ammEvent is a decoded PumpSwap trade, liquidity or pool-creation event.
type ammEvent struct {
	solana.Event
	kind                    [8]byte
	pool, user              solana.Key
	baseAmount, quoteAmount uint64
	poolBase                uint64 // base reserve before the event
	baseDec, quoteDec       uint8  // CreatePoolEvent only
	eventBase, eventQuote   solana.Key
}

func readAMM(e solana.Event) (ammEvent, bool, error) {
	a := ammEvent{Event: e, kind: [8]byte(e.Data[:8])}
	if !slices.Contains([][8]byte{evBuy, evSell, evPool, evDeposit, evWithdraw}, a.kind) {
		return a, false, nil
	}
	r := &borsh{b: e.Data[8:]}
	r.i64() // timestamp
	switch a.kind {
	case evBuy, evSell:
		a.baseAmount = r.u64()
		r.skip(8 * 3)
		a.poolBase = r.u64()
		r.u64()
		a.quoteAmount = r.u64()
		r.skip(8 * 6)
		a.pool, a.user = r.key(), r.key()
	case evDeposit, evWithdraw:
		r.skip(8 * 5)
		a.poolBase = r.u64()
		r.u64()
		a.baseAmount, a.quoteAmount = r.u64(), r.u64()
		r.u64()
		a.pool, a.user = r.key(), r.key()
	case evPool:
		r.u16()
		a.user = r.key() // creator
		a.eventBase, a.eventQuote = r.key(), r.key()
		a.baseDec, a.quoteDec = r.u8(), r.u8()
		a.baseAmount, a.quoteAmount = r.u64(), r.u64()
		r.skip(8 * 5)
		r.u8()
		a.pool = r.key()
	}
	return a, true, r.err
}

// poolMints resolves (base, quote) per pool once per transaction: from a
// pool creation, else from an instruction that starts with the pool, else
// (multi_hop_swap passes pools in undocumented remaining accounts) from the
// pool's two vaults after the transaction, the base being the one at the
// last event's reserve moved by its amount.
func poolMints(tx solana.Tx, events []ammEvent) map[solana.Key][2]solana.Key {
	out := map[solana.Key][2]solana.Key{}
	last := map[solana.Key]ammEvent{}
	for _, a := range events {
		em := tx.Instructions[a.Emitter]
		switch {
		case a.kind == evPool:
			out[a.pool] = [2]solana.Key{a.eventBase, a.eventQuote}
		case len(em.Accounts) > ammQuoteMint && em.Accounts[ammPool] == a.pool:
			if _, ok := out[a.pool]; !ok {
				out[a.pool] = [2]solana.Key{em.Accounts[ammBaseMint], em.Accounts[ammQuoteMint]}
			}
		}
		last[a.pool] = a
	}
	for pool, a := range last {
		if _, ok := out[pool]; ok {
			continue
		}
		after := a.poolBase + a.baseAmount
		if a.kind == evBuy || a.kind == evWithdraw {
			after = a.poolBase - a.baseAmount
		}
		if base, quote, ok := vaults(tx, pool, after); ok {
			out[pool] = [2]solana.Key{base, quote}
		}
	}
	return out
}

func ammFacts(f *parse.Facts, tx solana.Tx, a ammEvent, base, quote solana.Key) {
	// Token perspective: a pool whose base is a quote asset tracks its quote mint.
	_, baseIsQuote := quoteAssets[base]
	_, quoteIsQuote := quoteAssets[quote]
	flip := baseIsQuote && !quoteIsQuote
	token, quoteMint, tokenAmount, qAmount, tokenDec := base, quote, a.baseAmount, a.quoteAmount, a.baseDec
	if flip {
		token, quoteMint, tokenAmount, qAmount, tokenDec = quote, base, a.quoteAmount, a.baseAmount, a.quoteDec
	}
	var dec *int16
	if a.kind == evPool {
		d := int16(tokenDec)
		dec = &d
	} else {
		dec = decimals(tx, token, -1)
	}
	f.Tokens = append(f.Tokens, parse.Token{Address: k(token), Decimals: dec})
	p := parse.Pool{ID: k(a.pool), Token: k(token), Quote: k(quoteMint), QuoteSymbol: quoteAssets[quoteMint],
		Protocol: AMMDecoder, Dex: AMMDecoder, Tx: tx.Index, Ix: a.Index,
		ReserveToken: vault(tx, a.pool, token), ReserveQuote: vault(tx, a.pool, quoteMint)}
	if a.kind == evPool {
		p.Created, p.Status = true, "active"
	}
	f.Pools = append(f.Pools, p)
	switch a.kind {
	case evBuy, evSell:
		side := "sell"
		if (a.kind == evBuy) != flip {
			side = "buy"
		}
		f.Trades = append(f.Trades, trade(tx, a.Event, AMMDecoder, a.pool, token, quoteMint, a.user, side, tokenAmount, qAmount, dec))
	default:
		kind := "remove"
		if a.kind != evWithdraw {
			kind = "add"
		}
		f.Liquidity = append(f.Liquidity, parse.Liquidity{Tx: tx.Index, Ix: a.Index, TxHash: tx.Signature[:], Pool: k(a.pool),
			Token: k(token), Wallet: k(a.user), Kind: kind, TokenAmount: u(tokenAmount), QuoteAmount: u(qAmount),
			QuoteSymbol: quoteAssets[quoteMint], ValueNative: native(quoteMint, qAmount), Decoder: AMMDecoder, Version: Version})
	}
}

func curvePool(tx solana.Tx, e solana.Event, curve, mint, quote solana.Key, status string, created bool, rt, rq string) parse.Pool {
	return parse.Pool{ID: k(curve), Token: k(mint), Quote: k(quote), QuoteSymbol: quoteAssets[quote], Protocol: "pump_curve",
		Dex: CurveDecoder, Created: created, Status: status, ReserveToken: rt, ReserveQuote: rq, Tx: tx.Index, Ix: e.Index}
}

func trade(tx solana.Tx, e solana.Event, decoder string, pool, token, quote, user solana.Key, side string, tokenAmount, quoteAmount uint64, dec *int16) parse.Trade {
	attribution := "inferred"
	if slices.Contains(tx.Signers, user) {
		attribution = "confirmed"
	}
	return parse.Trade{Tx: tx.Index, Ix: e.Index, TxHash: tx.Signature[:], Pool: k(pool), Token: k(token), Wallet: k(user),
		Attribution: attribution, Side: side, TokenAmount: u(tokenAmount), QuoteAmount: u(quoteAmount),
		QuoteSymbol: quoteAssets[quote], PriceNative: price(tx, tokenAmount, quoteAmount, quote, dec),
		ValueNative: native(quote, quoteAmount), Decoder: decoder, Version: Version}
}

// price is quote per token in whole units, 18 decimal places.
func price(tx solana.Tx, tokenAmount, quoteAmount uint64, quote solana.Key, tokenDec *int16) string {
	qd, ok := tx.Decimals[quote]
	if quote == wsol {
		qd, ok = 9, true
	}
	if tokenDec == nil || !ok || tokenAmount == 0 {
		return ""
	}
	num := new(big.Int).Mul(new(big.Int).SetUint64(quoteAmount), pow10(int(*tokenDec)))
	den := new(big.Int).Mul(new(big.Int).SetUint64(tokenAmount), pow10(int(qd)))
	return new(big.Rat).SetFrac(num, den).FloatString(18)
}

// native is the amount in SOL when the quote is SOL.
func native(quote solana.Key, amount uint64) string {
	if quote != wsol {
		return ""
	}
	return new(big.Rat).SetFrac(new(big.Int).SetUint64(amount), pow10(9)).FloatString(9)
}

func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }

// decimals of mint from the transaction's token balances, else fallback
// (negative: unknown).
func decimals(tx solana.Tx, mint solana.Key, fallback int16) *int16 {
	if d, ok := tx.Decimals[mint]; ok {
		v := int16(d)
		return &v
	}
	if fallback < 0 {
		return nil
	}
	return &fallback
}

// vaults finds the pool's two token accounts and returns the mint holding
// baseAfter as base.
func vaults(tx solana.Tx, pool solana.Key, baseAfter uint64) (base, quote solana.Key, ok bool) {
	var held []solana.TokenBalance
	for _, b := range tx.PostBalances {
		if b.Owner == pool {
			held = append(held, b)
		}
	}
	if len(held) != 2 || held[0].Mint == held[1].Mint || (held[0].Amount == baseAfter) == (held[1].Amount == baseAfter) {
		return base, quote, false
	}
	if held[1].Amount == baseAfter {
		held[0], held[1] = held[1], held[0]
	}
	return held[0].Mint, held[1].Mint, true
}

// vault is the post-transaction balance of the pool's account of mint.
func vault(tx solana.Tx, pool, mint solana.Key) string {
	for _, b := range tx.PostBalances {
		if b.Owner == pool && b.Mint == mint {
			return strconv.FormatUint(b.Amount, 10)
		}
	}
	return ""
}

func nativeIfZero(q solana.Key) solana.Key {
	if q == (solana.Key{}) {
		return wsol
	}
	return q
}

func k(key solana.Key) []byte { return slices.Clone(key[:]) }
func u(v uint64) string       { return strconv.FormatUint(v, 10) }
