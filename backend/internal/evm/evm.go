// Package evm decodes the EVM block payloads stored by ingest
// ({"block": eth_getBlockByNumber with transactions, "receipts": …}) into
// transactions with their logs, plus Keccak and ABI word helpers.
package evm

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

// Address is a 20-byte account address.
type Address [20]byte

// Hash is a 32-byte hash or topic.
type Hash [32]byte

// Log is one receipt log.
type Log struct {
	Index   int // logIndex within the block
	Address Address
	Topics  []Hash
	Data    []byte
}

// Tx is a transaction with its receipt.
type Tx struct {
	Index  int
	Hash   Hash
	From   Address
	To     Address // zero for contract creation
	Value  *big.Int
	Failed bool
	Logs   []Log
}

// Block is a decoded block payload.
type Block struct {
	Time time.Time
	Txs  []Tx
}

type rawPayload struct {
	Block struct {
		Timestamp    string `json:"timestamp"`
		Transactions []struct {
			Hash  string  `json:"hash"`
			From  string  `json:"from"`
			To    *string `json:"to"`
			Value string  `json:"value"`
		} `json:"transactions"`
	} `json:"block"`
	Receipts []struct {
		TransactionHash string `json:"transactionHash"`
		Status          string `json:"status"`
		Logs            []struct {
			Address  string   `json:"address"`
			Topics   []string `json:"topics"`
			Data     string   `json:"data"`
			LogIndex string   `json:"logIndex"`
		} `json:"logs"`
	} `json:"receipts"`
}

// ParseBlock decodes a stored block payload; receipts are matched to
// transactions by hash.
func ParseBlock(payload []byte) (Block, error) {
	var p rawPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Block{}, fmt.Errorf("evm: block: %w", err)
	}
	ts, err := quantity(p.Block.Timestamp)
	if err != nil {
		return Block{}, fmt.Errorf("evm: block timestamp: %w", err)
	}
	if len(p.Receipts) != len(p.Block.Transactions) {
		return Block{}, fmt.Errorf("evm: %d receipts for %d transactions", len(p.Receipts), len(p.Block.Transactions))
	}
	out := Block{Time: time.Unix(int64(ts), 0), Txs: make([]Tx, len(p.Block.Transactions))}
	for i, t := range p.Block.Transactions {
		r := p.Receipts[i]
		if !strings.EqualFold(r.TransactionHash, t.Hash) {
			return Block{}, fmt.Errorf("evm: receipt %d is for %s, not %s", i, r.TransactionHash, t.Hash)
		}
		tx := Tx{Index: i, Failed: r.Status == "0x0", Value: new(big.Int)}
		if err := firstErr(decode(t.Hash, tx.Hash[:]), decode(t.From, tx.From[:])); err != nil {
			return Block{}, fmt.Errorf("evm: transaction %d: %w", i, err)
		}
		if t.To != nil {
			if err := decode(*t.To, tx.To[:]); err != nil {
				return Block{}, fmt.Errorf("evm: transaction %d to: %w", i, err)
			}
		}
		if _, ok := tx.Value.SetString(strings.TrimPrefix(t.Value, "0x"), 16); !ok && t.Value != "0x0" && t.Value != "" {
			return Block{}, fmt.Errorf("evm: transaction %d value %q", i, t.Value)
		}
		for _, l := range r.Logs {
			var lg Log
			idx, err := quantity(l.LogIndex)
			if err != nil {
				return Block{}, fmt.Errorf("evm: transaction %d log index: %w", i, err)
			}
			lg.Index = int(idx)
			if err := decode(l.Address, lg.Address[:]); err != nil {
				return Block{}, fmt.Errorf("evm: transaction %d log address: %w", i, err)
			}
			for _, tp := range l.Topics {
				var h Hash
				if err := decode(tp, h[:]); err != nil {
					return Block{}, fmt.Errorf("evm: transaction %d topic: %w", i, err)
				}
				lg.Topics = append(lg.Topics, h)
			}
			if lg.Data, err = hex.DecodeString(strings.TrimPrefix(l.Data, "0x")); err != nil {
				return Block{}, fmt.Errorf("evm: transaction %d log data: %w", i, err)
			}
			tx.Logs = append(tx.Logs, lg)
		}
		out.Txs[i] = tx
	}
	return out, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// decode reads a 0x hex string of exactly len(dst) bytes.
func decode(s string, dst []byte) error {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(b) != len(dst) {
		return fmt.Errorf("%q is not %d hex bytes", s, len(dst))
	}
	copy(dst, b)
	return nil
}

func quantity(q string) (uint64, error) {
	if !strings.HasPrefix(q, "0x") {
		return 0, fmt.Errorf("quantity %q lacks 0x", q)
	}
	return strconv.ParseUint(q[2:], 16, 64)
}

// Keccak256 hashes the concatenation of parts.
func Keccak256(parts ...[]byte) Hash {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	return Hash(h.Sum(nil))
}

// Topic is the Keccak-256 of an event signature such as
// "Transfer(address,address,uint256)".
func Topic(sig string) Hash { return Keccak256([]byte(sig)) }

// Hex renders a in lower-case 0x form.
func (a Address) Hex() string { return "0x" + hex.EncodeToString(a[:]) }

// ParseAddress decodes a 0x address in any case.
func ParseAddress(s string) (Address, error) {
	var a Address
	return a, decode(s, a[:])
}

// MustAddress is ParseAddress for constants and validated configuration.
func MustAddress(s string) Address {
	a, err := ParseAddress(s)
	if err != nil {
		panic(err)
	}
	return a
}
