package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
)

type sleepyPGBackend struct {
	*pgapi.Backend
	conn *pgx.Conn
}

func (b sleepyPGBackend) NativePrices(ctx context.Context) (api.NativePrices, error) {
	_, err := b.conn.Exec(ctx, "SELECT pg_sleep(5)")
	return api.NativePrices{}, err
}

// Regression supplement for H2-1's timeout behavior through a real PG query.
func TestPgQueryTimeout(t *testing.T) {
	f := newFixture(t)
	b := sleepyPGBackend{pgapi.New(f.st, f.cfg, func() time.Time { return f.now }), f.conn}
	h := httpapi.New(b, httpapi.Options{})
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/market/natives", nil))
	elapsed := time.Since(start)
	if rec.Code != 503 {
		t.Fatalf("sleeping PG query=%d %s, want 503", rec.Code, rec.Body.String())
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/market/natives", rec)
	var failure api.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "busy" || rec.Header().Get("Retry-After") != "2" || elapsed < 1800*time.Millisecond || elapsed > 3500*time.Millisecond {
		t.Errorf("timeout=%s code=%s Retry-After=%q; want default 2s busy", elapsed, failure.Error.Code, rec.Header().Get("Retry-After"))
	}
}
