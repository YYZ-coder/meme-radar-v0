package metadata

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFetchFallsBackAcrossGatewaysTransport(t *testing.T) {
	const object = "/ipfs/bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"
	for _, mode := range []string{"timeout", "refused", "reset", "dns", "tls", "parent_canceled", "invalid_json", "oversize", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "timeout":
					<-r.Context().Done()
				case "invalid_json":
					io.WriteString(w, "not JSON")
				case "oversize":
					io.WriteString(w, strings.Repeat(" ", (512<<10)+1))
				default:
					io.WriteString(w, `{}`)
				}
			}))
			defer origin.Close()
			if mode == "refused" {
				origin.Close()
			}
			tlsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{}`) }))
			defer tlsOrigin.Close()
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"description":"gateway success"}`) }))
			defer gateway.Close()
			f := NewFetcher(Options{IPFSGateways: []string{"https://first.example/ipfs/", "https://second.example/ipfs/"}})
			f.client.Timeout = 50 * time.Millisecond // Same per-attempt deadline mechanism; no 10-second test sleep.
			var seen []string
			f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				seen = append(seen, r.URL.Hostname())
				target := gateway.URL
				if r.URL.Hostname() == "original.example" {
					target = origin.URL
					switch mode {
					case "reset":
						return nil, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
					case "dns":
						return nil, &net.DNSError{Name: "original.example", Err: "no such host", IsNotFound: true}
					case "tls":
						target = tlsOrigin.URL
					case "parent_canceled":
						cancel()
						return nil, ctx.Err()
					case "blocked":
						return nil, &blockedAddressError{host: "original.example"}
					}
				}
				cp := r.Clone(r.Context())
				u := *r.URL
				cp.URL = &u
				dst, _ := url.Parse(target)
				cp.URL.Scheme = dst.Scheme
				cp.URL.Host = dst.Host
				resp, err := http.DefaultTransport.RoundTrip(cp)
				if resp != nil {
					resp.Request = r
				}
				return resp, err
			})
			doc, err := f.Fetch(ctx, "https://original.example"+object)
			stop := mode == "parent_canceled" || mode == "invalid_json" || mode == "oversize" || mode == "blocked"
			want := []string{"original.example", "first.example"}
			if stop {
				want = want[:1]
			}
			if !reflect.DeepEqual(seen, want) {
				t.Errorf("request order=%v; want %v", seen, want)
			}
			if stop {
				if err == nil {
					t.Fatal("terminal failure accepted")
				}
				if mode == "parent_canceled" && !errors.Is(err, context.Canceled) {
					t.Errorf("parent cancel lost: %v", err)
				}
				if mode == "blocked" && !errors.Is(err, ErrBlocked) {
					t.Errorf("SSRF sentinel lost: %v", err)
				}
			} else if err != nil || doc.Description != "gateway success" {
				t.Errorf("fallback doc=%+v error=%v; want gateway success", doc, err)
			}
		})
	}
}

// A body can cross the bound in the same Read that reports a reset. The size
// bound must win, since the CID contents are already known to be unusable.
type resetAtEndBody struct{ data []byte }

func (b *resetAtEndBody) Read(p []byte) (int, error) {
	n := copy(p, b.data)
	b.data = b.data[n:]
	if len(b.data) == 0 {
		return n, syscall.ECONNRESET
	}
	return n, nil
}
func (*resetAtEndBody) Close() error { return nil }
func TestFetchOversizeStopsEvenWhenBodyReadAlsoFails(t *testing.T) {
	f := NewFetcher(Options{IPFSGateways: []string{"https://gateway.example/ipfs/"}})
	var seen []string
	f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.URL.Hostname())
		body := io.ReadCloser(io.NopCloser(strings.NewReader(`{}`)))
		if r.URL.Hostname() == "original.example" {
			body = &resetAtEndBody{data: []byte(strings.Repeat(" ", (512<<10)+1))}
		}
		return &http.Response{StatusCode: 200, Body: body, Request: r, Header: http.Header{}}, nil
	})
	_, err := f.Fetch(context.Background(), "https://original.example/ipfs/bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm")
	if err == nil || !strings.Contains(err.Error(), "512 KiB") || !reflect.DeepEqual(seen, []string{"original.example"}) {
		t.Errorf("oversize err=%v requests=%v; must stop after original", err, seen)
	}
}
