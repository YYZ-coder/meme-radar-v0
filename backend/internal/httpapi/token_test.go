package httpapi_test

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
)

const (
	tokenRoute = "/api/v1/tokens/{chain}/{address}"
	gx1jPath   = "/api/v1/tokens/solana/" + gx1j
	ribbitPath = "/api/v1/tokens/base/0x7d4bfd446d4b81d5ede62529f9c8db6fdd81e222"
)

type wallet struct {
	Address string
	Labels  []string
}

func TestTokenDetail(t *testing.T) {
	h := newHarness(t)
	var d struct {
		Token          struct{ Address string }
		Currency       string
		MainPool       *struct{ Address string } `json:"main_pool"`
		HolderCoverage struct{ Status string }   `json:"holder_coverage"`
		Windows        []struct{ Window string }
	}
	h.get(tokenRoute, gx1jPath, http.StatusOK, &d)
	if d.Token.Address != gx1j || d.Currency != "USD" || d.HolderCoverage.Status != "full" || d.MainPool == nil {
		t.Fatalf("GX1J detail = %+v; want USD, full coverage, main pool", d)
	}
	var ws []string
	for _, w := range d.Windows {
		ws = append(ws, w.Window)
	}
	if !slices.Equal(ws, []string{"5m", "1h", "4h", "24h"}) {
		t.Errorf("windows = %v", ws)
	}
	h.get(tokenRoute, gx1jPath+"?currency=native", http.StatusOK, &d)
	if d.Currency != "SOL" {
		t.Errorf("native currency = %s, want SOL", d.Currency)
	}
	h.get(tokenRoute, ribbitPath, http.StatusOK, &d)
	if d.HolderCoverage.Status != "partial" {
		t.Errorf("RIBBIT (created before the stream start) coverage = %s, want partial", d.HolderCoverage.Status)
	}
	var e errorBody
	h.get(tokenRoute, "/api/v1/tokens/solana/11111111111111111111111111111111", http.StatusNotFound, &e)
	if e.Error.Code != "not_found" {
		t.Errorf("unknown token code = %s", e.Error.Code)
	}
	h.get(tokenRoute, "/api/v1/tokens/eth/0x00", http.StatusBadRequest, &e)

	var pools struct {
		Items []struct {
			Address string
			IsMain  bool `json:"is_main"`
		}
	}
	h.get(tokenRoute+"/pools", gx1jPath+"/pools", http.StatusOK, &pools)
	var mains []string
	for _, p := range pools.Items {
		if p.IsMain {
			mains = append(mains, p.Address)
		}
	}
	h.get(tokenRoute, gx1jPath, http.StatusOK, &d)
	if len(mains) != 1 || mains[0] != d.MainPool.Address {
		t.Errorf("main pools %v, header main pool %s", mains, d.MainPool.Address)
	}
}

func TestCandles(t *testing.T) {
	h := newHarness(t)
	steps := map[string]int64{"1s": 1, "30s": 30, "1m": 60, "5m": 300, "1h": 3600, "1d": 86400}
	for iv, step := range steps {
		for _, metric := range []string{"price", "market_cap"} {
			var c struct{ Items []struct{ T int64 } }
			h.get(tokenRoute+"/candles", gx1jPath+"/candles?interval="+iv+"&metric="+metric+"&limit=300", http.StatusOK, &c)
			if len(c.Items) == 0 || len(c.Items) > 300 {
				t.Fatalf("%s %s: %d candles, want 1..300", iv, metric, len(c.Items))
			}
			for i, x := range c.Items {
				if x.T%step != 0 || (i > 0 && x.T <= c.Items[i-1].T) {
					t.Fatalf("%s: candle %d t=%d not aligned/increasing", iv, i, x.T)
				}
			}
		}
	}
	var e errorBody
	h.get(tokenRoute+"/candles", gx1jPath+"/candles?interval=2m", http.StatusBadRequest, &e)
	if e.Error.Field == nil || *e.Error.Field != "interval" {
		t.Errorf("bad interval field = %v", e.Error.Field)
	}
	h.get(tokenRoute+"/candles", gx1jPath+"/candles", http.StatusBadRequest, nil)
}

