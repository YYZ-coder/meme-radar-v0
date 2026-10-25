// Package jsonrpc is a minimal JSON-RPC 2.0 client over HTTP with client-side
// rate limits, shared by the Solana and EVM block sources.
package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/meme-radar/radar/internal/metrics"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"
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
}

// New returns a client for url. limit applies to every call (nil means
// unlimited); perMethod adds tighter limits for specific methods.
func New(url string, hc *http.Client, limit *rate.Limiter, perMethod map[string]*rate.Limiter) *Client {
	return &Client{url: url, http: hc, limit: limit, perMethod: perMethod}
}

// Call invokes method and returns the raw result, which is "null" when the
// server returned a null result.
// URL returns the endpoint.
func (c *Client) URL() string { return c.url }

// Call sends one request and reports its outcome and duration
// (radar_rpc_requests_total, radar_rpc_duration_seconds) by endpoint host.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	start := time.Now()
	res, err := c.call(ctx, method, params)
	host := c.url
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
		return nil, fmt.Errorf("jsonrpc: %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, &RateLimitError{Method: method, After: retryAfter(resp.Header.Get("Retry-After"))}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: read: %w", method, err)
	}
	if len(data) > maxResponse {
		return nil, fmt.Errorf("jsonrpc: %s: response exceeds %d bytes", method, maxResponse)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jsonrpc: %s: HTTP %d: %.200s", method, resp.StatusCode, data)
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: decode: %w", method, err)
	}
	if out.Error != nil {
		return nil, out.Error
	}
	if out.Result == nil {
		return nil, fmt.Errorf("jsonrpc: %s: response has neither result nor error", method)
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
