package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/httpapi"
)

func TestAPIErrorLogsRoleAndChain(t *testing.T) {
	var logs bytes.Buffer
	b := stubBackend{detail: func(context.Context) (api.TokenDetail, error) { return api.TokenDetail{}, errors.New("query failed") }}
	h := httpapi.New(b, httpapi.Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	const trace = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x1111111111111111111111111111111111111111", nil)
	req.Header.Set("X-Trace-Id", trace)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status=%d, want 500", rec.Code)
	}
	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["role"] != "api" || record["chain"] != "bsc" || record["trace_id"] != trace || record["level"] != "ERROR" {
		t.Errorf("API error log=%v", record)
	}
}
