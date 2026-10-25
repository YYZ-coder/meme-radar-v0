package metadata

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestMetadataHTTPBudgets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options Options
		second  string
		blocked bool
	}{
		{"global_shared_across_hosts", Options{RPS: 1}, "https://two.example/a", true},
		{"same_host", Options{PerHostRPS: 1}, "https://one.example/b", true},
		{"different_host", Options{PerHostRPS: 1}, "https://two.example/a", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			f := replayFetcherOptions(t, func(w http.ResponseWriter, r *http.Request) { hits++; w.Write([]byte(`{}`)) }, tc.options)
			if _, err := f.Fetch(context.Background(), "https://one.example/a"); err != nil {
				t.Fatal(err)
			}
			// At 1 RPS a second request cannot fit a 50ms deadline. The limiter
			// must reject it before any HTTP operation; other hosts have a new budget.
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err := f.Fetch(ctx, tc.second)
			if (err != nil) != tc.blocked {
				t.Errorf("second request err=%v; blocked want %v", err, tc.blocked)
			}
			want := 2
			if tc.blocked {
				want = 1
			}
			if hits != want {
				t.Errorf("HTTP requests=%d; want %d", hits, want)
			}
		})
	}
	t.Run("fourmeme_dedicated_budget", func(t *testing.T) {
		hits := 0
		f := replayFetcherOptions(t, func(w http.ResponseWriter, r *http.Request) { hits++; w.Write([]byte(`{"code":0}`)) }, Options{FourMemeRPS: 1, FourMemeAPIURL: "https://four.example/get/v2"})
		address := make([]byte, 20)
		if _, err := f.FetchFourMeme(context.Background(), address); err != ErrNoSource {
			t.Fatalf("first response err=%v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := f.FetchFourMeme(ctx, address); err == nil || err == ErrNoSource {
			t.Errorf("Four.meme exceeded its dedicated budget: %v", err)
		}
		if hits != 1 {
			t.Errorf("Four.meme requests=%d; want 1", hits)
		}
	})
	t.Run("gateways_have_separate_budgets", func(t *testing.T) {
		f := replayFetcherOptions(t, func(w http.ResponseWriter, r *http.Request) {
			// Original hostname is retained in the HTTP Host header by the replay.
			if r.Host == "second.example" {
				w.Write([]byte(`{}`))
				return
			}
			w.WriteHeader(403)
		}, Options{PerHostRPS: 1, IPFSGateways: []string{"https://first.example/ipfs/", "https://second.example/ipfs/"}})
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if _, err := f.Fetch(ctx, "https://original.example/ipfs/QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr"); err != nil {
			t.Errorf("separate gateway host budgets blocked fallback: %v", err)
		}
	})
}
