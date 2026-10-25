// Package prices fetches native-coin USD prices (SOL, ETH, BNB). Each
// provider is a Source: Binance (USDT pairs, 1 USDT = 1 USD as for
// stablecoins in D135), Coinbase (USD pairs) and Pyth Hermes (needs a key);
// the config file chooses them and their order (D147).
package prices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/meme-radar/radar/internal/metrics"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/meme-radar/radar/internal/config"
)

// Price is one USD price; USD is a decimal string.
type Price struct {
	Symbol string
	T      time.Time // minute start for Minutes; quote time for Latest
	USD    string
	Source string // binance, coinbase
}

// Source returns latest prices and per-minute closes.
type Source interface {
	Latest(ctx context.Context, symbols []string) (map[string]Price, error)
	// Minutes returns the close of every minute in [from, to] it has, oldest first.
	Minutes(ctx context.Context, symbol string, from, to time.Time) ([]Price, error)
}

func get(ctx context.Context, c *http.Client, u string, v any) error {
	return getWith(ctx, c, u, nil, v)
}

func getWith(ctx context.Context, c *http.Client, u string, header http.Header, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	req.Header.Set("User-Agent", "meme-radar")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prices: %s: %s: %.200s", u, resp.Status, b)
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	return d.Decode(v)
}

// Binance reads <SYMBOL>USDT spot pairs.
type Binance struct {
	base string
	c    *http.Client
}

// binanceKlines is the API's page limit.
const binanceKlines = 1000

func NewBinance(baseURL string, c *http.Client) *Binance {
	return &Binance{strings.TrimRight(baseURL, "/"), c}
}

func (b *Binance) Latest(ctx context.Context, symbols []string) (map[string]Price, error) {
	pairs, _ := json.Marshal(mapSlice(symbols, func(s string) string { return s + "USDT" }))
	var rows []struct{ Symbol, Price string }
	if err := get(ctx, b.c, b.base+"/api/v3/ticker/price?"+url.Values{"symbols": {string(pairs)}}.Encode(), &rows); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := map[string]Price{}
	for _, r := range rows {
		if s, ok := strings.CutSuffix(r.Symbol, "USDT"); ok {
			out[s] = Price{Symbol: s, T: now, USD: trimZeros(r.Price), Source: "binance"}
		}
	}
	return complete(out, symbols, "binance")
}

func (b *Binance) Minutes(ctx context.Context, symbol string, from, to time.Time) ([]Price, error) {
	var out []Price
	for start := from.Truncate(time.Minute); !start.After(to); start = start.Add(binanceKlines * time.Minute) {
		end := earlier(to.Truncate(time.Minute), start.Add((binanceKlines-1)*time.Minute))
		q := url.Values{"symbol": {symbol + "USDT"}, "interval": {"1m"}, "limit": {fmt.Sprint(binanceKlines)},
			"startTime": {fmt.Sprint(start.UnixMilli())}, "endTime": {fmt.Sprint(end.Add(time.Minute).UnixMilli() - 1)}}
		var rows [][]json.Number // [openTime, open, high, low, close, ...]
		if err := get(ctx, b.c, b.base+"/api/v3/klines?"+q.Encode(), &rows); err != nil {
			return nil, err
		}
		for _, k := range rows {
			if len(k) < 5 {
				return nil, errors.New("prices: short binance kline")
			}
			ms, err := k[0].Int64()
			if err != nil {
				return nil, err
			}
			out = append(out, Price{Symbol: symbol, T: time.UnixMilli(ms).UTC(), USD: trimZeros(k[4].String()), Source: "binance"})
		}
	}
	return out, nil
}

// Coinbase reads <SYMBOL>-USD products.
type Coinbase struct {
	base string
	c    *http.Client
}

// coinbaseCandles is the API's page limit.
const coinbaseCandles = 300

func NewCoinbase(baseURL string, c *http.Client) *Coinbase {
	return &Coinbase{strings.TrimRight(baseURL, "/"), c}
}

func (c *Coinbase) Latest(ctx context.Context, symbols []string) (map[string]Price, error) {
	out := map[string]Price{}
	for _, s := range symbols {
		var t struct {
			Price string
			Time  time.Time
		}
		if err := get(ctx, c.c, c.base+"/products/"+s+"-USD/ticker", &t); err != nil {
			return nil, err
		}
		out[s] = Price{Symbol: s, T: t.Time.UTC(), USD: trimZeros(t.Price), Source: "coinbase"}
	}
	return complete(out, symbols, "coinbase")
}

