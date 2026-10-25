package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/mockdata"
)

const adminToken = "test-admin"

// clock is a controllable test time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type harness struct {
	t     *testing.T
	h     http.Handler
	clock *clock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	h := httpapi.New(mockdata.New(c.Now), httpapi.Options{AdminToken: adminToken, Now: c.Now})
	return &harness{t: t, h: h, clock: c}
}

type req struct {
	method, target string
	body           io.Reader
	header         map[string]string
	admin          bool
}

func (h *harness) do(r req) *httptest.ResponseRecorder {
	h.t.Helper()
	if r.method == "" {
		r.method = http.MethodGet
	}
	httpReq := httptest.NewRequest(r.method, r.target, r.body)
	for k, v := range r.header {
		httpReq.Header.Set(k, v)
	}
	if r.admin {
		httpReq.Header.Set("Authorization", "Bearer "+adminToken)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, httpReq)
	return rec
}

// get performs a GET, checks the response against the contract route and decodes it.
func (h *harness) get(route, target string, status int, out any, admin ...bool) *httptest.ResponseRecorder {
	h.t.Helper()
	rec := h.do(req{target: target, admin: len(admin) > 0 && admin[0]})
	return h.expect(http.MethodGet, route, rec, status, out)
}

func (h *harness) expect(method, route string, rec *httptest.ResponseRecorder, status int, out any) *httptest.ResponseRecorder {
	h.t.Helper()
	if rec.Code != status {
		h.t.Fatalf("%s %s: status %d, want %d; body %.300s", method, route, rec.Code, status, rec.Body.String())
	}
	contracttest.Check(h.t, method, route, rec)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			h.t.Fatalf("decode %s: %v", route, err)
		}
	}
	return rec
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return strings.NewReader(string(b))
}

type errorBody struct {
	Error struct {
		Code    string  `json:"code"`
		Field   *string `json:"field"`
		TraceID string  `json:"trace_id"`
	} `json:"error"`
}
