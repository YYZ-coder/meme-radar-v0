package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
)

func TestBackendQueryTimeout(t *testing.T) {
	for _, tt := range []struct {
		name                      string
		timeout, parent, min, max time.Duration
	}{
		{"configured", 10 * time.Millisecond, time.Second, 5 * time.Millisecond, 500 * time.Millisecond},
		{"default_two_seconds", 0, 4 * time.Second, 1500 * time.Millisecond, 3 * time.Second},
		{"earlier_parent", time.Second, 10 * time.Millisecond, 5 * time.Millisecond, 500 * time.Millisecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := stubBackend{detail: func(ctx context.Context) (api.TokenDetail, error) {
				<-ctx.Done()
				return api.TokenDetail{}, ctx.Err()
			}}
			h := httpapi.New(b, httpapi.Options{QueryTimeout: tt.timeout})
			// A longer parent deadline makes a missing server timeout fail boundedly.
			ctx, cancel := context.WithTimeout(context.Background(), tt.parent)
			defer cancel()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x1111111111111111111111111111111111111111", nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			start := time.Now()
			h.ServeHTTP(rec, r)
			took := time.Since(start)
			if took < tt.min || took >= tt.max {
				t.Errorf("request took %s, want [%s, %s)", took, tt.min, tt.max)
			}
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body %s", rec.Code, rec.Body.String())
			}
			contracttest.Check(t, http.MethodGet, "/api/v1/tokens/{chain}/{address}", rec)
			var body api.ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "busy" || rec.Header().Get("Retry-After") != "2" {
				t.Errorf("timeout response = %+v, Retry-After = %q", body, rec.Header().Get("Retry-After"))
			}
		})
	}
}
