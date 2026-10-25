package metadata

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type resolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (r resolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return r(ctx, host)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (r roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return r(req) }

func TestFetchRejectsPrivateAddresses(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{}`)) }))
	defer srv.Close()
	f := NewFetcher(Options{IPFSGateways: []string{"https://gateway.pinata.cloud/ipfs/"}})
	if _, err := f.Fetch(ctx, srv.URL); !errors.Is(err, ErrBlocked) {
		t.Errorf("loopback request = %v; want ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Fatal("private server received metadata request")
	}
	// The DNS answer, rather than the spelling of the hostname, controls access.
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1", "fe80::1", "224.0.0.1", "0.0.0.0", "::ffff:127.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			f := NewFetcher(Options{Resolver: resolverFunc(func(context.Context, string) ([]net.IPAddr, error) { return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil })})
			if _, err := f.Fetch(ctx, "http://metadata.test/a"); !errors.Is(err, ErrBlocked) {
				t.Errorf("DNS %s = %v; want ErrBlocked", ip, err)
			}
		})
	}
	t.Run("public_redirect_to_private", func(t *testing.T) {
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, srv.URL, http.StatusFound) }))
		defer redirect.Close()
		f := NewFetcher(Options{})
		original := f.client.Transport
		// Replay an external public server's 302 at the HTTP system boundary.
		// Redirect processing still belongs to the production client.
		f.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "public.example" {
				copy := req.Clone(req.Context())
				u := *req.URL
				copy.URL = &u
				copy.URL.Scheme = "http"
				copy.URL.Host = strings.TrimPrefix(redirect.URL, "http://")
				return http.DefaultTransport.RoundTrip(copy)
			}
			if original != nil {
				return original.RoundTrip(req)
			}
			return http.DefaultTransport.RoundTrip(req)
		})
		if _, err := f.Fetch(ctx, "http://public.example/redirect"); !errors.Is(err, ErrBlocked) {
			t.Errorf("redirect = %v; want ErrBlocked", err)
		}
		if hits.Load() != 0 {
			t.Fatal("redirect reached the private destination")
		}
	})
}
