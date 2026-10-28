package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"
)

// apiReadPool reserves bounded, read-only connections for health and detail.
// Inflow and other bulk reads retain the ordinary pool. A replacement becomes
// eligible only after its read statements are prepared; no data is preloaded.
func (s *Store) apiReadPool() *pgxpool.Pool {
	if p := s.apiReads.Load(); p != nil {
		return p
	}
	return s.pool
}

func (s *Store) initAPIReadPool(ctx context.Context) error {
	s.apiReadMu.Lock()
	defer s.apiReadMu.Unlock()
	if s.closed {
		return errors.New("store: closed")
	}
	if s.apiReads.Load() != nil {
		return nil
	}
	cfg := s.pool.Config()
	cfg.MaxConns = min(4, cfg.MaxConns)
	cfg.MinConns = cfg.MaxConns
	params := make(map[string]string, len(cfg.ConnConfig.RuntimeParams)+2)
	for k, v := range cfg.ConnConfig.RuntimeParams {
		params[k] = v
	}
	params["default_transaction_read_only"] = "on"
	params["application_name"] = "radar-api-read"
	cfg.ConnConfig.RuntimeParams = params
	// A losing spare often already has a reply in transit. Draining it avoids
	// discarding a warmed session and paying connect+prepare on the next read.
	// Cache callers still stop waiting at their own deadline; cleanup is bounded.
	cfg.ConnConfig.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.DeadlineContextWatcherHandler{Conn: c.Conn(), DeadlineDelay: 150 * time.Millisecond}
	}
	previous := cfg.AfterConnect
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if previous != nil {
			if err := previous(ctx, conn); err != nil {
				return err
			}
		}
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return prepareAPIStatements(bounded, conn, apiDetailHealthStatements())
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("store: API read pool: %w", err)
	}
	if err = holdWarmConnections(ctx, p, int(cfg.MaxConns), nil); err != nil {
		p.Close()
		return err
	}
	s.apiReads.Store(p)
	return nil
}

// holdWarmConnections makes readiness wait for all reserved connections. Each
// acquisition stays held so this loop cannot repeatedly acquire one session.
func holdWarmConnections(ctx context.Context, pool *pgxpool.Pool, n int, prepare func(context.Context, *pgx.Conn) error) error {
	held := make([]*pgxpool.Conn, 0, n)
	defer func() {
		for _, c := range held {
			c.Release()
		}
	}()
	for range n {
		c, err := pool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("store: warm read connection: %w", err)
		}
		held = append(held, c)
	}
	if prepare == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, n)
	for _, c := range held {
		go func() { results <- prepare(ctx, c.Conn()) }()
	}
	var first error
	for range n {
		if err := <-results; err != nil && first == nil {
			first = err
			cancel()
		}
	}
	return first
}
