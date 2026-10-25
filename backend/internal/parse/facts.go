// Package parse turns stored raw blocks into facts (tokens, pools, trades,
// liquidity events) with per-protocol decoders; the parse role writes them
// per block and marks the block parsed (05-ingest, 06-parse).
package parse

import "errors"

// Facts are the decoded results of one transaction or block. Amounts are
// atomic units; decimal strings are empty when unknown (stored as NULL).
type Facts struct {
	Tokens    []Token
	Pools     []Pool
	Trades    []Trade
	Liquidity []Liquidity
	Failures  []Failure
}

// Add appends o to f.
func (f *Facts) Add(o Facts) {
	f.Tokens = append(f.Tokens, o.Tokens...)
	f.Pools = append(f.Pools, o.Pools...)
	f.Trades = append(f.Trades, o.Trades...)
	f.Liquidity = append(f.Liquidity, o.Liquidity...)
	f.Failures = append(f.Failures, o.Failures...)
}

// Token is a token seen in a transaction. Created marks a creation event,
// whose metadata fields are then authoritative.
type Token struct {
	Address        []byte
	Created        bool
	Name, Symbol   string
	URI            string
	Decimals       *int16
	TotalSupply    string // decimal, empty when unknown
	Creator        []byte
	LaunchPlatform string
}

// Pool is a pool seen in a transaction. Empty fields leave stored values
// unchanged; reserves describe the state after the transaction.
type Pool struct {
	ID, Token, Quote []byte
	QuoteSymbol      string
	Protocol, Dex    string
	Created          bool
	Status           string // active, graduated
	GraduatedTo      []byte
	ReserveToken     string
	ReserveQuote     string
	Tx, Ix           int // position of the state, for ordering within a block
}

// Trade is one swap from the token's point of view.
type Trade struct {
	Tx, Ix      int
	TxHash      []byte
	Pool, Token []byte
	Wallet      []byte
	Attribution string // confirmed, inferred, ambiguous
	Side        string // buy, sell
	TokenAmount string // atomic units, decimal
	QuoteAmount string
	QuoteSymbol string
	PriceNative string // quote per token in whole units
	ValueNative string // value in the chain's native coin, when the quote is native
	Decoder     string
	Version     int
}

// Liquidity is a liquidity add or remove.
type Liquidity struct {
	Tx, Ix      int
	TxHash      []byte
	Pool, Token []byte
	Wallet      []byte
	Kind        string // add, remove
	TokenAmount string // atomic units, decimal
	QuoteAmount string
	QuoteSymbol string
	ValueNative string
	Decoder     string
	Version     int
}

// Failure is a transaction a decoder recognised but could not decode.
type Failure struct {
	Tx      int
	TxHash  []byte
	Decoder string
	Err     string
}

// ErrRetry marks a decode error that may succeed later (e.g. an RPC lookup
// failed); the block is retried instead of being recorded as a failure.
var ErrRetry = errors.New("parse: retry later")

// Balance is one transaction's atomic token balance change. Solana uses
// transaction-level Ix=0; EVM uses the receipt log index.
type Balance struct {
	Tx, Ix       int
	Owner, Token []byte
	Delta        string
}
