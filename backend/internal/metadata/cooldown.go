package metadata

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/meme-radar/radar/internal/metrics"
)

// ErrHostCooling reports that every candidate was temporarily unavailable.
var ErrHostCooling = errors.New("metadata: host cooling down")

type chainContextKey struct{}
type hostState struct {
	failures int
	until    time.Time
}
type hostHealth struct {
	mu     sync.Mutex
	states map[string]hostState
}

func (f *Fetcher) cooling(ctx context.Context, host string) bool {
	host = strings.ToLower(host)
	f.health.mu.Lock()
	state := f.health.states[host]
	cooling := f.options.Now().Before(state.until)
	if !cooling && !state.until.IsZero() {
		delete(f.health.states, host)
	}
	f.health.mu.Unlock()
	if cooling {
		if chain, ok := ctx.Value(chainContextKey{}).(string); ok {
			metrics.MetadataFetches.WithLabelValues(chain, "skipped_host").Inc()
		}
	}
	return cooling
}

func (f *Fetcher) hostResult(host string, failed bool) {
	if host == "" {
		return
	}
	host = strings.ToLower(host)
	f.health.mu.Lock()
	defer f.health.mu.Unlock()
	if !failed {
		delete(f.health.states, host)
		return
	}
	state := f.health.states[host]
	state.failures++
	if state.failures >= f.options.HostFailures {
		state.until = f.options.Now().Add(f.options.HostCooldown)
	}
	f.health.states[host] = state
}
