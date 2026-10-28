package jsonrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/prometheus/client_golang/prometheus"
)

// The external read protocol supplies an independently known block number.
func TestReadRPCFallsBackToPublicOnServiceUnavailable(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
	}))
	defer public.Close()
	client := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL)
	result, err := client.Call(context.Background(), "eth_blockNumber", []any{})
	if err != nil || string(result) != `"0x38"` {
		t.Fatalf("read result = %s, %v; want public node block 0x38", result, err)
	}
}

func TestFallbackDoesNotReplayTransactionSubmission(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "uncertain submission result", http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"unexpected-write-replay"}`)
	}))
	defer public.Close()
	result, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).
		Call(context.Background(), "eth_sendRawTransaction", []any{"0x0102"})
	if err == nil || result != nil {
		t.Fatalf("submission = %s, %v; want original failure without fallback replay", result, err)
	}
}

func TestReadRPCFallsBackOnProviderAvailabilityFailure(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusGatewayTimeout, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "provider unavailable", status)
			}))
			defer primary.Close()
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
			}))
			defer public.Close()
			result, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).
				Call(context.Background(), "eth_blockNumber", []any{})
			if err != nil || string(result) != `"0x38"` {
				t.Fatalf("read after HTTP %d = %s, %v; want public node result", status, result, err)
			}
		})
	}
}

func TestReadRPCFallsBackOnConnectionFailure(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
	}))
	defer public.Close()
	result, err := jsonrpc.New(primary.URL, public.Client(), nil, nil, public.URL).
		Call(context.Background(), "eth_blockNumber", []any{})
	if err != nil || string(result) != `"0x38"` {
		t.Fatalf("read after connection failure = %s, %v; want public node result", result, err)
	}
}

func TestSlowPrimaryLeavesDeadlineBudgetForPublicFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
	}))
	defer public.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	result, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).
		Call(ctx, "eth_blockNumber", []any{})
	if err != nil || string(result) != `"0x38"` {
		t.Fatalf("read with slow primary = %s, %v; want public result within the caller deadline", result, err)
	}
}

func TestFailedRPCDoesNotExposeEndpointCredential(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	public.Close()
	_, err := jsonrpc.New(primary.URL+"/private-credential/", http.DefaultClient, nil, nil, public.URL+"/fallback-credential/").
		Call(context.Background(), "eth_blockNumber", []any{})
	if err == nil {
		t.Fatal("both providers are offline; want a transport error")
	}
	for _, credential := range []string{"private-credential", "fallback-credential"} {
		if strings.Contains(err.Error(), credential) {
			t.Fatalf("RPC transport error exposes an endpoint credential")
		}
	}
}

func TestReadRPCFallsBackOnProviderJSONRPCFailure(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"upstream temporarily unavailable"}}`)
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
	}))
	defer public.Close()
	result, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).
		Call(context.Background(), "eth_blockNumber", []any{})
	if err != nil || string(result) != `"0x38"` {
		t.Fatalf("read after provider JSON-RPC failure = %s, %v; want public result", result, err)
	}
}

func TestReadRPCFallsBackWhenProviderLacksMethodOrIsUnhealthy(t *testing.T) {
	for _, code := range []int{-32601, -32005} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": code, "message": "provider cannot serve this read"}})
			}))
			defer primary.Close()
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
			}))
			defer public.Close()
			result, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).
				Call(context.Background(), "eth_blockNumber", []any{})
			if err != nil || string(result) != `"0x38"` {
				t.Fatalf("read after provider code %d = %s, %v; want public result", code, result, err)
			}
		})
	}
}

func TestMetadataAccountReadUsesPublicFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"context":{"slot":1},"value":null}}`)
	}))
	defer public.Close()
	result, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).
		Call(context.Background(), "getAccountInfo", []any{"11111111111111111111111111111111"})
	if err != nil || string(result) != `{"context":{"slot":1},"value":null}` {
		t.Fatalf("metadata account result=%s, %v; want public read result with truthful null", result, err)
	}
}

func TestReadRPCFallsBackOnMalformedProviderResponse(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "upstream returned HTML instead of JSON")
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
	}))
	defer public.Close()
	result, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).
		Call(context.Background(), "eth_blockNumber", []any{})
	if err != nil || string(result) != `"0x38"` {
		t.Fatalf("malformed primary response=%s, %v; want public block result", result, err)
	}
}

func TestRPCBusinessErrorRedactsReflectedCredential(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"execution reverted for credential reflected-secret-token"}}`)
	}))
	defer primary.Close()
	_, err := jsonrpc.New(primary.URL+"/reflected-secret-token/", primary.Client(), nil, nil).
		Call(context.Background(), "eth_call", []any{})
	var rpcError *jsonrpc.Error
	if !errors.As(err, &rpcError) || rpcError.Code != -32000 {
		t.Fatalf("want original typed business error, got %T", err)
	}
	if strings.Contains(err.Error(), "reflected-secret-token") {
		t.Fatal("provider business error reflected the private RPC credential")
	}
}

