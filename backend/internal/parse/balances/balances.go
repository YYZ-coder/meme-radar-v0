// Package balances decodes token balance changes separately from trade facts.
package balances

import (
	"bytes"
	"slices"

	"math/big"

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/solana"
)

// Version is the independent balance decoder version (D154).
const Version = 1

// Solana aggregates token account changes by owner and tracked mint.
func Solana(tx solana.Tx, tracked map[string]struct{}) []parse.Balance {
	if tx.Failed {
		return nil
	}
	type pair struct{ owner, mint solana.Key }
	totals := map[pair]*big.Int{}
	for i, list := range [][]solana.TokenBalance{tx.PreBalances, tx.PostBalances} {
		for _, b := range list {
			if _, ok := tracked[string(b.Mint[:])]; !ok {
				continue
			}
			key := pair{b.Owner, b.Mint}
			if totals[key] == nil {
				totals[key] = new(big.Int)
			}
			amount := new(big.Int).SetUint64(b.Amount)
			if i == 0 {
				totals[key].Sub(totals[key], amount)
			} else {
				totals[key].Add(totals[key], amount)
			}
		}
	}
	var out []parse.Balance
	for key, delta := range totals {
		if delta.Sign() != 0 {
			out = append(out, parse.Balance{Tx: tx.Index, Owner: key.owner[:], Token: key.mint[:], Delta: delta.String()})
		}
	}
	slices.SortFunc(out, func(a, b parse.Balance) int {
		if c := bytes.Compare(a.Token, b.Token); c != 0 {
			return c
		}
		return bytes.Compare(a.Owner, b.Owner)
	})
	return out
}

// EVM emits signed Transfer deltas for tracked ERC-20 contracts.
func EVM(tx evm.Tx, tracked map[string]struct{}) []parse.Balance {
	if tx.Failed {
		return nil
	}
	var out []parse.Balance
	for _, log := range tx.Logs {
		if _, ok := tracked[string(log.Address[:])]; !ok {
			continue
		}
		if len(log.Topics) != 3 || log.Topics[0] != transfer || len(log.Data) != 32 {
			continue
		}
		from, to := evm.Address(log.Topics[1][12:]), evm.Address(log.Topics[2][12:])
		amount := new(big.Int).SetBytes(log.Data)
		if amount.Sign() == 0 || from == to {
			continue
		}
		if from != (evm.Address{}) {
			out = append(out, parse.Balance{Tx: tx.Index, Ix: log.Index, Token: log.Address[:], Owner: from[:], Delta: new(big.Int).Neg(amount).String()})
		}
		out = append(out, parse.Balance{Tx: tx.Index, Ix: log.Index, Token: log.Address[:], Owner: to[:], Delta: amount.String()})
	}
	return out
}

var transfer = evm.Topic("Transfer(address,address,uint256)")