func TestActivities(t *testing.T) {
	h := newHarness(t)
	route := tokenRoute + "/activities"
	type page struct {
		Items []struct {
			ID, Kind string
			Wallet   *wallet
		}
		NextCursor *string `json:"next_cursor"`
	}
	kinds := map[string][]string{"trade": {"buy", "sell"}, "liquidity": {"add", "remove"}}
	for kind, allowed := range kinds {
		var p page
		h.get(route, gx1jPath+"/activities?kind="+kind+"&limit=50", http.StatusOK, &p)
		if len(p.Items) == 0 {
			t.Fatalf("kind=%s returned nothing", kind)
		}
		for _, a := range p.Items {
			if !slices.Contains(allowed, a.Kind) {
				t.Fatalf("kind=%s returned %s", kind, a.Kind)
			}
		}
	}
	var p page
	h.get(route, gx1jPath+"/activities?kind=trade&label=smart_money&limit=50", http.StatusOK, &p)
	for _, a := range p.Items {
		if a.Wallet == nil || !slices.Contains(a.Wallet.Labels, "smart_money") {
			t.Fatalf("label filter let through %+v", a)
		}
	}
	seen := map[string]bool{}
	h.get(route, gx1jPath+"/activities?limit=50", http.StatusOK, &p)
	for page := 0; page < 3 && p.NextCursor != nil; page++ {
		for _, a := range p.Items {
			if seen[a.ID] {
				t.Fatalf("activity %s repeated across pages", a.ID)
			}
			seen[a.ID] = true
		}
		cursor := *p.NextCursor
		p = struct {
			Items []struct {
				ID, Kind string
				Wallet   *wallet
			}
			NextCursor *string `json:"next_cursor"`
		}{}
		h.get(route, gx1jPath+"/activities?limit=50&cursor="+url.QueryEscape(cursor), http.StatusOK, &p)
	}
	h.get(route, gx1jPath+"/activities?kind=swap", http.StatusBadRequest, nil)
	h.get(route, gx1jPath+"/activities?limit=101", http.StatusBadRequest, nil)

	var lq struct{ Items []struct{ Kind string } }
	h.get(tokenRoute+"/liquidity-events", gx1jPath+"/liquidity-events?limit=50", http.StatusOK, &lq)
	for _, x := range lq.Items {
		if x.Kind != "add" && x.Kind != "remove" {
			t.Fatalf("liquidity event kind %s", x.Kind)
		}
	}
}

// Data model §8.2: ranks continue across pages; PnL and average prices stay null before M2.
func TestHolders(t *testing.T) {
	h := newHarness(t)
	route := tokenRoute + "/holders"
	type page struct {
		Items []struct {
			Rank        int64
			Wallet      wallet
			AvgBuy      *string `json:"avg_buy_price_usd"`
			RealizedPnL *string `json:"realized_pnl_usd"`
		}
		NextCursor *string `json:"next_cursor"`
	}
	var p page
	h.get(route, gx1jPath+"/holders?limit=50", http.StatusOK, &p)
	if len(p.Items) != 50 || p.Items[0].Rank != 1 || p.NextCursor == nil {
		t.Fatalf("first holders page: %d items, first rank %v", len(p.Items), p.Items)
	}
	for i, x := range p.Items {
		if x.Rank != int64(i+1) || x.AvgBuy != nil || x.RealizedPnL != nil {
			t.Fatalf("holder %d = %+v; want rank %d and null M2 fields", i, x, i+1)
		}
	}
	cursor := *p.NextCursor
	p = page{}
	h.get(route, gx1jPath+"/holders?limit=50&cursor="+url.QueryEscape(cursor), http.StatusOK, &p)
	if len(p.Items) == 0 || p.Items[0].Rank != 51 {
		t.Fatalf("second page first rank = %v, want 51", p.Items)
	}
	p = page{}
	h.get(route, gx1jPath+"/holders?label=smart_money", http.StatusOK, &p)
	for _, x := range p.Items {
		if !slices.Contains(x.Wallet.Labels, "smart_money") {
			t.Fatalf("smart_money holder without label: %+v", x)
		}
	}
	var sm struct {
		ListVersion string `json:"list_version"`
		Items       []struct{ Wallet wallet }
	}
	h.get(tokenRoute+"/smart-money", gx1jPath+"/smart-money", http.StatusOK, &sm)
	if _, err := strconv.Atoi(sm.ListVersion); err != nil || len(sm.Items) == 0 {
		t.Fatalf("token smart money = %+v", sm)
	}
	for _, x := range sm.Items {
		if !slices.Contains(x.Wallet.Labels, "smart_money") {
			t.Fatalf("smart money entry without label: %+v", x)
		}
	}
}
