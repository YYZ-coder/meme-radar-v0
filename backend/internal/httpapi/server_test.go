package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/obs"
)

// stubPinger satisfies httpapi.Pinger without a real database.
type stubPinger struct{ err error }

func (s *stubPinger) PingContext(_ context.Context) error { return s.err }

func TestHealthz_ok(t *testing.T) {
	srv := httpapi.NewServer(":0", &stubPinger{err: nil})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, want := body["status"], "ok"; got != want {
		t.Errorf("body.status = %q, want %q", got, want)
	}
}

func TestHealthz_degraded(t *testing.T) {
	srv := httpapi.NewServer(":0", &stubPinger{err: errors.New("connection refused")})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if got, want := w.Code, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, want := body["status"], "degraded"; got != want {
		t.Errorf("body.status = %q, want %q", got, want)
	}
}

func TestHealthz_methodNotAllowed(t *testing.T) {
	srv := httpapi.NewServer(":0", &stubPinger{})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/healthz", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		if got := w.Code; got == http.StatusOK {
			t.Errorf("%s /healthz returned 200, want non-200", method)
		}
	}
}

func TestTraceIDHeaderIsSetAndPropagated(t *testing.T) {
	srv := httpapi.NewServer(":0", &stubPinger{})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if id := w.Header().Get("X-Trace-Id"); len(id) != 32 {
		t.Fatalf("missing generated trace id: %q", id)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if got := w.Header().Get("X-Trace-Id"); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("incoming traceparent not reused: %q", got)
	}
}

type errorResponse struct {
	Error struct {
		Code    string  `json:"code"`
		Message string  `json:"message"`
		Field   *string `json:"field"`
		TraceID string  `json:"trace_id"`
	} `json:"error"`
}

func TestUnknownRouteIsContractJSON404(t *testing.T) {
	srv := httpapi.NewServer(":0", &stubPinger{})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
	var body errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if body.Error.Code != "not_found" || body.Error.TraceID != w.Header().Get("X-Trace-Id") {
		t.Fatalf("bad error body: %+v", body)
	}
}

func TestPanicInHandlerReturns500AndServerKeepsServing(t *testing.T) {
	srv := httpapi.NewServer(":0", &stubPinger{})
	srv.Handle("GET /api/v1/test/panic", func(http.ResponseWriter, *http.Request) error {
		panic("handler bug")
	})
	srv.Handle("GET /api/v1/test/field", func(http.ResponseWriter, *http.Request) error {
		return obs.Field(obs.CodeInvalidParam, "window", "bad window")
	})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/test/panic", nil))
	var body errorResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusInternalServerError || body.Error.Code != "internal" || body.Error.TraceID == "" {
		t.Fatalf("panic not converted: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "handler bug") {
		t.Fatal("panic detail leaked to client")
	}

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/test/field", nil))
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusBadRequest || body.Error.Field == nil || *body.Error.Field != "window" {
		t.Fatalf("field error not rendered: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatal("server unusable after panic")
	}
}
