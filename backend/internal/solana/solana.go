// Package solana decodes the Solana block payloads stored by ingest
// (getBlock with base64 transactions, see 02-data-model §7.1) into
// transactions with resolved account keys and instructions in execution order.
package solana

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"filippo.io/edwards25519"
)

// Key is a 32-byte account address.
type Key [32]byte

// Instruction is one top-level or inner instruction.
type Instruction struct {
	Top      int // index of the top-level instruction it belongs to
	Depth    int // 1 for top-level, 2+ for CPIs (stackHeight)
	Program  Key
	Accounts []Key
	Data     []byte
}

// Tx is a decoded transaction.
type Tx struct {
	Index     int // position in the block
	Signature [64]byte
	Signers   []Key // the fee payer first
	Keys      []Key // static keys, then loaded writable, then loaded readonly
	// Instructions in execution order: each top-level instruction followed
	// by its inner instructions.
	Instructions []Instruction
	Failed       bool
	// Decimals of every mint that appears in the token balances.
	Decimals map[Key]uint8
	// PostBalances are the token account balances after the transaction.
	PostBalances []TokenBalance
	// PreBalances are the token account balances before the transaction.
	PreBalances []TokenBalance
}

// TokenBalance is a token account balance from the transaction meta.
type TokenBalance struct {
	Mint, Owner  Key
	Amount       uint64
	AccountIndex int
}

// Block is a decoded block payload.
type Block struct {
	Time time.Time
	Txs  []Tx
}

// ParseBlock decodes a stored getBlock result.
func ParseBlock(payload []byte) (Block, error) {
	var b struct {
		BlockTime    *int64            `json:"blockTime"`
		Transactions []json.RawMessage `json:"transactions"`
	}
	if err := json.Unmarshal(payload, &b); err != nil {
		return Block{}, fmt.Errorf("solana: block: %w", err)
	}
	if b.BlockTime == nil {
		return Block{}, fmt.Errorf("solana: block has no blockTime")
	}
	out := Block{Time: time.Unix(*b.BlockTime, 0), Txs: make([]Tx, 0, len(b.Transactions))}
	for i, raw := range b.Transactions {
		tx, err := ParseTx(i, raw)
		if err != nil {
			return Block{}, fmt.Errorf("solana: transaction %d: %w", i, err)
		}
		out.Txs = append(out.Txs, tx)
	}
	return out, nil
}

type rawTx struct {
	Transaction []string `json:"transaction"`
	Meta        struct {
		Err             json.RawMessage `json:"err"`
		LoadedAddresses struct {
			Writable []string `json:"writable"`
			Readonly []string `json:"readonly"`
		} `json:"loadedAddresses"`
		InnerInstructions []struct {
			Index        int `json:"index"`
			Instructions []struct {
				ProgramIDIndex int    `json:"programIdIndex"`
				Accounts       []int  `json:"accounts"`
				Data           string `json:"data"`
				StackHeight    *int   `json:"stackHeight"`
			} `json:"instructions"`
		} `json:"innerInstructions"`
		PreTokenBalances  []tokenBalance `json:"preTokenBalances"`
		PostTokenBalances []tokenBalance `json:"postTokenBalances"`
	} `json:"meta"`
}

type tokenBalance struct {
	AccountIndex  int    `json:"accountIndex"`
	Mint          string `json:"mint"`
	Owner         string `json:"owner"`
	UITokenAmount struct {
		Amount   string `json:"amount"`
		Decimals uint8  `json:"decimals"`
	} `json:"uiTokenAmount"`
}

