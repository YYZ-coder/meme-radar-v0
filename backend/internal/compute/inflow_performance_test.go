package compute_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// TestInflowPerformance35000 is an opt-in synthetic local measurement, never
// a cloud-write benchmark. It also measures the actual compute child process.
func TestInflowPerformance35000(t *testing.T) {
	if os.Getenv("RADAR_H4_PERF") != "1" {
		t.Skip("manual 35k-token local performance measurement")
	}
	s, c := open(t)
	ctx := t.Context()
	cutoff := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	key := func(prefix byte, n uint64) []byte {
		b := make([]byte, 32)
		b[0] = prefix
		binary.BigEndian.PutUint64(b[24:], n)
		return b
	}
	quote := key('q', 1)
	six := int16(6)
	if err := s.SaveQuoteAssets(ctx, []store.QuoteAsset{{Chain: "solana", Asset: quote, Symbol: "USDC", Kind: "stable", Decimals: 6}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQuotePrices(ctx, []store.QuotePrice{{Symbol: "SOL", Minute: cutoff.Add(-time.Minute), USD: "100", Source: "binance"}}); err != nil {
		t.Fatal(err)
	}
	apply(t, s, "solana", 1, cutoff.Add(-25*time.Hour), parse.Facts{})
	seedStarted := time.Now()
	changes := make([]store.MainPoolChange, 0, 35000)
	for batch := 0; batch < 35; batch++ {
		f := parse.Facts{}
		for i := 0; i < 1000; i++ {
			n := uint64(batch*1000 + i + 1)
			token, pool := key('t', n), key('p', n)
			f.Tokens = append(f.Tokens, parse.Token{Address: token, Symbol: "PERF", Decimals: &six, TotalSupply: "1000000000000", Created: true})
			f.Pools = append(f.Pools, parse.Pool{ID: pool, Token: token, Quote: quote, QuoteSymbol: "USDC", Protocol: "test", Dex: "performance", Created: true, Status: "active", ReserveToken: "1000000", ReserveQuote: "100000000"})
			f.Trades = append(f.Trades, parse.Trade{Tx: i + 1, Ix: 1, TxHash: key('x', n), Token: token, Pool: pool, Wallet: key('w', n), Attribution: "confirmed", Side: "buy", TokenAmount: "1000000", QuoteAmount: "1000000", QuoteSymbol: "USDC", PriceNative: "1", Decoder: "test", Version: 1})
			changes = append(changes, store.MainPoolChange{Token: token, Pool: pool, Reason: "liquidity"})
		}
		apply(t, s, "solana", uint64(batch+2), cutoff.Add(-time.Minute+time.Duration(batch)*time.Millisecond), f)
	}
	apply(t, s, "solana", 37, cutoff.Add(time.Second), parse.Facts{})
	for _, err := range []error{s.FillValues(ctx, "solana", "SOL", cutoff.Add(-time.Hour)), s.SaveMainPools(ctx, "solana", changes, cutoff.Add(-25*time.Hour)), s.SampleLiquidity(ctx, "solana"), s.PriceLiquidity(ctx, "solana", "SOL", cutoff), s.UpdateMarket(ctx, "solana", cutoff, false), s.RefreshStats(ctx, "solana", cutoff), s.RefreshSmTrades(ctx, "solana", cutoff), s.RefreshCoverage(ctx, "solana")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("synthetic local fixture tokens=35000 trades=35000 seed=%s; exact values $1 / .01 SOL; main-pool closes via H1", time.Since(seedStarted))
	started := time.Now()
	built, err := s.RebuildInflow(ctx, "solana", "24h", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	t.Logf("local solana24h rows=%d query_decode=%s fill=%s write=%s full_transaction=%s heap_alloc_bytes=%d", built.Rows, built.QueryTime, built.FillTime, built.WriteTime, time.Since(started), mem.Alloc)
	if built.Rows != 35000 {
		t.Fatalf("benchmark published rows=%d; want 35000", built.Rows)
	}
	h4Members(t, s, cutoff, false) // formal activation requires the child to rebuild this head.
	price := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "klines") {
			fmt.Fprint(w, `[]`)
		} else {
			fmt.Fprint(w, `[{"symbol":"SOLUSDT","price":"100"}]`)
		}
	}))
	defer price.Close()
	backend, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	artifacts := filepath.Join(backend, "..", ".local", "h4-performance")
	if err = os.MkdirAll(artifacts, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "radar.yaml")
	cfg := fmt.Sprintf("chains:\n  - name: solana\n    family: solana\n    native: SOL\n    wrapped_native: So11111111111111111111111111111111111111112\n    lookup_rps: 1\n    ingest: {rpc_url: %q, rps: 1, block_interval: 1s, poll: 1s}\nprices:\n  sources: [{type: binance, url: %q}]\n", price.URL, price.URL)
	if err = os.WriteFile(configPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, filepath.Join(backend, "bin", "radar"), "compute", "--config="+configPath, "--interval=1h")
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "RADAR_DATABASE_URL=") && !strings.HasPrefix(v, "RADAR_METRICS_ADDR=") {
			env = append(env, v)
		}
	}
	cmd.Env = append(env, "RADAR_DATABASE_URL="+c.Config().ConnString(), "RADAR_METRICS_ADDR=off")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	childLog, err := os.Create(filepath.Join(artifacts, "compute.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer childLog.Close()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	observed := make(chan struct{}, 1)
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Bytes()
			fmt.Fprintln(childLog, string(line))
			var v map[string]any
			if json.Unmarshal(line, &v) == nil && v["msg"] == "inflow published" && v["window"] == "24h" {
				select {
				case observed <- struct{}{}:
				default:
				}
			}
		}
	}()
	select {
	case <-observed:
		if err = cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
	case <-runCtx.Done():
		cmd.Wait()
		<-scanned
		t.Fatal("compute did not publish synthetic 24h head before timeout")
	}
	err = cmd.Wait()
	<-scanned
	if err != nil {
		t.Fatalf("compute process stop=%v", err)
	}
	usage := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	t.Logf("actual compute child peak_rss_bytes_darwin=%d; process logs=%s; local fixture, not deployed/cloud memory", usage.Maxrss, filepath.Join(artifacts, "compute.log"))
}
