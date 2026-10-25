package httpapi_test

import (
	"testing"

	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/mockdata"
)

// D110: responses served from demonstration data carry X-Radar-Mock: 1 (errors too),
// so the frontend can label the page; real data never carries it.
func TestDemoDataHeader(t *testing.T) {
	c := &clock{}
	demo := &harness{t: t, clock: c, h: httpapi.New(mockdata.New(c.Now), httpapi.Options{DemoData: true, Now: c.Now})}
	for _, target := range []string{"/api/v1/market/natives", "/api/v1/nope", "/api/v1/admin/health"} {
		if got := demo.do(req{target: target}).Header().Get("X-Radar-Mock"); got != "1" {
			t.Errorf("demo %s: X-Radar-Mock = %q, want 1", target, got)
		}
	}
	real := newHarness(t)
	if got := real.do(req{target: "/api/v1/market/natives"}).Header().Get("X-Radar-Mock"); got != "" {
		t.Errorf("non-demo X-Radar-Mock = %q, want absent", got)
	}
}
