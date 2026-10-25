// Package evmrpc is the Base/BSC block source: full blocks with transactions
// (eth_getBlockByNumber) plus their receipts (eth_getBlockReceipts).
package evmrpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/meme-radar/radar/internal/ingest"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
)

// Source implements ingest.Source over EVM JSON-RPC.
type Source struct {
	rpc *jsonrpc.Client
}

// New returns a source using rpc.
func New(rpc *jsonrpc.Client) *Source { return &Source{rpc: rpc} }

// Head returns the latest block number.
func (s *Source) Head(ctx context.Context) (uint64, error) {
	res, err := s.rpc.Call(ctx, "eth_blockNumber", []any{})
	if err != nil {
		return 0, err
	}
	var q string
	if err := json.Unmarshal(res, &q); err != nil {
		return 0, fmt.Errorf("evmrpc: eth_blockNumber result %.100s: %w", res, err)
	}
	return quantity(q)
}

// codeServerError is geth's generic error code; "header not found" and
// similar "not found" messages mark blocks the node does not have yet.
const codeServerError = -32000

// Block returns block num with its receipts. Receipts that a load-balanced
// endpoint serves from another node or fork count as not available yet.
func (s *Source) Block(ctx context.Context, num uint64) (ingest.Block, error) {
	tag := "0x" + strconv.FormatUint(num, 16)
	block, err := s.rpc.Call(ctx, "eth_getBlockByNumber", []any{tag, true})
	if err != nil {
		return ingest.Block{}, notAvailable(num, err)
	}
	if string(block) == "null" {
		return ingest.Block{}, fmt.Errorf("block %d: %w", num, ingest.ErrNotAvailable)
	}
	var head struct {
		Number, Hash, ParentHash, Timestamp string
		Transactions                        []json.RawMessage
	}
	if err := json.Unmarshal(block, &head); err != nil {
		return ingest.Block{}, fmt.Errorf("evmrpc: block %d: %w", num, err)
	}
	receipts, err := s.rpc.Call(ctx, "eth_getBlockReceipts", []any{tag})
	if err != nil {
		return ingest.Block{}, notAvailable(num, err)
	}
	var rcpts []struct{ BlockHash string }
	if err := json.Unmarshal(receipts, &rcpts); err != nil {
		return ingest.Block{}, fmt.Errorf("evmrpc: receipts %d: %w", num, err)
	}
	if len(rcpts) != len(head.Transactions) {
		return ingest.Block{}, fmt.Errorf("block %d: %d receipts for %d transactions: %w", num, len(rcpts), len(head.Transactions), ingest.ErrNotAvailable)
	}
	for _, r := range rcpts {
		if !strings.EqualFold(r.BlockHash, head.Hash) {
			return ingest.Block{}, fmt.Errorf("block %d: receipt of block %s: %w", num, r.BlockHash, ingest.ErrNotAvailable)
		}
	}
	n, err := quantity(head.Number)
	if err != nil || n != num {
		return ingest.Block{}, fmt.Errorf("evmrpc: block %d: response number %q", num, head.Number)
	}
	ts, err := quantity(head.Timestamp)
	if err != nil {
		return ingest.Block{}, fmt.Errorf("evmrpc: block %d timestamp: %w", num, err)
	}
	hash, err := hash32(head.Hash)
	if err != nil {
		return ingest.Block{}, fmt.Errorf("evmrpc: block %d hash: %w", num, err)
	}
	parent, err := hash32(head.ParentHash)
	if err != nil {
		return ingest.Block{}, fmt.Errorf("evmrpc: block %d parentHash: %w", num, err)
	}
	var payload bytes.Buffer
	payload.Grow(len(block) + len(receipts) + 24)
	payload.WriteString(`{"block":`)
	payload.Write(block)
	payload.WriteString(`,"receipts":`)
	payload.Write(receipts)
	payload.WriteString(`}`)
	return ingest.Block{
		Num: num, Hash: hash, ParentHash: parent, Time: time.Unix(int64(ts), 0),
		TxCount: len(head.Transactions), Payload: payload.Bytes(),
	}, nil
}

func notAvailable(num uint64, err error) error {
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == codeServerError && strings.Contains(rpcErr.Message, "not found") {
		return fmt.Errorf("block %d: %s: %w", num, rpcErr.Message, ingest.ErrNotAvailable)
	}
	return err
}

func quantity(q string) (uint64, error) {
	if !strings.HasPrefix(q, "0x") {
		return 0, fmt.Errorf("evmrpc: quantity %q lacks 0x", q)
	}
	return strconv.ParseUint(q[2:], 16, 64)
}

func hash32(h string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(h, "0x"))
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("evmrpc: hash %q is not 32 hex bytes", h)
	}
	return b, nil
}
