package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// replayFetcher replays an external HTTP service locally; URL/redirect
// validation, response bounds and JSON parsing use the production fetcher.
func replayFetcher(t *testing.T, handler http.HandlerFunc) *Fetcher {
	return replayFetcherOptions(t, handler, Options{IPFSGateways: []string{"https://gateway.pinata.cloud/ipfs/"}})
}

func replayFetcherOptions(t *testing.T, handler http.HandlerFunc, options Options) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	f := NewFetcher(options)
	f.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		cp := req.Clone(req.Context())
		u := *req.URL
		cp.URL = &u
		cp.URL.Scheme = "http"
		cp.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		resp, err := http.DefaultTransport.RoundTrip(cp)
		if resp != nil {
			resp.Request = req
		}
		return resp, err
	})
	return f
}

func TestFetchPumpMetadata(t *testing.T) {
	t.Run("recorded_pump", func(t *testing.T) {
		// Hand-read from the SHA-256-verified recordings, not derived by the parser.
		for _, tc := range []struct {
			file, name, symbol, image, description string
			socials                                map[string]string
		}{
			{"pump_1.json", "Politburo Or Parrot", "POP", "https://gmgn.ai/external-res-va/c9d8812b35aeeecfcdccb62f6abf7f83_v2.webp", "", map[string]string{"twitter": "https://x.com/toly/status/2113737743973192015"}},
			{"pump_2.json", "Bitcona", "BITC", "https://ipfs.io/ipfs/bafybeic6koqmyfif55p2jzu3gz53fusu52eerepmlhpnqzcncsb7iywlxa", "Bitcona ($BITC) is a community-driven meme coin concept on Solana, inspired by the golden serpent — a symbol of resilience, transformation, and crypto culture. Built for the community, powered by imagination. Join the serpent movement. Meme coin, high risk, no guaranteed returns.", map[string]string{}},
			{"pump_3.json", "smol kimchi", "smolchi", "https://ipfs.io/ipfs/bafkreigzv5vh72ubgrwbw2g4vwxjzpjr6rxshtk56td4rtc45em6nhe7za", "", map[string]string{"twitter": "https://x.com/smolkimchi", "website": "https://smolkimchi.fun"}},
		} {
			t.Run(tc.file, func(t *testing.T) {
				body, err := os.ReadFile("testdata/" + tc.file)
				if err != nil {
					t.Fatal(err)
				}
				f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
				got, err := f.Fetch(context.Background(), "https://metadata.example/token")
				if err != nil {
					t.Fatal(err)
				}
				if got.Name != tc.name || got.Symbol != tc.symbol || got.ImageURL != tc.image || got.Description != tc.description || !reflect.DeepEqual(got.Socials, tc.socials) {
					t.Fatalf("document=%+v; want name=%q symbol=%q image=%q description=%q socials=%v", got, tc.name, tc.symbol, tc.image, tc.description, tc.socials)
				}
				if string(got.Raw) != string(body) {
					t.Fatal("raw JSON was not retained exactly")
				}
			})
		}
	})

	t.Run("body_limits_and_json", func(t *testing.T) {
		for _, tc := range []struct {
			name, body  string
			failed, raw bool
		}{
			{"object", `{"custom":"preserved"}`, false, true},
			{"raw_over_64KiB", `{"custom":"` + strings.Repeat("x", 64<<10) + `"}`, false, false},
			{"body_over_512KiB", `{"custom":"` + strings.Repeat("x", 512<<10) + `"}`, true, false},
			{"non_json", "<!doctype html>", true, false},
			{"not_object", "[1,2]", true, false},
			{"null", "null", true, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.body)) })
				doc, err := f.Fetch(context.Background(), "https://metadata.example/token")
				if (err != nil) != tc.failed {
					t.Fatalf("Fetch error=%v; failed want %v", err, tc.failed)
				}
				if !tc.failed && (len(doc.Raw) > 0) != tc.raw {
					t.Fatalf("raw present=%v; want %v", len(doc.Raw) > 0, tc.raw)
				}
			})
		}
	})
	t.Run("timeout", func(t *testing.T) {
		f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if _, err := f.Fetch(ctx, "https://metadata.example/slow"); err == nil {
			t.Fatal("slow response accepted; want timeout")
		}
	})
}
