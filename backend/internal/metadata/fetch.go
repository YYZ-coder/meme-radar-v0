package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Resolver resolves untrusted hosts; injectable at the DNS system boundary.
type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// Options configures metadata requests.
type Options struct {
	IPFSGateways                 []string
	FourMemeAPIURL               string
	RPS, PerHostRPS, FourMemeRPS float64
	Resolver                     Resolver
	Now                          func() time.Time
	HostCooldown                 time.Duration
	HostFailures                 int
}

// Document is normalized display metadata. Raw holds only bounded valid JSON.
type Document struct {
	Name        string
	Symbol      string
	ImageURL    string
	Socials     map[string]string
	Description string
	Raw         json.RawMessage
	RawOmitted  string
}

// Fetcher fetches only JSON documents, never image contents.
type Fetcher struct {
	options Options
	client  *http.Client
	limits  *budgets
	health  hostHealth
}

// NewFetcher constructs a fetcher for untrusted token URLs.
func NewFetcher(options Options) *Fetcher {
	options.IPFSGateways = append([]string(nil), options.IPFSGateways...)
	if options.Resolver == nil {
		options.Resolver = net.DefaultResolver
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.HostCooldown <= 0 {
		options.HostCooldown = 10 * time.Minute
	}
	if options.HostFailures <= 0 {
		options.HostFailures = 3
	}
	f := &Fetcher{options: options, limits: newBudgets(options), health: hostHealth{states: make(map[string]hostState)}}
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !publicIP(ip) {
			return &blockedAddressError{host: host}
		}
		return nil
	}}
	tr := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := options.Resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var last error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			if errors.Is(err, ErrBlocked) {
				return nil, &blockedAddressError{host: host}
			}
			last = err
		}
		if last == nil {
			last = fmt.Errorf("metadata: no resolved addresses")
		}
		return nil, last
	}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second}
	f.client = &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return &requestPolicyError{fmt.Errorf("metadata: exceeds 3 redirects")}
		}
		_, err := NormalizeURI(req.URL.String(), f.gateway())
		if err != nil {
			return err
		}
		if f.cooling(req.Context(), req.URL.Hostname()) {
			return ErrHostCooling
		}
		if err := f.limits.wait(req.Context(), req.URL.Hostname()); err != nil {
			return &requestPolicyError{err}
		}
		return nil
	}}
	return f
}

// Fetch fetches a token JSON document under the metadata URL policy.
func (f *Fetcher) Fetch(ctx context.Context, uri string) (Document, error) {
	data, err := f.read(ctx, uri)
	if err != nil {
		return Document{}, err
	}
	return ParseMetadata(data, f.gateway())
}

// HTTPStatusError reports the response code without storing an untrusted body.
type HTTPStatusError struct {
	Code int
	host string
}

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("metadata: HTTP %d", e.Code) }

func (f *Fetcher) read(ctx context.Context, uri string) ([]byte, error) {
	original, err := NormalizeURI(uri, f.gateway())
	if err != nil {
		return nil, err
	}
	candidates := []string{original}
	cid, suffix := ipfsReference(uri)
	if cid != "" {
		for _, gateway := range f.options.IPFSGateways {
			candidate := strings.TrimRight(gateway, "/") + "/" + cid + suffix
			if !slices.Contains(candidates, candidate) {
				candidates = append(candidates, candidate)
			}
		}
	}
	var last error = ErrHostCooling
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := f.readOnce(ctx, candidate)
		if err == nil {
			return data, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrHostCooling) {
			continue
		}
		if !degradable(err) {
			return nil, err
		}
		var status *HTTPStatusError
		var network *transportError
		if errors.As(err, &status) {
			f.hostResult(status.host, true)
		} else if errors.As(err, &network) {
			f.hostResult(network.host, true)
		}
		last = err
	}
	return nil, last
}

// Only network failures and the explicitly retriable statuses may change gateway.
type transportError struct {
	err  error
	host string
}

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

type requestPolicyError struct{ error }

func degradable(err error) bool {
	if errors.Is(err, ErrBlocked) {
		return false
	}
	var policy *requestPolicyError
	if errors.As(err, &policy) {
		return false
	}
	var status *HTTPStatusError
	if errors.As(err, &status) {
		return status.Code == 403 || status.Code == 429 || (status.Code >= 500 && status.Code <= 599)
	}
	var transport *transportError
	return errors.As(err, &transport)
}

func (f *Fetcher) readOnce(ctx context.Context, uri string) ([]byte, error) {
	resp, err := f.get(ctx, uri)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{Code: resp.StatusCode, host: resp.Request.URL.Hostname()}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (512<<10)+1))
	if len(data) > 512<<10 {
		return nil, fmt.Errorf("metadata: response exceeds 512 KiB")
	}
	if err != nil {
		return nil, &transportError{err: err, host: resp.Request.URL.Hostname()}
	}
	f.hostResult(resp.Request.URL.Hostname(), false)
	return data, nil
}

func (f *Fetcher) get(ctx context.Context, uri string) (*http.Response, error) {
	normalized, err := NormalizeURI(uri, f.gateway())
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return nil, err
	}
	if f.cooling(ctx, req.URL.Hostname()) {
		return nil, ErrHostCooling
	}
	if err := f.limits.wait(ctx, req.URL.Hostname()); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := f.client.Do(req)
	if err != nil {
		host := req.URL.Hostname()
		var requestError *url.Error
		if errors.As(err, &requestError) {
			if u, e := url.Parse(requestError.URL); e == nil && u.Hostname() != "" {
				host = u.Hostname()
			}
		}
		return nil, &transportError{err: err, host: host}
	}
	return resp, nil
}

func (f *Fetcher) gateway() string {
	if len(f.options.IPFSGateways) == 0 {
		return ""
	}
	return f.options.IPFSGateways[0]
}

// CloseIdleConnections releases pooled metadata connections when enrich stops.
func (f *Fetcher) CloseIdleConnections() { f.client.CloseIdleConnections() }
