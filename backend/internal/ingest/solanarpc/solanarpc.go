// Package solanarpc is the free-tier Solana block source (D144): full blocks
// via getBlock and the confirmed head via getSlot.
package solanarpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/meme-radar/radar/internal/ingest"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/solana"
)

// Source implements ingest.Source over Solana JSON-RPC.
type Source struct {
	rpc *jsonrpc.Client
}

// New returns a source using rpc.
func New(rpc *jsonrpc.Client) *Source { return &Source{rpc: rpc} }

var confirmed = map[string]any{"commitment": "confirmed"}

// Head returns the newest confirmed slot.
func (s *Source) Head(ctx context.Context) (uint64, error) {
	res, err := s.rpc.Call(ctx, "getSlot", []any{confirmed})
	if err != nil {
		return 0, err
	}
	var slot uint64
	if err := json.Unmarshal(res, &slot); err != nil {
		return 0, fmt.Errorf("solanarpc: getSlot result %.100s: %w", res, err)
	}
	return slot, nil
}

// blockOpts requests every transaction with metadata. Version 1 is the newest
// transaction version; a lower value makes blocks containing it fail.
var blockOpts = map[string]any{
	"commitment":                     "confirmed",
	"encoding":                       "base64",
	"transactionDetails":             "full",
	"rewards":                        false,
	"maxSupportedTransactionVersion": 1,
}

// Solana RPC error codes for slots without a served block.
const (
	codeNotAvailable       = -32004 // block not available for slot
	codeSkipped            = -32007 // slot skipped, or missing after a snapshot jump
	codeSkippedLongTerm    = -32009 // slot skipped, or missing in long-term storage
	codeStatusNotAvailable = -32014 // block status not yet available
)

// Block returns the full block of slot.
func (s *Source) Block(ctx context.Context, slot uint64) (ingest.Block, error) {
	res, err := s.rpc.Call(ctx, "getBlock", []any{slot, blockOpts})
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case codeSkipped, codeSkippedLongTerm:
			return ingest.Block{}, fmt.Errorf("slot %d: %w", slot, ingest.ErrSkipped)
		case codeNotAvailable, codeStatusNotAvailable:
			return ingest.Block{}, fmt.Errorf("slot %d: %w", slot, ingest.ErrNotAvailable)
		}
	}
	if err != nil {
		return ingest.Block{}, err
	}
	if string(res) == "null" {
		return ingest.Block{}, fmt.Errorf("slot %d: %w", slot, ingest.ErrNotAvailable)
	}
	var head struct {
		Blockhash         string            `json:"blockhash"`
		PreviousBlockhash string            `json:"previousBlockhash"`
		BlockTime         *int64            `json:"blockTime"`
		Transactions      []json.RawMessage `json:"transactions"`
	}
	if err := json.Unmarshal(res, &head); err != nil {
		return ingest.Block{}, fmt.Errorf("solanarpc: slot %d: %w", slot, err)
	}
	if head.BlockTime == nil {
		return ingest.Block{}, fmt.Errorf("solanarpc: slot %d has no blockTime", slot)
	}
	hash, err := solana.DecodeBase58(head.Blockhash)
	if err != nil {
		return ingest.Block{}, fmt.Errorf("solanarpc: slot %d blockhash: %w", slot, err)
	}
	parent, err := solana.DecodeBase58(head.PreviousBlockhash)
	if err != nil {
		return ingest.Block{}, fmt.Errorf("solanarpc: slot %d previousBlockhash: %w", slot, err)
	}
	return ingest.Block{
		Num: slot, Hash: hash, ParentHash: parent,
		Time: time.Unix(*head.BlockTime, 0), TxCount: len(head.Transactions), Payload: res,
	}, nil
}
