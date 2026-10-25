// Package ingest follows a chain block by block (D142): each full block is
// stored raw with its ledger row, cursor and gap changes in one transaction.
package ingest

import (
	"context"
	"errors"
	"time"
)

// Block is a full block as served by a Source.
type Block struct {
	Num        uint64
	Hash       []byte
	ParentHash []byte
	Time       time.Time
	TxCount    int
	// Payload is the uncompressed source response stored in raw_blocks.
	Payload []byte
}

// Source serves full blocks of one chain. Implementations must be safe for
// concurrent use (Config.Prefetch fetches blocks in parallel) and apply
// their own request rate limits.
type Source interface {
	// Head returns the newest block number served at the source's
	// commitment level.
	Head(ctx context.Context) (uint64, error)
	// Block returns block num, ErrSkipped when that Solana slot produced no
	// block, or ErrNotAvailable when the source does not serve it yet.
	Block(ctx context.Context, num uint64) (Block, error)
}

var (
	// ErrSkipped reports a slot without a block.
	ErrSkipped = errors.New("ingest: slot skipped")
	// ErrNotAvailable reports a block the source cannot serve yet.
	ErrNotAvailable = errors.New("ingest: block not available")
)

// retryAfter is implemented by source errors that say when to retry.
type retryAfter interface {
	RetryAfter() time.Duration
}
