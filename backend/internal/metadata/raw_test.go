package metadata

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetadataOmitsRawThatJSONBRejects(t *testing.T) {
	st, db := metadataDB(t)
	ctx := context.Background()
	now := metadataDay.Add(12 * time.Hour)
	tokens := []parse.Token{
		{Address: bytes.Repeat([]byte{1}, 20), URI: "https://metadata.example/nul"},
		{Address: bytes.Repeat([]byte{2}, 20), URI: "https://metadata.example/surrogate"},
		{Address: bytes.Repeat([]byte{3}, 20), URI: "https://metadata.example/normal"},
	}
	metadataFacts(t, st, "base", 1, now.Add(-time.Minute), tokens, true)
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nul":
			w.Write([]byte(`{"description":"be\u0000fore\n","image":"https://images.example/to\u0000ken.png","twitter":"https://x.com/ac\u0000count"}`))
		case "/surrogate":
			w.Write([]byte(`{"description":"be\ud800fore","image":"https://images.example/token.png","twitter":"account"}`))
		default:
			w.Write([]byte(`{"description":"normal","image":"https://images.example/token.png","twitter":"account"}`))
		}
	})
	before := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("base", "raw_omitted"))
	if err := New(st, "base", f, nil, nil).Step(ctx, now); err != nil {
		t.Errorf("one unrepresentable raw must not abort batch: %v", err)
	}
	for i, token := range tokens {
		var status, description, image, twitter string
		var warning *string
		var rawNull bool
		err := db.QueryRow(ctx, `SELECT status,description,image_url,socials->>'twitter',error,raw IS NULL FROM token_metadata WHERE chain='base' AND address=$1`, token.Address).Scan(&status, &description, &image, &twitter, &warning, &rawNull)
		if err != nil {
			t.Errorf("row %d missing: %v", i, err)
			continue
		}
		wantDescription := []string{"before", "be\ufffdfore", "normal"}[i]
		if status != "ok" || description != wantDescription || image != "https://images.example/token.png" || twitter != "https://x.com/account" {
			t.Errorf("row %d: status=%s desc=%q image=%q twitter=%q", i, status, description, image, twitter)
		}
		if i < 2 {
			if !rawNull || warning == nil || !strings.HasPrefix(*warning, "raw omitted:") {
				t.Errorf("row %d rawNull=%v warning=%v; want omitted raw with warning", i, rawNull, warning)
			}
		} else if rawNull || warning != nil {
			t.Errorf("normal row rawNull=%v warning=%v", rawNull, warning)
		}
	}
	if delta := testutil.ToFloat64(metrics.MetadataFetches.WithLabelValues("base", "raw_omitted")) - before; delta != 2 {
		t.Errorf("raw_omitted delta=%g; want 2 (one per omitted row)", delta)
	}
}

func TestParseMetadataRawPreservation(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		keep       bool
	}{
		{"escaped_nul", `{"unknown":"\u0000"}`, false},
		{"nul_key", `{"\u0000":1}`, false},
		{"high_surrogate", `{"unknown":"\ud800"}`, false},
		{"low_surrogate", `{"unknown":"\udc00"}`, false},
		{"bad_pair", `{"unknown":"\ud800\u0041"}`, false},
		{"valid_pair", `{"unknown":"\ud83d\ude00"}`, true},
		{"literal_backslash", `{"unknown":"\\u0000"}`, true},
		{"escaped_quotes", `{"unknown":"\"\\\ud800"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseMetadata([]byte(tc.data), "")
			if err != nil {
				t.Fatal(err)
			}
			if (len(doc.Raw) > 0) != tc.keep {
				t.Errorf("raw=%q; keep want %v", doc.Raw, tc.keep)
			}
		})
	}
}
