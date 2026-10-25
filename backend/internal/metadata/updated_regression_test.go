package metadata

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// These checks deepen regression coverage after the new slices were green.
func TestMetadataCountsCoolingHosts(t *testing.T) {
	st, db := metadataDB(t)
	now := metadataDay.Add(12 * time.Hour)
	const uri = "https://original.example/ipfs/bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"
	var tokens []parse.Token
	for i := 1; i <= 4; i++ {
		tokens = append(tokens, parse.Token{Address: bytes.Repeat([]byte{byte(i)}, 20), URI: uri})
	}
	metadataFacts(t, st, "base", 1, now.Add(-time.Minute), tokens, true)
	hits := map[string]int{}
	f := replayFetcherOptions(t, func(w http.ResponseWriter, r *http.Request) {
		hits[r.Host]++
		if r.Host == "original.example" {
			w.WriteHeader(403)
			return
		}
		w.Write([]byte(`{}`))
	}, Options{IPFSGateways: []string{"https://gateway.example/ipfs/"}})
	before := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("base", "skipped_host"))
	if err := New(st, "base", f, nil, nil).Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM token_metadata WHERE status='ok'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 || hits["original.example"] != 3 || hits["gateway.example"] != 4 {
		t.Errorf("ok=%d requests=%v; want 4 ok, original3/gateway4", count, hits)
	}
	if delta := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("base", "skipped_host")) - before; delta != 1 {
		t.Errorf("skipped_host delta=%g; want 1", delta)
	}
}

func TestFourMemeRawPreflightInspectsOriginalEnvelope(t *testing.T) {
	f := replayFetcherOptions(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"extra":"\ud800","data":{"descr":"be\u0000fore","image":"https://images.example/a.png","shortName":"TOKEN","symbol":"BNB"}}`))
	}, Options{FourMemeAPIURL: "https://four.example/get/v2"})
	doc, err := f.FetchFourMeme(context.Background(), make([]byte, 20))
	if err != nil || len(doc.Raw) != 0 || doc.RawOmitted == "" || doc.Description != "before" || doc.Symbol != "TOKEN" {
		t.Errorf("Four.meme preflight doc=%+v error=%v", doc, err)
	}
}

func TestFetchAlreadyCanceledParentMakesNoRequests(t *testing.T) {
	f := NewFetcher(Options{IPFSGateways: []string{"https://gateway.example/ipfs/"}})
	f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Error("canceled fetch made an HTTP request")
		return nil, context.Canceled
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Fetch(ctx, "ipfs://bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"); err != context.Canceled {
		t.Errorf("error=%v; want parent cancellation", err)
	}
}
