// Package jsonrpc is a minimal JSON-RPC 2.0 client over HTTP with client-side
// rate limits, shared by the Solana and EVM block sources.
package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/meme-radar/radar/internal/metrics"
)

// Error is a JSON-RPC error object returned by the server.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("jsonrpc: error %d: %s", e.Code, e.Message) }

// RateLimitError reports HTTP 429; After is the server's Retry-After.
type RateLimitError struct {
	Method string
	After  time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("jsonrpc: %s rate limited, retry after %s", e.Method, e.After)
}

// RetryAfter returns how long to wait before retrying.
func (e *RateLimitError) RetryAfter() time.Duration { return e.After }

// Client is safe for concurrent use.
type Client struct {
	url       string
	http      *http.Client
	limit     *rate.Limiter
	perMethod map[string]*rate.Limiter
	fallbacks []*Client
}

// New returns a client for url, optionally followed by ordered fallback URLs.
// Read-only requests try each distinct endpoint at most once on provider failure.
// limit (nil means unlimited) and perMethod limits are shared by all attempts.
func New(url string, hc *http.Client, limit *rate.Limiter, perMethod map[string]*rate.Limiter, fallbacks ...string) *Client {
	c := &Client{url: url, http: hc, limit: limit, perMethod: perMethod}
	seen := map[string]bool{url: true}
	for _, endpoint := range fallbacks {
		if endpoint != "" && !seen[endpoint] {
			seen[endpoint] = true
			c.fallbacks = append(c.fallbacks, New(endpoint, hc, limit, perMethod))
		}
	}
	return c
}

// URL returns the configured primary endpoint, which may contain credentials.
// Use only its parsed host for logs and metrics.
func (c *Client) URL() string { return c.url }

// Call returns the raw JSON-RPC result, including a valid null. Provider failures
// may use public fallbacks for the explicitly supported read methods. Caller
// cancellation and business errors retain their meaning. Metrics count attempts
// by endpoint host, excluding authentication paths and query parameters.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	budget := time.Duration(0)
	if len(c.fallbacks) > 0 && readOnlyMethod(method) {
		budget = attemptBudget(ctx, len(c.fallbacks)+1)
	}
	result, err := c.callObserved(ctx, method, params, budget)
	for i, fallback := range c.fallbacks {
		if !readOnlyMethod(method) || ctx.Err() != nil || !fallbackError(err) {
			break
		}
		result, err = fallback.callObserved(ctx, method, params, attemptBudget(ctx, len(c.fallbacks)-i))
	}
	return result, err
}

// A provider cannot consume the entire caller deadline before fallback starts.
func attemptBudget(ctx context.Context, remaining int) time.Duration {
	budget := 15 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, max(time.Nanosecond, time.Until(deadline)/time.Duration(remaining)))
	}
	return budget
}

func fallbackError(err error) bool {
	var response *responseError
	if errors.As(err, &response) {
		return true
	}
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		if rpcErr.Code == 3 || strings.Contains(strings.ToLower(rpcErr.Message), "revert") {
			return false
		}
		return rpcErr.Code == -32603 || rpcErr.Code == -32601 || rpcErr.Code == -32005
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	var limited *RateLimitError
	if errors.As(err, &limited) {
		return true
	}
	var status *statusError
	return errors.As(err, &status) && (status.code == http.StatusUnauthorized || status.code == http.StatusForbidden || status.code >= 500 && status.code < 600)
}

// Only the project's read methods may be replayed on another provider.
func readOnlyMethod(method string) bool {
	switch method {
	case "eth_chainId", "eth_blockNumber", "eth_getBlockByNumber", "eth_getBlockReceipts", "eth_call", "eth_getBalance",
		"getSlot", "getBlock", "getTokenSupply", "getMultipleAccounts", "getGenesisHash", "getAccountInfo":
		return true
	default:
		return false
	}
}

type responseError struct{ err error }

func (e *responseError) Error() string { return e.err.Error() }
func (e *responseError) Unwrap() error { return e.err }

type statusError struct {
	method string
	code   int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("jsonrpc: %s: HTTP %d", e.method, e.code)
}

func (c *Client) callObserved(ctx context.Context, method string, params any, budget time.Duration) (json.RawMessage, error) {
	start := time.Now()
	requestCtx := ctx
	if budget > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}
	res, err := c.call(requestCtx, method, params)
	host := "invalid"
	if u, perr := url.Parse(c.url); perr == nil && u.Host != "" {
		host = u.Host
	}
	outcome := "ok"
	var rl *RateLimitError
	var rpcErr *Error
	switch {
	case errors.As(err, &rl):
		outcome = "rate_limited"
	case errors.As(err, &rpcErr):
		outcome = "rpc_error"
	case err != nil:
		outcome = "error"
	}
	if ctx.Err() == nil {
		metrics.RPCRequests.WithLabelValues(host, method, outcome).Inc()
		metrics.RPCSeconds.WithLabelValues(host, method).Observe(time.Since(start).Seconds())
	}
	return res, err
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	for _, l := range []*rate.Limiter{c.limit, c.perMethod[method]} {
		if l == nil {
			continue
		}
		if err := l.Wait(ctx); err != nil {
			return nil, fmt.Errorf("jsonrpc: %s: %w", method, err)
		}
	}
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", 1, method, params})
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: invalid RPC endpoint", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// net/http wraps transport failures with the full authentication-bearing URL.
		var requestErr *url.Error
		if errors.As(err, &requestErr) {
			err = requestErr.Err
		}
		return nil, fmt.Errorf("jsonrpc: %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, &RateLimitError{Method: method, After: retryAfter(resp.Header.Get("Retry-After"))}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, &responseError{fmt.Errorf("jsonrpc: %s: read: %w", method, err)}
	}
	if len(data) > maxResponse {
		return nil, fmt.Errorf("jsonrpc: %s: response exceeds %d bytes", method, maxResponse)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{method: method, code: resp.StatusCode}
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, &responseError{fmt.Errorf("jsonrpc: %s: decode: %w", method, err)}
	}
	if out.Error != nil {
		out.Error.Message = redactEndpoint(out.Error.Message, c.url)
		return nil, out.Error
	}
	if out.Result == nil {
		return nil, &responseError{fmt.Errorf("jsonrpc: %s: response has neither result nor error", method)}
	}
	return out.Result, nil
}

// maxResponse bounds one response; full Solana blocks are a few MB.
const maxResponse = 64 << 20

// retryAfter parses a Retry-After seconds value, defaulting to 1s.
func retryAfter(v string) time.Duration {
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Second
}

// Providers may reflect an authentication path/query in otherwise useful errors.
func redactEndpoint(message, endpoint string) string {
	message = strings.ReplaceAll(message, endpoint, "[RPC endpoint]")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return message
	}
	for _, part := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if len(part) >= 16 {
			message = strings.ReplaceAll(message, part, "[redacted]")
		}
	}
	for _, values := range parsed.Query() {
		for _, value := range values {
			if value != "" {
				message = strings.ReplaceAll(message, value, "[redacted]")
			}
		}
	}
	if parsed.User != nil {
		if password, ok := parsed.User.Password(); ok && password != "" {
			message = strings.ReplaceAll(message, password, "[redacted]")
		}
	}
	return message
}