// Regression coverage: these semantics already held before the fallback work.
func TestFallbackPreservesBusinessErrorsAndValidNull(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"invalid_params", `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid params"}}`, -32602},
		{"execution_reverted", `{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted"}}`, 3},
		{"skipped_slot", `{"jsonrpc":"2.0","id":1,"error":{"code":-32007,"message":"slot skipped"}}`, -32007},
		{"valid_null", `{"jsonrpc":"2.0","id":1,"result":null}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) }))
			defer primary.Close()
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"must-not-replace-primary-meaning"}`)
			}))
			defer public.Close()
			result, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).Call(context.Background(), "getBlock", []any{1})
			if tc.code == 0 {
				if err != nil || string(result) != "null" {
					t.Fatalf("valid null replaced: result=%s err=%v", result, err)
				}
			} else {
				var rpcError *jsonrpc.Error
				if !errors.As(err, &rpcError) || rpcError.Code != tc.code {
					t.Fatalf("business error replaced: result=%s err=%v", result, err)
				}
			}
		})
	}
}

func TestFallbackPreservesCallerCancellation(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"unexpected"}`)
	}))
	defer provider.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := jsonrpc.New(provider.URL, provider.Client(), nil, nil, provider.URL+"/fallback").Call(ctx, "eth_blockNumber", []any{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v; want context.Canceled", err)
	}
}

func TestFallbackIsSafeForConcurrentReaders(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
	}))
	defer public.Close()
	client := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := client.Call(context.Background(), "eth_blockNumber", []any{})
			if err != nil || string(result) != `"0x38"` {
				t.Errorf("concurrent read=%s, %v", result, err)
			}
		}()
	}
	wg.Wait()
}

func TestColdPrimaryCanFinishWithinCallerBudget(t *testing.T) {
	// The live macOS preflight measured about 6.8s for a cold connection.
	// This explicit wire-delay fixture models that case; it is not a sleep
	// used to synchronize competing goroutines.
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		ready := time.NewTimer(6 * time.Second)
		defer ready.Stop()
		select {
		case <-ready.C:
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x38"}`)
		case <-r.Context().Done():
		}
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer public.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := jsonrpc.New(primary.URL, &http.Client{Timeout: 30 * time.Second}, nil, nil, public.URL).
		Call(ctx, "eth_blockNumber", []any{})
	if err != nil || string(result) != `"0x38"` {
		t.Fatalf("healthy cold primary result=%s, %v; want its result within the caller's 20s budget", result, err)
	}
}

func TestOrdinaryRPCPathDoesNotRewriteBusinessMeaning(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"execution reverted"}}`)
	}))
	defer primary.Close()
	_, err := jsonrpc.New(primary.URL+"/revert", primary.Client(), nil, nil).Call(context.Background(), "eth_call", []any{})
	var rpcError *jsonrpc.Error
	if !errors.As(err, &rpcError) || rpcError.Message != "execution reverted" {
		t.Fatalf("ordinary route altered the provider's business error: %v", err)
	}
}

func TestFallbackPreservesRevertEvenWithProviderInternalCode(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"execution reverted"}}`)
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x01"}`)
	}))
	defer public.Close()
	_, err := jsonrpc.New(primary.URL, primary.Client(), nil, nil, public.URL).Call(context.Background(), "eth_call", []any{})
	var rpcError *jsonrpc.Error
	if !errors.As(err, &rpcError) || rpcError.Code != -32603 || rpcError.Message != "execution reverted" {
		t.Fatalf("provider revert was hidden by a public result: %v", err)
	}
}

func TestMalformedEndpointDoesNotExposeCredentialInErrorsOrMetrics(t *testing.T) {
	const credential = "metric-secret-token"
	_, err := jsonrpc.New("https://private.example/"+credential+"%zz", http.DefaultClient, nil, nil).
		Call(context.Background(), "eth_blockNumber", []any{})
	if err == nil {
		t.Fatal("malformed endpoint must fail")
	}
	if strings.Contains(err.Error(), credential) {
		t.Error("malformed endpoint credential leaked in error")
	}
	families, gatherErr := prometheus.DefaultGatherer.Gather()
	if gatherErr != nil {
		t.Fatal(gatherErr)
	}
	for _, family := range families {
		if family.GetName() != "radar_rpc_requests_total" && family.GetName() != "radar_rpc_duration_seconds" {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if strings.Contains(label.GetValue(), credential) {
					t.Error("malformed endpoint credential leaked in metrics label")
				}
			}
		}
	}
}
