package obs

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"
)

var panics atomic.Int64

// PanicCount is the number of panics recovered since start (exported for health/metrics).
func PanicCount() int64 { return panics.Load() }

// Recover must be deferred directly (`defer obs.Recover(ctx, task, &err)`) at every
// goroutine boundary. It logs the panic with stack and trace id and stores it into *errp
// (if non-nil) instead of crashing.
func Recover(ctx context.Context, task string, errp *error) {
	if r := recover(); r != nil {
		err := Recovered(ctx, task, r)
		if errp != nil {
			*errp = err
		}
	}
}

// Recovered logs a value obtained from recover() and returns it as a CodeInternal error.
// Use it when the deferred function must do more than recover (e.g. HTTP middleware).
func Recovered(ctx context.Context, task string, r any) error {
	panics.Add(1)
	slog.ErrorContext(ctx, "panic recovered",
		slog.String(KeyCode, string(RuntimePanicRecovered)),
		slog.String(KeyTask, task),
		slog.String(KeyErr, fmt.Sprint(r)),
		slog.String(KeyStack, string(debug.Stack())),
	)
	return Wrap(CodeInternal, "panic in "+task, fmt.Errorf("%v", r))
}

// run executes fn with its own trace and panic protection.
func run(ctx context.Context, task string, fn func(context.Context) error) (err error) {
	ctx = WithAttrs(NewTrace(ctx), slog.String(KeyTask, task))
	defer Recover(ctx, task, &err)
	return fn(ctx)
}

// Go starts fn in a goroutine with a new trace id (parent = caller's trace) and panic
// recovery. Use it instead of a bare `go` statement (make lint-go enforces this).
// The returned channel yields fn's error (or the recovered panic) and is then closed.
func Go(ctx context.Context, task string, fn func(context.Context) error) <-chan error {
	done := make(chan error, 1)
	go func() { //nolint:radar-go — the only sanctioned go statement
		defer close(done)
		err := run(ctx, task, fn)
		if err != nil && ctx.Err() == nil {
			e := As(err)
			slog.Log(ctx, Catalog[e.Code].Level, "task failed",
				slog.String(KeyTask, task), slog.String(KeyCode, string(e.Code)), slog.String(KeyErr, err.Error()))
		}
		done <- err
	}()
	return done
}

// Loop calls fn every interval until ctx ends. Each iteration gets its own trace id and
// its own recover, so one bad block or job never stops the worker.
func Loop(ctx context.Context, task string, interval time.Duration, fn func(context.Context) error) <-chan error {
	return Go(ctx, task, func(ctx context.Context) error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := run(ctx, task, fn); err != nil && ctx.Err() == nil {
				e := As(err)
				slog.Log(ctx, Catalog[e.Code].Level, "iteration failed",
					slog.String(KeyCode, string(e.Code)), slog.String(KeyErr, err.Error()))
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	})
}

// Supervise runs fn and restarts it with exponential backoff (1s → 1min) when it
// returns an error or panics, until ctx ends. Use for long-lived workers.
func Supervise(ctx context.Context, task string, fn func(context.Context) error) <-chan error {
	return superviseWith(ctx, task, time.Second, time.Minute, fn)
}

func superviseWith(ctx context.Context, task string, minWait, maxWait time.Duration, fn func(context.Context) error) <-chan error {
	return Go(ctx, task+".supervisor", func(sctx context.Context) error {
		wait := minWait
		for attempt := 1; ; attempt++ {
			err := run(sctx, task, fn)
			if sctx.Err() != nil {
				return nil
			}
			slog.WarnContext(sctx, "worker stopped; restarting",
				slog.String(KeyCode, string(RuntimeGoroutineRestarted)),
				slog.String(KeyTask, task), slog.Int("attempt", attempt),
				slog.Any(KeyErr, err), slog.Duration("backoff", wait))
			select {
			case <-sctx.Done():
				return nil
			case <-time.After(wait):
			}
			if wait *= 2; wait > maxWait {
				wait = maxWait
			}
		}
	})
}
