package prices_test

import (
	"context"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/prices"
)

const (
	solFeed = "ef0d8b6fda2ceba41da15d4095d1da392a0d2f8ed0c6c7bc0f4cfac8c280b56d"
	ethFeed = "ff61491a931112ddf1bd8147cd1b641375f79f5825126d665480874634fd0ace"
)

// pythServer answers like Hermes v2. The bodies (testdata/pyth_*.json) are
// written from the documented response format: the public endpoint
// answered 401 on 2026-10-23, so no live response could be recorded.
func pythServer(t *testing.T, key string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != key {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		file := map[string]string{
			"/v2/updates/price/latest":     "pyth_latest.json",
			"/v2/updates/price/1792713659": "pyth_at_1792713659.json",
		}[r.URL.Path]
		if file == "" || r.URL.Query().Get("parsed") != "true" {
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
			return
		}
		b, _ := os.ReadFile("testdata/" + file)
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFromConfigBuildsSourcesInOrder(t *testing.T) {
	ctx := context.Background()
	cb := replay(t, map[string]string{"/products/SOL-USD/ticker": "coinbase_ticker_SOL.json"})
	bn := replay(t, map[string]string{"/api/v3/ticker/price?": "binance_ticker.json"})
	src, err := prices.FromConfig(config.Prices{Sources: []config.PriceSource{
		{Type: "coinbase", URL: cb.URL}, {Type: "binance", URL: bn.URL},
	}}, http.DefaultClient, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	before := testutil.ToFloat64(metrics.PriceRequests.WithLabelValues("coinbase", "ok"))
	got, err := src.Latest(ctx, []string{"SOL"})
	if d := testutil.ToFloat64(metrics.PriceRequests.WithLabelValues("coinbase", "ok")) - before; d != 1 {
		t.Errorf("coinbase ok requests reported = %v; want 1", d)
	}
	if err != nil || got["SOL"].Source != "coinbase" {
		t.Errorf("Latest = %v, %v; want the first configured source (coinbase)", got, err)
	}
	for _, bad := range []config.Prices{
		{},
		{Sources: []config.PriceSource{{Type: "kraken", URL: "https://x"}}},
		{Sources: []config.PriceSource{{Type: "pyth", URL: "https://x", APIKeyEnv: "RADAR_PYTH_API_KEY", APIKeyHeader: "x-api-key", Feeds: map[string]string{"SOL": solFeed}}}}, // key unset
	} {
		if _, err := prices.FromConfig(bad, http.DefaultClient, func(string) string { return "" }); err == nil {
			t.Errorf("FromConfig(%+v) succeeded; want an error", bad)
		}
	}
}

func TestPythReadsHermesPrices(t *testing.T) {
	ctx := context.Background()
	srv := pythServer(t, "k1")
	env := map[string]string{"RADAR_PYTH_API_KEY": "k1"}
	src, err := prices.FromConfig(config.Prices{Sources: []config.PriceSource{{Type: "pyth", URL: srv.URL, APIKeyEnv: "RADAR_PYTH_API_KEY",
		APIKeyHeader: "x-api-key", Feeds: map[string]string{"SOL": solFeed, "ETH": ethFeed}}}}, srv.Client(), func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Latest(ctx, []string{"SOL", "ETH"})
	// price x 10^expo: 10423000000e-8, 239971000000e-8.
	if err != nil || got["SOL"].USD != "104.23" || got["ETH"].USD != "2399.71" || got["SOL"].Source != "pyth" {
		t.Errorf("Latest = %+v, %v; want SOL 104.23 and ETH 2399.71 from pyth", got, err)
	}
	// A minute's close is the price at its last second: 1036205e-4.
	ps, err := src.Minutes(ctx, "SOL", time.Unix(1792713600, 0), time.Unix(1792713600, 0))
	if err != nil || len(ps) != 1 || ps[0].USD != "103.6205" || !ps[0].T.Equal(time.Unix(1792713600, 0)) {
		t.Errorf("Minutes = %+v, %v; want 103.6205 at 00:00", ps, err)
	}
	if _, err := src.Latest(ctx, []string{"BNB"}); err == nil {
		t.Errorf("Latest(BNB) without a configured feed succeeded")
	}
}
