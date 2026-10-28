package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const observationTimeSQL = "SELECT transaction_timestamp()"

// HealthData contains the administrative report and its database observation time.
type HealthData struct {
	ReadAt   time.Time
	Chains   []ChainHealth
	Parsers  []ParserHealth
	Failures []FailureHealth
}

// ReadHealth returns a fresh report with one bounded spare attempt for a slow
// connection. Each attempt pipelines reads in an implicit transaction and
// closes every result and batch even when cancellation or decoding fails.
func (s *Store) ReadHealth(ctx context.Context, chains []HealthChainConfig, now time.Time) (HealthData, error) {
	return hedgeAPIRead(ctx, s, func(attempt context.Context) (HealthData, error) { return s.readHealth(attempt, chains, now) })
}

func (s *Store) readHealth(ctx context.Context, chains []HealthChainConfig, now time.Time) (HealthData, error) {
	var out HealthData
	names, windows := make([]string, len(chains)), make([]int64, len(chains))
	for i, c := range chains {
		names[i], windows[i] = c.Chain, c.WindowBlocks
	}
	cachedVersion, cachedFailures := s.healthFailures.snapshot()
	var batch pgx.Batch
	batch.Queue(observationTimeSQL)
	batch.Queue(healthChainsSQL, names, windows, now)
	batch.Queue(healthParsersSQL, names, now)
	batch.Queue(healthFailureReadSQL, cachedVersion)
	results := s.apiReadPool().SendBatch(ctx, &batch)
	defer results.Close()
	if err := results.QueryRow().Scan(&out.ReadAt); err != nil {
		return out, fmt.Errorf("store: health observation time: %w", err)
	}
	rows, err := results.Query()
	if err != nil {
		return out, fmt.Errorf("store: health chain batch: %w", err)
	}
	out.Chains, err = collectHealthChains(rows)
	if err != nil {
		return out, err
	}
	rows, err = results.Query()
	if err != nil {
		return out, fmt.Errorf("store: health parser batch: %w", err)
	}
	out.Parsers, err = collectHealthParsers(rows)
	if err != nil {
		return out, err
	}
	rows, err = results.Query()
	if err != nil {
		return out, fmt.Errorf("store: health failure batch: %w", err)
	}
	failures, version, err := collectCurrentFailures(rows, cachedVersion, cachedFailures)
	out.Failures = failures
	if err != nil {
		return out, err
	}
	if err = results.Close(); err != nil {
		return out, fmt.Errorf("store: health batch: %w", err)
	}
	s.healthFailures.publish(version, out.Failures)
	return out, nil
}
