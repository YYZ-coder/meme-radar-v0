package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestFetchFallsBackAcrossGateways(t *testing.T) {
	const cid = "bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"
	const path = "/ipfs/" + cid + "/metadata.json?download=1"
	for _, tc := range []struct {
		name, uri string
		codes     []int
		want      []string
		failed    bool
	}{
		{"403_then_429_then_ok", "https://original.example" + path, []int{403, 429, 200}, []string{"original.example", "first.example", "second.example"}, false},
		{"all_403", "https://original.example" + path, []int{403, 403, 403}, []string{"original.example", "first.example", "second.example"}, true},
		{"404_no_fallback", "https://original.example" + path, []int{404, 200, 200}, []string{"original.example"}, true},
		{"5xx_fallback", "https://original.example" + path, []int{503, 200, 200}, []string{"original.example", "first.example"}, false},
		{"bare_cid", cid, []int{200, 429, 200}, []string{"first.example", "second.example"}, false},
		{"ipfs_uri", "ipfs://" + cid, []int{200, 200, 200}, []string{"first.example"}, false},
		{"original_is_first", "https://first.example" + path, []int{200, 403, 200}, []string{"first.example", "second.example"}, false},
		{"no_cid", "https://original.example/token", []int{403, 200, 200}, []string{"original.example"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			servers := map[string]*httptest.Server{}
			for i, host := range []string{"original.example", "first.example", "second.example"} {
				code := tc.codes[i]
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code); w.Write([]byte(`{}`)) }))
				defer srv.Close()
				servers[host] = srv
			}
			f := NewFetcher(Options{IPFSGateways: []string{"https://first.example/ipfs/", "https://second.example/ipfs/"}})
			f.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				seen = append(seen, req.URL.Host)
				if ExtractCID(req.URL.String()) != cid && tc.name != "no_cid" {
					t.Errorf("request lost CID: %s", req.URL)
				}
				if req.URL.Host != "original.example" && tc.uri != cid && tc.name != "ipfs_uri" && req.URL.RequestURI() != path {
					t.Errorf("fallback lost object path/query: %s", req.URL)
				}
				cp := req.Clone(req.Context())
				u := *req.URL
				cp.URL = &u
				target, err := url.Parse(servers[req.URL.Host].URL)
				if err != nil {
					return nil, err
				}
				cp.URL.Scheme = target.Scheme
				cp.URL.Host = target.Host
				resp, err := http.DefaultTransport.RoundTrip(cp)
				if resp != nil {
					resp.Request = req
				}
				return resp, err
			})
			_, err := f.Fetch(context.Background(), tc.uri)
			if (err != nil) != tc.failed {
				t.Errorf("Fetch error=%v; failed want %v", err, tc.failed)
			}
			if !reflect.DeepEqual(seen, tc.want) {
				t.Errorf("request order=%v; want %v", seen, tc.want)
			}
		})
	}
	t.Run("fallback_private_gateway_blocked", func(t *testing.T) {
		f := NewFetcher(Options{IPFSGateways: []string{"http://127.0.0.1/ipfs/"}})
		f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
		})
		if _, err := f.Fetch(context.Background(), "https://original.example"+path); !errors.Is(err, ErrBlocked) {
			t.Errorf("private fallback=%v; want ErrBlocked", err)
		}
	})
}
