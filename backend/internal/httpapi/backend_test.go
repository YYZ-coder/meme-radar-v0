package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/mockdata"
)

// The nil embedded mock panics if a handler calls an unexpected method.
type stubBackend struct {
	*mockdata.Data
	detail func(context.Context) (api.TokenDetail, error)
}

func (b stubBackend) TokenDetail(ctx context.Context, _ api.Chain, _ string, _ bool) (api.TokenDetail, error) {
	return b.detail(ctx)
}

// H2 §2-1 defines these outcomes independently of the Backend implementation.
func TestBackendErrorsMapToResponses(t *testing.T) {
	for _, tt := range []struct {
		name, code, message string
		err                 error
		status              int
	}{
		{"deadline", "busy", "", context.DeadlineExceeded, http.StatusServiceUnavailable},
		{"wrapped_deadline", "busy", "", fmt.Errorf("query: %w", context.DeadlineExceeded), http.StatusServiceUnavailable},
		{"internal", "internal", "internal error", errors.New("boom"), http.StatusInternalServerError},
		{"api_error", "not_found", "token missing", api.Errorf(404, "not_found", "address", "token missing"), http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			b := stubBackend{detail: func(context.Context) (api.TokenDetail, error) { return api.TokenDetail{}, tt.err }}
			h := httpapi.New(b, httpapi.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
			r := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x1111111111111111111111111111111111111111", nil)
			const trace = "4bf92f3577b34da6a3ce929d0e0e4736"
			r.Header.Set("X-Trace-Id", trace)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.status, rec.Body.String())
			}
			contracttest.Check(t, http.MethodGet, "/api/v1/tokens/{chain}/{address}", rec)
			var body api.ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tt.code || body.Error.TraceID != trace {
				t.Errorf("error = %+v, want %s with incoming trace", body.Error, tt.code)
			}
			if tt.message != "" && body.Error.Message != tt.message {
				t.Errorf("message = %q, want %q", body.Error.Message, tt.message)
			}
			if tt.name == "api_error" && (body.Error.Field == nil || *body.Error.Field != "address") {
				t.Errorf("field = %v, want address", body.Error.Field)
			}
			if tt.status == 503 && rec.Header().Get("Retry-After") != "2" {
				t.Errorf("Retry-After = %q, want 2", rec.Header().Get("Retry-After"))
			}
			if tt.status == 500 && (!strings.Contains(logs.String(), trace) || !strings.Contains(logs.String(), "boom") || !strings.Contains(logs.String(), `"level":"ERROR"`)) {
				t.Errorf("internal error log lacks trace/error: %s", logs.String())
			}
		})
	}
}