func (c *Coinbase) Minutes(ctx context.Context, symbol string, from, to time.Time) ([]Price, error) {
	var out []Price
	for start := from.Truncate(time.Minute); !start.After(to); start = start.Add(coinbaseCandles * time.Minute) {
		end := earlier(to.Truncate(time.Minute), start.Add((coinbaseCandles-1)*time.Minute))
		q := url.Values{"granularity": {"60"}, "start": {start.Format(time.RFC3339)}, "end": {end.Format(time.RFC3339)}}
		var rows [][]json.Number // newest first: [time, low, high, open, close, volume]
		if err := get(ctx, c.c, c.base+"/products/"+symbol+"-USD/candles?"+q.Encode(), &rows); err != nil {
			return nil, err
		}
		for i := len(rows) - 1; i >= 0; i-- {
			k := rows[i]
			if len(k) < 5 {
				return nil, errors.New("prices: short coinbase candle")
			}
			sec, err := k[0].Int64()
			if err != nil {
				return nil, err
			}
			out = append(out, Price{Symbol: symbol, T: time.Unix(sec, 0).UTC(), USD: trimZeros(k[4].String()), Source: "coinbase"})
		}
	}
	return out, nil
}

// instrumented reports each request's outcome (radar_price_requests_total);
// it decorates any Source.
type instrumented struct {
	Source
	name string
}

func (i instrumented) report(err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	metrics.PriceRequests.WithLabelValues(i.name, outcome).Inc()
}

func (i instrumented) Latest(ctx context.Context, symbols []string) (map[string]Price, error) {
	p, err := i.Source.Latest(ctx, symbols)
	i.report(err)
	return p, err
}

func (i instrumented) Minutes(ctx context.Context, symbol string, from, to time.Time) ([]Price, error) {
	p, err := i.Source.Minutes(ctx, symbol, from, to)
	i.report(err)
	return p, err
}

type fallback []Source

// Fallback tries each source in order and returns the first full answer.
func Fallback(sources ...Source) Source { return fallback(sources) }

func (f fallback) Latest(ctx context.Context, symbols []string) (map[string]Price, error) {
	var errs []error
	for _, s := range f {
		p, err := s.Latest(ctx, symbols)
		if err == nil {
			return p, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

func (f fallback) Minutes(ctx context.Context, symbol string, from, to time.Time) ([]Price, error) {
	var errs []error
	for _, s := range f {
		p, err := s.Minutes(ctx, symbol, from, to)
		if err == nil {
			return p, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

func complete(out map[string]Price, symbols []string, source string) (map[string]Price, error) {
	for _, s := range symbols {
		if _, ok := out[s]; !ok {
			return nil, fmt.Errorf("prices: %s has no price for %s", source, s)
		}
	}
	return out, nil
}

// trimZeros drops trailing fractional zeros ("103.62000000" -> "103.62").
func trimZeros(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func mapSlice[T, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

// FromConfig builds the configured sources, tried in order. Each type is
// one Source strategy (binance, coinbase, pyth); switching providers is a
// change of the config file only. Keys come from getenv.
func FromConfig(cfg config.Prices, c *http.Client, getenv func(string) string) (Source, error) {
	if len(cfg.Sources) == 0 {
		return nil, errors.New("prices: no price sources configured")
	}
	var out []Source
	for i, s := range cfg.Sources {
		var src Source
		switch s.Type {
		case "binance":
			src = NewBinance(s.URL, c)
		case "coinbase":
			src = NewCoinbase(s.URL, c)
		case "pyth":
			key := ""
			if s.APIKeyEnv != "" {
				if key = getenv(s.APIKeyEnv); key == "" {
					return nil, fmt.Errorf("prices: sources[%d] pyth: %s is not set", i, s.APIKeyEnv)
				}
			}
			src = NewPyth(s.URL, c, s.APIKeyHeader, key, s.Feeds)
		default:
			return nil, fmt.Errorf("prices: sources[%d]: unknown type %q", i, s.Type)
		}
		out = append(out, instrumented{src, s.Type})
	}
	if len(out) == 1 {
		return out[0], nil
	}
	return Fallback(out...), nil
}
