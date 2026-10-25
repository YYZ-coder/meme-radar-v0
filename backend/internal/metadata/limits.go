package metadata

import (
	"context"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

type budgets struct {
	global, four *rate.Limiter
	perHost      float64
	mu           sync.Mutex
	hosts        map[string]*rate.Limiter
}

func newBudgets(options Options) *budgets {
	makeLimit := func(rps float64) *rate.Limiter {
		if rps <= 0 {
			return nil
		}
		return rate.NewLimiter(rate.Limit(rps), 1)
	}
	return &budgets{global: makeLimit(options.RPS), four: makeLimit(options.FourMemeRPS), perHost: options.PerHostRPS, hosts: map[string]*rate.Limiter{}}
}

func (b *budgets) wait(ctx context.Context, host string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.global != nil {
		if err := b.global.Wait(ctx); err != nil {
			return err
		}
	}
	if b.perHost <= 0 {
		return nil
	}
	host = strings.ToLower(host)
	b.mu.Lock()
	limit := b.hosts[host]
	if limit == nil {
		// A fully replenished burst-1 budget can be dropped without changing
		// behavior. Prune idle hosts to bound long-lived enrich memory.
		if len(b.hosts) >= 1024 {
			for key, old := range b.hosts {
				if old.Tokens() >= 1 {
					delete(b.hosts, key)
				}
			}
		}
		limit = rate.NewLimiter(rate.Limit(b.perHost), 1)
		b.hosts[host] = limit
	}
	b.mu.Unlock()
	return limit.Wait(ctx)
}

func (b *budgets) waitFour(ctx context.Context) error {
	if b.four == nil {
		return ctx.Err()
	}
	return b.four.Wait(ctx)
}
