// Package parser is the parse role: it decodes stored raw blocks, newest
// first, and writes their facts with store.ApplyParsed. Progress is
// chain_blocks.parsed_version, so restarts resume and blocks that ingest
// adds later (backfill) are picked up too.
package parser

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/pump"
	"github.com/meme-radar/radar/internal/solana"
	"github.com/meme-radar/radar/internal/store"
)

// Decoded is the decode result of one block payload.
type Decoded struct {
	Facts  parse.Facts
	HitTxs int // transactions with at least one fact or failure
	Events int // trades plus liquidity events
}

// BlockDecoder decodes a raw block payload of one chain; errors wrapping
// parse.ErrRetry leave the block unparsed for a later step.
type BlockDecoder func(ctx context.Context, payload []byte) (Decoded, error)

// Config configures a Parser.
type Config struct {
	Chain   string
	Store   *store.Store
	Decode  BlockDecoder
	Version int // bump to reparse every block
	// Decoders lists the enabled module names persisted at startup.
	Decoders []string
	// Batch is how many blocks one Step parses; they are written in one
	// transaction.
	Batch int
	// Concurrency is how many blocks are decoded at once (lookups overlap).
	Concurrency  int
	PollInterval time.Duration
	Log          *slog.Logger
}

// Parser parses one chain; run one per chain.
type Parser struct {
	cfg Config
}

// New returns a parser for cfg.
func New(cfg Config) *Parser { return &Parser{cfg: cfg} }

// Step parses up to Batch unparsed blocks and returns how long to wait
// before the next step. A block whose payload cannot be decoded is stored
// with a block-level failure and still marked parsed.
func (p *Parser) Step(ctx context.Context) (time.Duration, error) {
	retried, err := p.cfg.Store.ExecuteParseRetries(ctx, p.cfg.Chain, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	if len(retried) > 0 {
		metrics.ParseRetryBlocks.WithLabelValues(p.cfg.Chain).Add(float64(len(retried)))
		p.log().Info("parse retry blocks reset", "role", "parse", "chain", p.cfg.Chain, "blocks", retried)
	}
	blocks, err := p.cfg.Store.UnparsedBlocks(ctx, p.cfg.Chain, max(1, p.cfg.Batch))
	if err != nil {
		return 0, err
	}
	type result struct {
		d   Decoded
		err error
	}
	results := make([]result, len(blocks))
	sem := make(chan struct{}, max(1, p.cfg.Concurrency))
	var wg sync.WaitGroup
	for i, b := range blocks {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			results[i].d, results[i].err = p.decode(ctx, b.Num)
		}()
	}
	wg.Wait()
	// Write every decoded block in one transaction; a block whose decode
	// must be retried stays unparsed and its error is returned.
	var parsed []store.Parsed
	var retry error
	for i, b := range blocks {
		if r := results[i]; r.err != nil {
			retry = cmp.Or(retry, r.err)
			continue
		}
		d := results[i].d
		parsed = append(parsed, store.Parsed{Chain: p.cfg.Chain, Num: b.Num, Time: b.Time,
			Version: p.cfg.Version, Facts: d.Facts, HitTxs: d.HitTxs, Events: d.Events})
	}
	if err := p.cfg.Store.ApplyParsedMany(ctx, parsed); err != nil {
		return 0, err
	}
	failures := 0
	for _, b := range parsed {
		failures += len(b.Facts.Failures)
	}
	metrics.BlocksParsed.WithLabelValues(p.cfg.Chain).Add(float64(len(parsed)))
	metrics.DecodeFailures.WithLabelValues(p.cfg.Chain).Add(float64(failures))
	if retry != nil {
		return 0, retry
	}
	if len(blocks) < p.cfg.Batch {
		return p.cfg.PollInterval, nil
	}
	return 0, nil
}

func (p *Parser) decode(ctx context.Context, num uint64) (Decoded, error) {
	payload, err := p.cfg.Store.RawBlock(ctx, p.cfg.Chain, num)
	if errors.Is(err, store.ErrNotFound) {
		return blockFailure("raw", errors.New("raw block missing (pruned or never stored)")), nil
	}
	if err != nil {
		return Decoded{}, err
	}
	d, err := p.cfg.Decode(ctx, payload)
	if errors.Is(err, parse.ErrRetry) {
		return Decoded{}, err
	}
	if err != nil {
		return blockFailure("block", err), nil
	}
	return d, nil
}

func blockFailure(decoder string, err error) Decoded {
	return Decoded{Facts: parse.Facts{Failures: []parse.Failure{{Tx: -1, Decoder: decoder, Err: err.Error()}}}}
}

// Run marks blocks of older parser versions unparsed, then steps until ctx
// is cancelled, backing off from 1s to 30s on errors. It returns nil on
// cancellation.
func (p *Parser) Run(ctx context.Context) error {
	if err := p.cfg.Store.SaveParserStatus(ctx, p.cfg.Chain, p.cfg.Version, p.cfg.Decoders, time.Now().UTC()); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	n, err := p.cfg.Store.ResetParsed(ctx, p.cfg.Chain, p.cfg.Version)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	p.log().Info("parse started", "chain", p.cfg.Chain, "version", p.cfg.Version, "reset_blocks", n)
	var backoff time.Duration
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		wait, err := p.Step(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			backoff = min(max(2*backoff, time.Second), 30*time.Second)
			wait = backoff
			metrics.StepErrors.WithLabelValues("parse", p.cfg.Chain).Inc()
			p.log().Warn("parse step failed", "chain", p.cfg.Chain, "retry_in", wait.String(), "err", err)
		} else {
			backoff = 0
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (p *Parser) log() *slog.Logger {
	if p.cfg.Log != nil {
		return p.cfg.Log
	}
	return slog.Default()
}

// Solana decodes a stored Solana getBlock payload with the Pump.fun and
// PumpSwap decoders.
func Solana(_ context.Context, payload []byte) (Decoded, error) {
	blk, err := solana.ParseBlock(payload)
	if err != nil {
		return Decoded{}, err
	}
	var d Decoded
	for _, tx := range blk.Txs {
		f := pump.Decode(tx)
		if len(f.Tokens)+len(f.Pools)+len(f.Trades)+len(f.Liquidity)+len(f.Failures) == 0 {
			continue
		}
		d.HitTxs++
		d.Events += len(f.Trades) + len(f.Liquidity)
		d.Facts.Add(f)
	}
	return d, nil
}

// TxDecoder decodes one EVM transaction.
type TxDecoder interface {
	Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error)
}

// EVM returns a block decoder running decoders over every transaction.
func EVM(decoders ...TxDecoder) BlockDecoder {
	return func(ctx context.Context, payload []byte) (Decoded, error) {
		blk, err := evm.ParseBlock(payload)
		if err != nil {
			return Decoded{}, err
		}
		var d Decoded
		for _, tx := range blk.Txs {
			var f parse.Facts
			for _, dec := range decoders {
				g, err := dec.Decode(ctx, tx)
				if err != nil {
					return Decoded{}, err
				}
				f.Add(g)
			}
			if len(f.Tokens)+len(f.Pools)+len(f.Trades)+len(f.Liquidity)+len(f.Failures) == 0 {
				continue
			}
			d.HitTxs++
			d.Events += len(f.Trades) + len(f.Liquidity)
			d.Facts.Add(f)
		}
		return d, nil
	}
}
