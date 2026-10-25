// Package pgapi maps PostgreSQL query results to the API wire types.
package pgapi

import (
	"net/http"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/store"
)

// Backend serves the PostgreSQL API. It is safe for concurrent use.
// The caller owns the store and closes it after serving requests stops.
type Backend struct {
	st     *store.Store
	cfg    config.Config
	now    func() time.Time
	native *nativeBalances
}

var _ httpapi.Backend = (*Backend)(nil)

// New returns a Backend using st, cfg and the supplied clock (default time.Now).
func New(st *store.Store, cfg config.Config, now func() time.Time) *Backend {
	if now == nil {
		now = time.Now
	}
	return &Backend{st: st, cfg: cfg, now: now, native: newNativeBalances(cfg)}
}

func unavailable() *api.Error {
	return api.Errorf(http.StatusServiceUnavailable, "api_unavailable", "", "data source is not ready")
}
