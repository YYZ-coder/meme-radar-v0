package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/mockdata"
)

const healthRoute = "/api/v1/admin/health"

// D106: admin endpoints need Authorization: Bearer <RADAR_ADMIN_TOKEN>; missing,
// wrong or unconfigured tokens get 403 forbidden.
func TestAdminAuth(t *testing.T) {
	h := newHarness(t)
	var e errorBody
	h.get(healthRoute, healthRoute, http.StatusForbidden, &e)
	if e.Error.Code != "forbidden" {
		t.Errorf("code = %s, want forbidden", e.Error.Code)
	}
	rec := h.do(req{target: healthRoute, header: map[string]string{"Authorization": "Bearer wrong"}})
	h.expect(http.MethodGet, healthRoute, rec, http.StatusForbidden, nil)

	open := &harness{t: t, clock: h.clock, h: httpapi.New(mockdata.New(h.clock.Now), httpapi.Options{Now: h.clock.Now})}
	rec = open.do(req{target: healthRoute, header: map[string]string{"Authorization": "Bearer "}})
	open.expect(http.MethodGet, healthRoute, rec, http.StatusForbidden, nil)
}

type health struct {
	Chains  []struct{ Chain string }
	Parsers []struct {
		Module   string
		Failures int
	}
	Failures []struct{ ID, Module, Status string }
}

func TestAdminHealthAndRetry(t *testing.T) {
	h := newHarness(t)
	var hl health
	h.get(healthRoute, healthRoute, http.StatusOK, &hl, true)
	if len(hl.Chains) != 3 || len(hl.Parsers) == 0 || len(hl.Failures) == 0 {
		t.Fatalf("health = %+v; want 3 chains, decoders and failures", hl)
	}
	f := hl.Failures[0]
	failuresOf := func(hl health, module string) int {
		for _, p := range hl.Parsers {
			if p.Module == module {
				return p.Failures
			}
		}
		return -1
	}
	before := failuresOf(hl, f.Module)

	route := "/api/v1/admin/parse-failures/{id}/retry"
	var got struct{ ID, Status string }
	rec := h.do(req{method: http.MethodPost, target: "/api/v1/admin/parse-failures/" + f.ID + "/retry", admin: true})
	h.expect(http.MethodPost, route, rec, http.StatusAccepted, &got)
	if got.ID != f.ID || got.Status != "queued" {
		t.Errorf("retry = %+v, want %s queued", got, f.ID)
	}
	h.get(healthRoute, healthRoute, http.StatusOK, &hl, true)
	if after := failuresOf(hl, f.Module); after != before-1 {
		t.Errorf("%s failures %d → %d, want one fewer", f.Module, before, after)
	}
	rec = h.do(req{method: http.MethodPost, target: "/api/v1/admin/parse-failures/nope/retry", admin: true})
	h.expect(http.MethodPost, route, rec, http.StatusNotFound, nil)
}
