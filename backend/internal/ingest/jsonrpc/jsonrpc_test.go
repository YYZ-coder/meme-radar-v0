package jsonrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
)

// serve replies to every request with status, headers and the body of a
// recorded response file from ../testdata, capturing the request body.
func serve(t *testing.T, status int, header map[string]string, file string, got *[]byte) *jsonrpc.Client {
	t.Helper()
	body, err := os.ReadFile("../testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			*got, _ = io.ReadAll(r.Body)
		}
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return jsonrpc.New(srv.URL, srv.Client(), nil, nil)
}

func TestCallSendsRequestAndReturnsJSONRPCError(t *testing.T) {
	var req []byte
	c := serve(t, 200, nil, "solana_getblock_skipped.json", &req)
	host := strings.TrimPrefix(c.URL(), "http://")
	_, err := c.Call(context.Background(), "getBlock", []any{460201745})
	if got := testutil.ToFloat64(metrics.RPCRequests.WithLabelValues(host, "getBlock", "rpc_error")); got != 1 {
		t.Errorf("rpc_error requests reported = %v; want 1", got)
	}
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32007 {
		t.Fatalf("Call err = %v; want *jsonrpc.Error code -32007 (recorded skipped-slot response)", err)
	}
	var sent struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  []int  `json:"params"`
	}
	if err := json.Unmarshal(req, &sent); err != nil || sent.JSONRPC != "2.0" || sent.Method != "getBlock" || len(sent.Params) != 1 || sent.Params[0] != 460201745 {
		t.Fatalf("request body = %s; want a JSON-RPC 2.0 getBlock call with params [460201745]", req)
	}
}

func TestCall429ReturnsRetryAfter(t *testing.T) {
	c := serve(t, 429, map[string]string{"Retry-After": "10"}, "solana_getblock_429.json", nil)
	host := strings.TrimPrefix(c.URL(), "http://")
	before := testutil.ToFloat64(metrics.RPCRequests.WithLabelValues(host, "getBlock", "rate_limited"))
	_, err := c.Call(context.Background(), "getBlock", []any{1})
	if got := testutil.ToFloat64(metrics.RPCRequests.WithLabelValues(host, "getBlock", "rate_limited")) - before; got != 1 {
		t.Errorf("rate_limited requests reported = %v; want 1", got)
	}
	var rl *jsonrpc.RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter() != 10*time.Second {
		t.Fatalf("Call err = %v; want *RateLimitError with RetryAfter 10s (recorded 429 response)", err)
	}
}

func TestCallReturnsNullResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":null}`) // Base for a future block
	}))
	defer srv.Close()
	res, err := jsonrpc.New(srv.URL, srv.Client(), nil, nil).Call(context.Background(), "eth_getBlockByNumber", []any{"0xffffffff", true})
	if err != nil || string(res) != "null" {
		t.Fatalf("Call = %q, %v; want null result", res, err)
	}
}
