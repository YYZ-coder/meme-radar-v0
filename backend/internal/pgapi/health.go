package pgapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/store"
)

// CheckHealth checks PostgreSQL readiness without modifying data.
func (b *Backend) CheckHealth(ctx context.Context) error {
	return b.st.Ping(ctx)
}

// Health reports ingest progress and parser quality from PostgreSQL.
func (b *Backend) Health(ctx context.Context) (api.AdminHealth, error) {
	now := b.now().UTC()
	chains := make([]store.HealthChainConfig, 0, len(b.cfg.Chains))
	for _, c := range b.cfg.Chains {
		var blocks int64
		if c.Backfill.Hours > 0 && c.Ingest.BlockInterval > 0 {
			blocks = int64(c.Backfill.Hours * float64(time.Hour) / float64(c.Ingest.BlockInterval))
		}
		chains = append(chains, store.HealthChainConfig{Chain: c.Name, WindowBlocks: blocks})
	}
	rows, err := b.st.HealthChains(ctx, chains, now)
	if err != nil {
		return api.AdminHealth{}, err
	}
	out := api.AdminHealth{AsOf: now.Format("2006-01-02T15:04:05.000Z"), Chains: []api.ChainHealth{}, Parsers: []api.ParserHealth{}, Failures: []api.ParseFailure{},
		Budget: api.Budget{Month: now.Format("2006-01"), SpentUSD: healthSpentUSD, LimitUSD: healthLimitUSD, State: "normal"}}
	for _, c := range rows {
		item := api.ChainHealth{Chain: api.Chain(c.Chain), Head: c.Head, Anchor: c.Anchor, LowerBound: c.LowerBound, ForwardPosition: c.ForwardPosition, HistoryPosition: c.HistoryPosition, BacklogBlocks: c.BacklogBlocks, LagSeconds: c.LagSeconds, GapsOpen: c.GapsOpen, GapsByReason: c.GapsByReason}
		if c.ErrorKind != nil && c.ErrorMessage != nil && c.ErrorAt != nil {
			kind := *c.ErrorKind
			if authStatus.MatchString(*c.ErrorMessage) {
				kind = "config"
			} else {
				switch kind {
				case "config", "not_found", "history_unavailable", "incomplete":
				default:
					kind = "retryable"
				}
			}
			item.LastError = &api.LastError{Kind: kind, Message: *c.ErrorMessage, At: c.ErrorAt.UTC().Format("2006-01-02T15:04:05.000Z")}
		}
		out.Chains = append(out.Chains, item)
	}
	names := make([]string, 0, len(chains))
	for _, c := range chains {
		names = append(names, c.Chain)
	}
	parsers, err := b.st.HealthParsers(ctx, names, now)
	if err != nil {
		return api.AdminHealth{}, err
	}
	for _, p := range parsers {
		lag := p.LagBlocks
		out.Parsers = append(out.Parsers, api.ParserHealth{Chain: api.Chain(p.Chain), Module: p.Module, Version: strconv.Itoa(p.Version), Position: p.Position, LagBlocks: &lag, Failures: p.Failures, Quarantined: p.Quarantined})
	}
	failures, err := b.st.HealthFailures(ctx)
	if err != nil {
		return api.AdminHealth{}, err
	}
	for _, f := range failures {
		out.Failures = append(out.Failures, failureResponse(f))
	}
	return out, nil
}

var authStatus = regexp.MustCompile(`\b(?:401|403)\b`)

func failureResponse(f store.FailureHealth) api.ParseFailure {
	status := "failed"
	if f.Queued {
		status = "queued"
	}
	return api.ParseFailure{ID: strconv.FormatInt(f.ID, 10), Chain: api.Chain(f.Chain), Module: f.Decoder, Position: fmt.Sprintf("%d/%d", f.BlockNum, f.TxIndex), Error: f.Error, Attempts: f.Attempts, Status: status, LastAttemptAt: f.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z")}
}

// RetryFailure queues a failure for execution by the parse role.
func (b *Backend) RetryFailure(ctx context.Context, id string) (api.ParseFailure, error) {
	missing := api.Errorf(http.StatusNotFound, "not_found", "", "failure not found")
	num, err := strconv.ParseInt(id, 10, 64)
	if err != nil || num < 1 {
		return api.ParseFailure{}, missing
	}
	f, err := b.st.QueueParseRetry(ctx, num, b.now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return api.ParseFailure{}, missing
	}
	if err != nil {
		return api.ParseFailure{}, err
	}
	return failureResponse(f), nil
}

// Budget is a placeholder only. D104's historical limit is retained; D14's
// actual paid-source allowance is pending. No budget configuration or control.
const (
	healthSpentUSD = "0"
	healthLimitUSD = "100"
)
