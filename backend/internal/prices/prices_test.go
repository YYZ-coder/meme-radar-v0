package prices_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/prices"
)

// replay serves the recorded responses in testdata (2026-10-23) by request.
func replay(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, file := range routes {
			if strings.HasPrefix(r.URL.Path+"?"+r.URL.RawQuery, prefix) {
				if file == "" {
					http.Error(w, `{"code":0,"msg":"Service unavailable from a restricted location"}`, http.StatusUnavailableForLegalReasons)
					return
				}
				b, err := os.ReadFile("testdata/" + file)
				if err != nil {
					t.Fatal(err)
				}
				w.Write(b)
				return
			}
		}
		t.Errorf("unexpected request %s", r.URL)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

var minute0 = time.Unix(1792713600, 0).UTC()

func binance(t *testing.T) *prices.Binance {
	srv := replay(t, map[string]string{
		"/api/v3/ticker/price?": "binance_ticker.json",
		"/api/v3/klines?endTime=1792713779999&interval=1m&limit=1000&startTime=1792713600000&symbol=SOLUSDT": "binance_klines_sol.json",
	})
	return prices.NewBinance(srv.URL, srv.Client())
}

func coinbase(t *testing.T) *prices.Coinbase {
	srv := replay(t, map[string]string{
		"/products/SOL-USD/ticker": "coinbase_ticker_SOL.json",
		"/products/ETH-USD/ticker": "coinbase_ticker_ETH.json",
		"/products/BNB-USD/ticker": "coinbase_ticker_BNB.json",
		"/products/SOL-USD/candles?end=2026-10-23T00%3A02%3A00Z&granularity=60&start=2026-10-23T00%3A00%3A00Z": "coinbase_candles_sol.json",
	})
	return prices.NewCoinbase(srv.URL, srv.Client())
}

func usd(m map[string]prices.Price) map[string]string {
	out := map[string]string{}
	for k, p := range m {
		out[k] = p.USD + "@" + p.Source
	}
	return out
}

func closes(ps []prices.Price) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Symbol+" "+p.T.Format("15:04")+" "+p.USD+" "+p.Source)
	}
	return out
}

func TestLatestReadsExchangeTickers(t *testing.T) {
	ctx := context.Background()
	syms := []string{"SOL", "ETH", "BNB"}
	got, err := binance(t).Latest(ctx, syms)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"SOL": "104.23@binance", "ETH": "2399.71@binance", "BNB": "747.16@binance"}
	if g := usd(got); !mapsEqual(g, want) {
		t.Errorf("binance latest = %v; want %v", g, want)
	}
	got, err = coinbase(t).Latest(ctx, syms)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]string{"SOL": "104.11@coinbase", "ETH": "2396.54@coinbase", "BNB": "745.91@coinbase"}
	if g := usd(got); !mapsEqual(g, want) {
		t.Errorf("coinbase latest = %v; want %v", g, want)
	}
}

func TestMinutesReturnsClosesInOrder(t *testing.T) {
	ctx := context.Background()
	to := minute0.Add(2 * time.Minute)
	got, err := binance(t).Minutes(ctx, "SOL", minute0, to)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SOL 00:00 103.62 binance", "SOL 00:01 103.66 binance", "SOL 00:02 103.67 binance"}
	if g := closes(got); !slices.Equal(g, want) {
		t.Errorf("binance minutes = %v; want %v", g, want)
	}
	got, err = coinbase(t).Minutes(ctx, "SOL", minute0, to)
	if err != nil {
		t.Fatal(err)
	}
	// Coinbase answers newest first as [time, low, high, open, close, volume].
	want = []string{"SOL 00:00 103.51 coinbase", "SOL 00:01 103.54 coinbase", "SOL 00:02 103.54 coinbase"}
	if g := closes(got); !slices.Equal(g, want) {
		t.Errorf("coinbase minutes = %v; want %v", g, want)
	}
}

func TestFallbackUsesCoinbaseWhenBinanceIsBlocked(t *testing.T) {
	blocked := replay(t, map[string]string{"/api/v3/": ""})
	src := prices.Fallback(prices.NewBinance(blocked.URL, blocked.Client()), coinbase(t))
	got, err := src.Latest(context.Background(), []string{"SOL", "ETH", "BNB"})
	if err != nil {
		t.Fatal(err)
	}
	if g := usd(got); g["SOL"] != "104.11@coinbase" || g["BNB"] != "745.91@coinbase" {
		t.Errorf("fallback latest = %v; want coinbase prices", g)
	}
	ps, err := src.Minutes(context.Background(), "SOL", minute0, minute0.Add(2*time.Minute))
	if err != nil || len(ps) != 3 || ps[0].Source != "coinbase" {
		t.Errorf("fallback minutes = %v, %v; want 3 coinbase closes", closes(ps), err)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
