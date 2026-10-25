package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/store"
)

// These checks extend regression coverage after the behavior slices were green.
func TestMetadataBatchIsIsolatedAndPendingWorkIsReplayable(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	now := metadataDay.Add(12 * time.Hour)
	a, b := bytes.Repeat([]byte{1}, 20), bytes.Repeat([]byte{2}, 20)
	metadataFacts(t, st, "base", 1, now.Add(-time.Minute), []parse.Token{{Address: a, URI: "https://metadata.example/a"}, {Address: b, URI: "https://metadata.example/b"}}, true)
	writes := []store.MetadataWrite{{Address: a, Status: "ok", Attempts: 1}, {Address: b, Status: "invalid", Attempts: 1}}
	if err := st.SaveMetadata(ctx, "base", writes, now); err == nil {
		t.Fatal("invalid frozen status must be reported")
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM token_metadata`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("usable row must survive sibling failure: rows=%d; want 1", count)
	}
	writes[1].Status = "pending"
	writes[1].Attempts = 0
	for i := 0; i < 2; i++ {
		if err := st.SaveMetadata(ctx, "base", writes, now); err != nil {
			t.Fatal(err)
		}
	}
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/b" {
			t.Errorf("terminal ok row fetched again: %s", r.URL)
		}
		w.Write([]byte(`{}`))
	})
	worker := New(st, "base", f, nil, nil)
	if err := worker.Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := worker.Step(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM token_metadata WHERE status='ok' AND attempts=1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("replayed rows=%d; want 2 unique terminal rows with one attempt", count)
	}
}

func TestFourMemeFailuresBackOffAndDisabledSourceStaysPending(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	now := metadataDay.Add(12 * time.Hour)
	a, b, c := bytes.Repeat([]byte{1}, 20), bytes.Repeat([]byte{2}, 20), bytes.Repeat([]byte{3}, 20)
	metadataFacts(t, st, "bsc", 1, now.Add(-time.Minute), []parse.Token{{Address: a, LaunchPlatform: "four_meme"}, {Address: b, LaunchPlatform: "four_meme"}, {Address: c, LaunchPlatform: "four_meme"}}, true)
	if err := st.SaveMetadata(ctx, "bsc", []store.MetadataWrite{{Address: c, Status: "pending", Attempts: 0}}, now); err != nil {
		t.Fatal(err)
	}
	hits := 0
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("address")[2:4] == "01" {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"code":9,"msg":"unavailable"}`))
	})
	worker := New(st, "bsc", f, nil, nil)
	if err := worker.Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	if hits != 0 {
		t.Fatal("disabled API was called")
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM token_metadata WHERE status='pending' AND attempts=0`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("disabled source should preserve pending work")
	}
	f.options.FourMemeAPIURL = "https://four.example/get/v2"
	if err := worker.Step(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM token_metadata WHERE status='failed' AND attempts=1 AND next_attempt_at=$1`, now.Add(10*time.Minute)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 || hits != 3 {
		t.Errorf("Four.meme failures=%d requests=%d; want all 3 with +10m backoff", count, hits)
	}
}

func TestFetchRedirectLimitAndDNSAreEnforced(t *testing.T) {
	for _, redirects := range []int{3, 4} {
		t.Run(fmt.Sprintf("%d_redirects", redirects), func(t *testing.T) {
			f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
				n, err := strconv.Atoi(r.URL.Path[1:])
				if err != nil {
					t.Error(err)
				}
				if n < redirects {
					http.Redirect(w, r, fmt.Sprintf("/%d", n+1), http.StatusFound)
					return
				}
				w.Write([]byte(`{}`))
			})
			_, err := f.Fetch(context.Background(), "https://public.example/0")
			if (err != nil) != (redirects > 3) {
				t.Errorf("%d redirects: error=%v", redirects, err)
			}
		})
	}
	t.Run("redirect_dns_private", func(t *testing.T) {
		f := NewFetcher(Options{Resolver: resolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		})})
		original := f.client.Transport
		f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "public.example" {
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://redirected.test/a"}}, Body: http.NoBody, Request: r}, nil
			}
			return original.RoundTrip(r)
		})
		if _, err := f.Fetch(context.Background(), "http://public.example/a"); !errors.Is(err, ErrBlocked) {
			t.Errorf("redirect private DNS error=%v; want ErrBlocked", err)
		}
	})
}
