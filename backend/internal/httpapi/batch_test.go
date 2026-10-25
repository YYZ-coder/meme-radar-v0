package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

const batchRoute = "/api/v1/tokens/batch"

func (h *harness) batch(body any, status int, out any) {
	h.t.Helper()
	rec := h.do(req{method: http.MethodPost, target: batchRoute, body: jsonBody(body), header: map[string]string{"Content-Type": "application/json"}})
	h.expect(http.MethodPost, batchRoute, rec, status, out)
}

type tokenKey struct {
	Chain   string `json:"chain"`
	Address string `json:"address"`
}

// 01-api §4 step 3: refresh reads current values, validates the snapshot binding,
// and omits tokens that are not in the projection.
func TestBatchRefresh(t *testing.T) {
	h := newHarness(t)
	var p struct {
		QueryID     string `json:"query_id"`
		ListVersion string `json:"list_version"`
		Items       []inflowRow
	}
	h.get(inflowRoute, inflowRoute+"?window=1h&limit=5", http.StatusOK, &p)
	keys := []tokenKey{}
	for _, r := range p.Items {
		keys = append(keys, tokenKey{r.Token.Chain, r.Token.Address})
	}
	withUnknown := append(keys, tokenKey{"solana", "11111111111111111111111111111111"})

	var got struct {
		ListVersion string `json:"list_version"`
		Items       []inflowRow
	}
	h.batch(map[string]any{"window": "1h", "tokens": withUnknown}, http.StatusOK, &got)
	if len(got.Items) != len(keys) {
		t.Errorf("watchlist refresh returned %d items, want %d (unknown omitted)", len(got.Items), len(keys))
	}
	h.batch(map[string]any{"window": "1h", "tokens": keys, "query_id": p.QueryID, "list_version": p.ListVersion}, http.StatusOK, &got)
	if got.ListVersion != p.ListVersion {
		t.Errorf("list_version = %s, want %s", got.ListVersion, p.ListVersion)
	}

	var e errorBody
	h.batch(map[string]any{"window": "1h", "tokens": keys, "query_id": "nope", "list_version": p.ListVersion}, http.StatusGone, &e)
	h.batch(map[string]any{"window": "1h", "tokens": keys, "query_id": p.QueryID, "list_version": "41"}, http.StatusGone, &e)
	if e.Error.Code != "query_expired" {
		t.Errorf("stale list_version code = %s, want query_expired", e.Error.Code)
	}
	h.batch(map[string]any{"window": "24h", "tokens": keys, "query_id": p.QueryID, "list_version": p.ListVersion}, http.StatusBadRequest, nil)
	h.batch(map[string]any{"window": "1h", "currency": "native", "tokens": keys, "query_id": p.QueryID, "list_version": p.ListVersion}, http.StatusBadRequest, nil)
	h.batch(map[string]any{"window": "1h", "tokens": keys, "query_id": p.QueryID}, http.StatusBadRequest, nil)
	h.batch(map[string]any{"window": "1h", "tokens": []tokenKey{}}, http.StatusBadRequest, &e)
	if e.Error.Field == nil || *e.Error.Field != "tokens" {
		t.Errorf("empty tokens field = %v, want tokens", e.Error.Field)
	}
	many := make([]tokenKey, 101)
	for i := range many {
		many[i] = keys[0]
	}
	h.batch(map[string]any{"window": "1h", "tokens": many}, http.StatusBadRequest, nil)
	rec := h.do(req{method: http.MethodPost, target: batchRoute, body: strings.NewReader("{")})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON status %d, want 400", rec.Code)
	}
}
