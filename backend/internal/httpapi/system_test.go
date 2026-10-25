package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Contract: /healthz returns HealthResponse; every response carries X-Trace-Id and
// errors use ErrorResponse with error.trace_id equal to the header (openapi info rules).
func TestHealthz(t *testing.T) {
	h := newHarness(t)
	var body struct{ Status string }
	h.get("/healthz", "/healthz", http.StatusOK, &body)
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

func TestUnknownRouteIsJSONNotFound(t *testing.T) {
	h := newHarness(t)
	rec := h.do(req{target: "/api/v1/nope"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	var e errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if e.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", e.Error.Code)
	}
	if got := rec.Header().Get("X-Trace-Id"); got == "" || got != e.Error.TraceID {
		t.Errorf("X-Trace-Id %q must equal error.trace_id %q", got, e.Error.TraceID)
	}
}

func TestReusesIncomingTraceID(t *testing.T) {
	h := newHarness(t)
	const id = "4bf92f3577b34da6a3ce929d0e0e4736"
	rec := h.do(req{target: "/healthz", header: map[string]string{"X-Trace-Id": id}})
	if got := rec.Header().Get("X-Trace-Id"); got != id {
		t.Errorf("X-Trace-Id = %q, want reused %q", got, id)
	}
	rec = h.do(req{target: "/healthz", header: map[string]string{"traceparent": "00-" + id + "-00f067aa0ba902b7-01"}})
	if got := rec.Header().Get("X-Trace-Id"); got != id {
		t.Errorf("traceparent: X-Trace-Id = %q, want %q", got, id)
	}
	rec = h.do(req{target: "/healthz", header: map[string]string{"X-Trace-Id": "00000000000000000000000000000000"}})
	if got := rec.Header().Get("X-Trace-Id"); got == "00000000000000000000000000000000" || len(got) != 32 {
		t.Errorf("all-zero trace id must be replaced, got %q", got)
	}
}