// ParseTx decodes one entry of a getBlock "transactions" array.
func ParseTx(index int, raw json.RawMessage) (Tx, error) {
	var t rawTx
	if err := json.Unmarshal(raw, &t); err != nil {
		return Tx{}, err
	}
	if len(t.Transaction) != 2 || t.Transaction[1] != "base64" {
		return Tx{}, fmt.Errorf("transaction is not base64-encoded")
	}
	wire, err := base64.StdEncoding.DecodeString(t.Transaction[0])
	if err != nil {
		return Tx{}, err
	}
	m, err := parseWire(wire)
	if err != nil {
		return Tx{}, err
	}
	tx := Tx{Index: index, Signature: m.signature, Failed: len(t.Meta.Err) > 0 && string(t.Meta.Err) != "null"}
	tx.Keys = m.keys
	for _, list := range [][]string{t.Meta.LoadedAddresses.Writable, t.Meta.LoadedAddresses.Readonly} {
		for _, s := range list {
			k, err := ParseKey(s)
			if err != nil {
				return Tx{}, err
			}
			tx.Keys = append(tx.Keys, k)
		}
	}
	if m.numSigners > len(m.keys) {
		return Tx{}, fmt.Errorf("%d signers but %d static keys", m.numSigners, len(m.keys))
	}
	tx.Signers = m.keys[:m.numSigners]
	key := func(i int) (Key, error) {
		if i < 0 || i >= len(tx.Keys) {
			return Key{}, fmt.Errorf("account index %d out of %d keys", i, len(tx.Keys))
		}
		return tx.Keys[i], nil
	}
	inner := map[int]int{}
	for g, group := range t.Meta.InnerInstructions {
		inner[group.Index] = g
	}
	for top, w := range m.instructions {
		ix, err := resolve(top, 1, w.program, bytesToInts(w.accounts), w.data, key)
		if err != nil {
			return Tx{}, err
		}
		tx.Instructions = append(tx.Instructions, ix)
		g, ok := inner[top]
		if !ok {
			continue
		}
		for _, in := range t.Meta.InnerInstructions[g].Instructions {
			data, err := DecodeBase58OrEmpty(in.Data)
			if err != nil {
				return Tx{}, err
			}
			depth := 2
			if in.StackHeight != nil {
				depth = *in.StackHeight
			}
			ix, err := resolve(top, depth, in.ProgramIDIndex, in.Accounts, data, key)
			if err != nil {
				return Tx{}, err
			}
			tx.Instructions = append(tx.Instructions, ix)
		}
	}
	tx.Decimals = map[Key]uint8{}
	for _, list := range [][]tokenBalance{t.Meta.PreTokenBalances, t.Meta.PostTokenBalances} {
		for _, b := range list {
			k, err := ParseKey(b.Mint)
			if err != nil {
				return Tx{}, err
			}
			tx.Decimals[k] = b.UITokenAmount.Decimals
		}
	}
	for i, list := range [][]tokenBalance{t.Meta.PreTokenBalances, t.Meta.PostTokenBalances} {
		out := &tx.PreBalances
		if i == 1 {
			out = &tx.PostBalances
		}
		for _, b := range list {
			if b.Owner == "" {
				continue
			}
			mint, err := ParseKey(b.Mint)
			if err != nil {
				return Tx{}, err
			}
			owner, err := ParseKey(b.Owner)
			if err != nil {
				return Tx{}, err
			}
			amount, err := strconv.ParseUint(b.UITokenAmount.Amount, 10, 64)
			if err != nil {
				return Tx{}, fmt.Errorf("token balance %q: %w", b.UITokenAmount.Amount, err)
			}
			*out = append(*out, TokenBalance{Mint: mint, Owner: owner, Amount: amount, AccountIndex: b.AccountIndex})
		}
	}
	return tx, nil
}

func resolve(top, depth, program int, accounts []int, data []byte, key func(int) (Key, error)) (Instruction, error) {
	p, err := key(program)
	if err != nil {
		return Instruction{}, err
	}
	ix := Instruction{Top: top, Depth: depth, Program: p, Data: data, Accounts: make([]Key, len(accounts))}
	for i, a := range accounts {
		if ix.Accounts[i], err = key(a); err != nil {
			return Instruction{}, err
		}
	}
	return ix, nil
}

func bytesToInts(b []byte) []int {
	out := make([]int, len(b))
	for i, v := range b {
		out[i] = int(v)
	}
	return out
}

// DecodeBase58OrEmpty decodes s, mapping "" to empty data.
func DecodeBase58OrEmpty(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return DecodeBase58(s)
}

// Event is an Anchor event emitted through a self-CPI (emit_cpi!).
type Event struct {
	// Index is the event's own position in Tx.Instructions, unique within
	// the transaction (used as ix_index).
	Index int
	// Emitter indexes Tx.Instructions: the program instruction that
	// emitted the event, whose accounts identify the pool and mints.
	Emitter int
	// Data starts with the 8-byte event discriminator.
	Data []byte
}

// Events returns the CPI events of program in execution order. Failed
// transactions have none.
func (tx Tx) Events(program Key) []Event {
	if tx.Failed {
		return nil
	}
	var out []Event
	for i, ix := range tx.Instructions {
		if ix.Program != program || ix.Depth < 2 || len(ix.Data) < 16 || !bytes.Equal(ix.Data[:8], eventTag[:]) {
			continue
		}
		emitter := -1
		for j := i - 1; j >= 0 && tx.Instructions[j].Top == ix.Top; j-- {
			if p := tx.Instructions[j]; p.Depth == ix.Depth-1 && p.Program == program {
				emitter = j
				break
			}
		}
		if emitter >= 0 {
			out = append(out, Event{Index: i, Emitter: emitter, Data: ix.Data[8:]})
		}
	}
	return out
}

// eventTag prefixes Anchor self-CPI event data (sha256("anchor:event")[:8]
// as stored little-endian).
var eventTag = [8]byte{0xe4, 0x45, 0xa5, 0x2e, 0x51, 0xcb, 0x9a, 0x1d}

// FindProgramAddress derives the program address of seeds: the first
// SHA-256(seeds‖bump‖program‖"ProgramDerivedAddress"), bump from 255 down,
// that is not an ed25519 point.
func FindProgramAddress(seeds [][]byte, program Key) (Key, uint8, error) {
	for bump := 255; bump >= 0; bump-- {
		h := sha256.New()
		for _, s := range seeds {
			h.Write(s)
		}
		h.Write([]byte{byte(bump)})
		h.Write(program[:])
		h.Write([]byte("ProgramDerivedAddress"))
		k := Key(h.Sum(nil))
		if _, err := new(edwards25519.Point).SetBytes(k[:]); err != nil {
			return k, uint8(bump), nil
		}
	}
	return Key{}, 0, fmt.Errorf("solana: no program address for %d seeds", len(seeds))
}
