// Package enrich fills token facts that parse could not see (line A, D150):
// the total supply and decimals of traded tokens whose creation event is
// outside the stored blocks, read from the chain into token_supplements.
package enrich

import (
	"context"
	"errors"
	"github.com/meme-radar/radar/internal/metrics"
	"time"

	"github.com/meme-radar/radar/internal/store"
)

// Supply reads a token's current total supply (atomic units) and decimals.
type Supply interface {
	Supply(ctx context.Context, token []byte) (string, int16, error)
}

// batch bounds the lookups of one step.
const batch = 200

// Enricher fills one chain's token_supplements.
type Enricher struct {
	store *store.Store
	chain string
	sup   Supply
}

func New(s *store.Store, chain string, sup Supply) *Enricher {
	return &Enricher{store: s, chain: chain, sup: sup}
}

// Step looks up the tokens due at now. A failed lookup is recorded and
// retried after an hour; only a store error stops the step.
func (e *Enricher) Step(ctx context.Context, now time.Time) error {
	tokens, err := e.store.TokensToEnrich(ctx, e.chain, now, batch)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tokens {
		supply, dec, err := e.sup.Supply(ctx, t)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			metrics.EnrichLookups.WithLabelValues(e.chain, "error").Inc()
			errs = append(errs, e.store.SaveSupplement(ctx, e.chain, t, nil, nil, err.Error(), now))
			continue
		}
		metrics.EnrichLookups.WithLabelValues(e.chain, "ok").Inc()
		errs = append(errs, e.store.SaveSupplement(ctx, e.chain, t, &supply, &dec, "", now))
	}
	return errors.Join(errs...)
}
