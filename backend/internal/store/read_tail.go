package store

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type readHedgeLimiter struct {
	once  sync.Once
	slots chan struct{}
}

// hedgeRead permits one additional live read after a slow start. The first
// successful result wins; both attempts retain caller cancellation. Spare
// attempts wait half the 200ms target budget, so ordinary RTT does not churn
// warmed sessions. They are bounded per Store and disabled for single-connection pools.
// Callers must supply only read-only, independently repeatable operations.
func hedgeRead[T any](ctx context.Context, s *Store, read func(context.Context) (T, error)) (T, error) {
	return hedgeReadOn(ctx, s.pool, &s.readHedges, 100*time.Millisecond, true, read)
}

// Reserved reads start a spare sooner, but only against existing idle capacity.
func hedgeAPIRead[T any](ctx context.Context, s *Store, read func(context.Context) (T, error)) (T, error) {
	p := s.apiReadPool()
	if p == s.pool {
		return hedgeRead(ctx, s, read)
	}
	return hedgeReadOn(ctx, p, &s.apiHedges, 50*time.Millisecond, false, read)
}

func hedgeReadOn[T any](ctx context.Context, pool *pgxpool.Pool, limit *readHedgeLimiter, delay time.Duration, allowDial bool, read func(context.Context) (T, error)) (T, error) {
	if pool.Config().MaxConns < 2 {
		return read(ctx)
	}
	type result struct {
		value T
		err   error
	}
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, 2)
	run := func() { v, err := read(attemptCtx); results <- result{v, err} }
	go run()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	attempts := 1
	select {
	case r := <-results:
		return r.value, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-timer.C:
		limit.once.Do(func() { limit.slots = make(chan struct{}, 4) })
		// Skip a spare when reserved capacity is busy. A late connection
		// setup would add work while the primary is already waiting.
		if !allowDial && pool.Stat().IdleConns() == 0 {
			break
		}
		select {
		case limit.slots <- struct{}{}:
			attempts++
			go func() { defer func() { <-limit.slots }(); run() }()
		default:
		}
	}
	var first result
	for i := 0; i < attempts; i++ {
		select {
		case r := <-results:
			if r.err == nil {
				return r.value, nil
			}
			if i == 0 {
				first = r
			}
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
	return first.value, first.err
}
