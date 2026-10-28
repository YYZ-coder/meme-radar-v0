package pgapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
)

// BenchmarkHotspotHTTP uses the same real PostgreSQL datasets as the public
// performance tests. Setup is outside the measured interval. Failed responses
// remain visible instead of being discarded from the latency measurements.
func BenchmarkHotspotHTTP(b *testing.B) {
	for _, tc := range []struct {
		name, target string
		admin        bool
		seed         func(testing.TB, *fixture)
	}{
		{"admin_health", "/api/v1/admin/health", true, seedLargeHealthLedger},
		{"token_detail", "/api/v1/tokens/bsc/0x00000000000000000000000000000000000f4240", false, seedLargeSameNamePopulation},
	} {
		b.Run(tc.name, func(b *testing.B) {
			f := newFixture(b)
			tc.seed(b, f)
			h := f.handler()
			var failed int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodGet, tc.target, nil)
				if tc.admin {
					req.Header.Set("Authorization", "Bearer test-admin")
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					failed++
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(failed), "failed_requests")
		})
	}
}

// BenchmarkCachedHotspotHTTP measures the deployed warm-read path on the same
// real PostgreSQL fixtures. Preparation and the first observation are excluded
// only from this microbenchmark; live acceptance retains first requests.
func BenchmarkCachedHotspotHTTP(b *testing.B) {
	for _, tc := range []struct {
		name, target string
		admin        bool
		seed         func(testing.TB, *fixture)
	}{
		{"admin_health", "/api/v1/admin/health", true, seedLargeHealthLedger},
		{"token_detail", "/api/v1/tokens/bsc/0x00000000000000000000000000000000000f4240", false, seedLargeSameNamePopulation},
	} {
		b.Run(tc.name, func(b *testing.B) {
			f := newFixture(b)
			tc.seed(b, f)
			cached, err := pgapi.NewCached(pgapi.New(f.st, f.cfg, func() time.Time { return f.now }), time.Second)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(cached.Close)
			h := httpapi.New(cached, httpapi.Options{Now: func() time.Time { return f.now }, AdminToken: "test-admin"})
			request := func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, tc.target, nil)
				if tc.admin {
					r.Header.Set("Authorization", "Bearer test-admin")
				}
				return r
			}
			warm := httptest.NewRecorder()
			h.ServeHTTP(warm, request())
			if warm.Code != 200 {
				b.Fatalf("warmup=%d: %s", warm.Code, warm.Body.String())
			}
			var failed int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, request())
				if rec.Code != 200 {
					failed++
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(failed), "failed_requests")
		})
	}
}
