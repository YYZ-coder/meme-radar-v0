package pgapi

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/httpapi"
)

// CachedBackend bounds successful health/detail observations to maxAge.
// Existing New callers retain uncached behavior; authorization remains in HTTP.
type CachedBackend struct {
	*Backend
	maxAge    time.Duration
	health    *freshCache[struct{}, api.AdminHealth]
	details   *freshCache[detailCacheKey, api.TokenDetail]
	slots     chan struct{}
	closeOnce sync.Once
}

type detailCacheKey struct {
	chain   api.Chain
	address string
	native  bool
}

// NewCached creates an opt-in backend with at most one second of data age.
// Recent keys refresh before expiration; errors never extend an old snapshot.
func NewCached(b *Backend, maxAge time.Duration) (*CachedBackend, error) {
	if maxAge <= 0 || maxAge > time.Second {
		return nil, fmt.Errorf("read cache age must be in (0,1s]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := b.st.WarmAPIReads(ctx)
	cancel()
	if err != nil {
		return nil, err
	}
	cached := &CachedBackend{Backend: b, maxAge: maxAge, slots: make(chan struct{}, 4)}
	cached.health = newFreshCache(maxAge, 1, cached.slots, func(ctx context.Context, _ struct{}) (api.AdminHealth, time.Time, error) {
		return b.readHealthObserved(ctx)
	})
	cached.details = newFreshCache(maxAge, 256, cached.slots, func(ctx context.Context, key detailCacheKey) (api.TokenDetail, time.Time, error) {
		return b.readTokenDetailObserved(ctx, key.chain, key.address, key.native)
	})
	return cached, nil
}

// Health returns an exact observation with its original data time. It never
// waits for a background refresh while a permitted-age observation is available.
func (b *CachedBackend) Health(ctx context.Context) (api.AdminHealth, error) {
	out, at, err := b.health.get(ctx, struct{}{})
	if err == nil {
		httpapi.RecordDataRead(ctx, at, b.maxAge)
	}
	return out, err
}

// TokenDetail keeps chain, address and currency observations separate.
func (b *CachedBackend) TokenDetail(ctx context.Context, chain api.Chain, address string, native bool) (api.TokenDetail, error) {
	if chain != api.Solana {
		address = strings.ToLower(address)
	}
	out, at, err := b.details.get(ctx, detailCacheKey{chain, address, native})
	if err == nil {
		httpapi.RecordDataRead(ctx, at, b.maxAge)
	}
	return out, err
}

// Close stops background reads before the caller closes the underlying store.
func (b *CachedBackend) Close() { b.closeOnce.Do(func() { b.health.close(); b.details.close() }) }

// PrimeHealth initializes the singleton report before the listener is ready.
func (b *CachedBackend) PrimeHealth(ctx context.Context) error { return b.health.pin(ctx, struct{}{}) }
