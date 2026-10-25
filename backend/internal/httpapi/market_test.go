package httpapi_test

import (
	"net/http"
	"testing"
)

const gx1j = "GX1JGwFmrzgkv6MBjwZZdZt3qBcWi1d5ZuAUS3KFpump"

// Bottom bar: one USD price per chain's native coin, in chain order.
func TestNativePrices(t *testing.T) {
	h := newHarness(t)
	var body struct {
		Items []struct {
			Chain, Symbol string
			PriceUSD      *string `json:"price_usd"`
		}
	}
	h.get("/api/v1/market/natives", "/api/v1/market/natives", http.StatusOK, &body)
	want := [][2]string{{"solana", "SOL"}, {"base", "ETH"}, {"bsc", "BNB"}}
	if len(body.Items) != len(want) {
		t.Fatalf("items = %d, want 3", len(body.Items))
	}
	for i, w := range want {
		if body.Items[i].Chain != w[0] || body.Items[i].Symbol != w[1] || body.Items[i].PriceUSD == nil {
			t.Errorf("item %d = %+v, want %v with a price", i, body.Items[i], w)
		}
	}
}

// Search: exact address ranks first; q is required; limit bounds follow the contract (1–20).
func TestSearch(t *testing.T) {
	h := newHarness(t)
	var body struct {
		Tokens []struct{ Chain, Address, Symbol string }
	}
	h.get("/api/v1/search", "/api/v1/search?q="+gx1j+"&limit=8", http.StatusOK, &body)
	if len(body.Tokens) == 0 || body.Tokens[0].Address != gx1j || body.Tokens[0].Chain != "solana" {
		t.Fatalf("exact address search first hit = %+v, want solana %s", body.Tokens, gx1j)
	}
	h.get("/api/v1/search", "/api/v1/search?q=PEPE&limit=3", http.StatusOK, &body)
	if len(body.Tokens) == 0 || len(body.Tokens) > 3 {
		t.Errorf("symbol search returned %d tokens, want 1..3", len(body.Tokens))
	}
	var e errorBody
	h.get("/api/v1/search", "/api/v1/search?q=", http.StatusBadRequest, &e)
	if e.Error.Code != "invalid_param" || e.Error.Field == nil || *e.Error.Field != "q" {
		t.Errorf("empty q error = %+v, want invalid_param on q", e.Error)
	}
	h.get("/api/v1/search", "/api/v1/search?q=PEPE&limit=21", http.StatusBadRequest, &e)
}
