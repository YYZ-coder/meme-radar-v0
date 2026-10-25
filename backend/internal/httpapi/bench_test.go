package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/mockdata"
)

// BenchmarkInflowFirstQuery measures a full-list first query (projection, filter,
// sort, snapshot) against the mock store. Skeleton baseline only: it says nothing
// about the PostgreSQL-backed P95 target.
func BenchmarkInflowFirstQuery(b *testing.B) {
	now := func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }
	h := httpapi.New(mockdata.New(now), httpapi.Options{Now: now})
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/smart-money/inflow?window=24h&min_liquidity=1000", nil))
		if rec.Code != http.StatusOK {
			b.Fatal(rec.Code)
		}
	}
}
