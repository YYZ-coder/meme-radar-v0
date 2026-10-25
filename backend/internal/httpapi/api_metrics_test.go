package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/mockdata"
)

func durationCount(t *testing.T, route string) uint64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "radar_api_duration_seconds" {
			for _, m := range f.Metric {
				for _, l := range m.Label {
					if l.GetName() == "route" && l.GetValue() == route {
						return m.GetHistogram().GetSampleCount()
					}
				}
			}
		}
	}
	return 0
}

func TestAPIRequestMetrics(t *testing.T) {
	const okRoute = "GET /healthz"
	const missRoute = "GET /api/v1/tokens/{chain}/{address}"
	ok := metrics.APIRequests.WithLabelValues(okRoute, "200")
	missing := metrics.APIRequests.WithLabelValues(missRoute, "404")
	beforeOK, beforeMissing := testutil.ToFloat64(ok), testutil.ToFloat64(missing)
	beforeOKDuration, beforeMissDuration := durationCount(t, okRoute), durationCount(t, missRoute)
	h := httpapi.New(mockdata.New(nil), httpapi.Options{})
	for _, tc := range []struct {
		target string
		status int
	}{{"/healthz", 200}, {"/api/v1/tokens/solana/11111111111111111111111111111111?currency=usd", 404}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))
		if rec.Code != tc.status {
			t.Fatalf("status=%d, want %d", rec.Code, tc.status)
		}
	}
	if got := testutil.ToFloat64(ok) - beforeOK; got != 1 {
		t.Errorf("200 request increment=%v, want 1", got)
	}
	if got := testutil.ToFloat64(missing) - beforeMissing; got != 1 {
		t.Errorf("404 request increment=%v, want 1", got)
	}
	if got := durationCount(t, okRoute) - beforeOKDuration; got != 1 {
		t.Errorf("200 duration samples=%d, want 1", got)
	}
	if got := durationCount(t, missRoute) - beforeMissDuration; got != 1 {
		t.Errorf("404 duration samples=%d, want 1", got)
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if strings.HasPrefix(f.GetName(), "radar_api_") {
			for _, m := range f.Metric {
				for _, l := range m.Label {
					if l.GetName() == "route" && (strings.Contains(l.GetValue(), "11111111111111111111111111111111") || strings.Contains(l.GetValue(), "?")) {
						t.Errorf("route label exposes raw URL: %s", l.GetValue())
					}
				}
			}
		}
	}
}
