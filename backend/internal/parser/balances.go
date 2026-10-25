package parser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"log/slog"

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/balances"
	"github.com/meme-radar/radar/internal/solana"
	"github.com/meme-radar/radar/internal/store"
)

// BalanceConfig configures the independent balance pass in a parse process.
type BalanceConfig struct {
	Chain, Family  string
	Store          *store.Store
	Version, Batch int
	PollInterval   time.Duration
	Log            *slog.Logger
}

// Balances tracks only balance progress, under the process's parse:<chain> lock.
// Step is called by one goroutine per chain.
type Balances struct {
	cfg       BalanceConfig
	tracked   map[string]struct{}
	firstSeen *time.Time
	updatedAt *time.Time
}

// NewBalances returns a balance pass; Version defaults to balances.Version.
func NewBalances(cfg BalanceConfig) *Balances {
	if cfg.Version == 0 {
		cfg.Version = balances.Version
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	return &Balances{cfg: cfg, tracked: map[string]struct{}{}}
}

// Step decodes a balance batch, yielding when the main parser is backlogged.
// Unparsed main blocks are ineligible so their new tokens cannot be missed.
func (p *Balances) Step(ctx context.Context) (time.Duration, error) {
	blocks, err := p.cfg.Store.UnbalancedBlocks(ctx, p.cfg.Chain, p.cfg.Version, max(1, p.cfg.Batch))
	if err != nil {
		return 0, err
	}
	if len(blocks) == 0 {
		return p.cfg.PollInterval, nil
	}
	nums := make([]int64, len(blocks))
	for i, b := range blocks {
		nums[i] = int64(b.Num)
	}
	tokens, err := p.cfg.Store.BalanceTokens(ctx, p.cfg.Chain, p.firstSeen, p.updatedAt, nums)
	if err != nil {
		return 0, err
	}
	for _, t := range tokens {
		p.tracked[string(t.Address)] = struct{}{}
		if p.updatedAt == nil || t.UpdatedAt.After(*p.updatedAt) {
			at := t.UpdatedAt
			p.updatedAt = &at
		}
		if p.firstSeen == nil || t.FirstSeen.After(*p.firstSeen) {
			at := t.FirstSeen
			p.firstSeen = &at
		}
	}
	var results []store.BalanceBlock
	var failures []error
	for _, b := range blocks {
		deltas, err := decodeBalances(b.Payload, p.cfg.Family, p.tracked)
		if err != nil {
			failures = append(failures, fmt.Errorf("balances %s block %d: %w", p.cfg.Chain, b.Num, err))
			continue
		}
		results = append(results, store.BalanceBlock{Chain: p.cfg.Chain, Num: b.Num, Time: b.Time, Hash: b.Hash, Version: p.cfg.Version, Deltas: deltas})
	}
	if err := p.cfg.Store.ApplyBalances(ctx, results); err != nil {
		return 0, err
	}
	metrics.BalanceBlocksApplied.WithLabelValues(p.cfg.Chain).Add(float64(len(results)))
	if len(failures) > 0 {
		return 0, errors.Join(failures...)
	}
	if len(blocks) < max(1, p.cfg.Batch) {
		return p.cfg.PollInterval, nil
	}
	return 0, nil
}

func decodeBalances(payload []byte, family string, tracked map[string]struct{}) ([]parse.Balance, error) {
	var out []parse.Balance
	switch family {
	case "solana":
		block, err := solana.ParseBlock(payload)
		if err != nil {
			return nil, err
		}
		for _, tx := range block.Txs {
			out = append(out, balances.Solana(tx, tracked)...)
		}
	case "evm":
		block, err := evm.ParseBlock(payload)
		if err != nil {
			return nil, err
		}
		for _, tx := range block.Txs {
			out = append(out, balances.EVM(tx, tracked)...)
		}
	default:
		return nil, fmt.Errorf("unsupported balance family %q", family)
	}
	return out, nil
}

// Run steps until cancellation, retrying transient errors with bounded backoff.
// The caller holds the same parse:<chain> session lock as the main parser.
func (p *Balances) Run(ctx context.Context) error {
	log := p.cfg.Log
	if log == nil {
		log = slog.Default()
	}
	var backoff time.Duration
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		wait, err := p.Step(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			backoff = min(max(2*backoff, time.Second), 30*time.Second)
			wait = backoff
			metrics.StepErrors.WithLabelValues("balances", p.cfg.Chain).Inc()
			log.Warn("balance step failed", "chain", p.cfg.Chain, "role", "parse", "retry_in", wait.String(), "err", err)
		} else {
			backoff = 0
		}
		timer.Reset(wait)
	}
}
