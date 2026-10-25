package metadata

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestFourMemeMetadata(t *testing.T) {
	sample, err := os.ReadFile("testdata/fourmeme_token_get_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	address, err := hex.DecodeString("5d709925a8671dbd97d73d0b1f3053ec66dfffff")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                       string
		body                       []byte
		status                     int
		disabled, noSource, failed bool
	}{
		{"recorded", sample, 200, false, false, false},
		{"missing_data", []byte(`{"code":0,"msg":"success"}`), 200, false, true, false},
		{"api_error", []byte(`{"code":7,"msg":"error"}`), 200, false, false, true},
		{"http_error", sample, 503, false, false, true},
		{"disabled", sample, 200, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.URL.Query().Get("address") != "0x5d709925a8671dbd97d73d0b1f3053ec66dfffff" {
					t.Errorf("bad API address query: %s", r.URL)
				}
				if !strings.Contains(r.UserAgent(), "Mozilla/") {
					t.Errorf("missing browser User-Agent: %q", r.UserAgent())
				}
				w.WriteHeader(tc.status)
				w.Write(tc.body)
			})
			if !tc.disabled {
				f.options.FourMemeAPIURL = "https://four.example/get/v2"
			}
			doc, err := f.FetchFourMeme(context.Background(), address)
			if tc.disabled {
				if !errors.Is(err, ErrDisabled) || hits != 0 {
					t.Errorf("disabled err=%v hits=%d", err, hits)
				}
				return
			}
			if tc.noSource {
				if !errors.Is(err, ErrNoSource) {
					t.Errorf("missing data err=%v; want ErrNoSource", err)
				}
				return
			}
			if tc.failed {
				if err == nil {
					t.Fatal("expected API/HTTP failure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Literal expected fields hand-read in the recorded API envelope (D155).
			if doc.ImageURL != "https://static.four.meme/market/10234cg8Yq3jRu.png" || doc.Socials["website"] != "https://onevi.app" || doc.Socials["twitter"] != "https://x.com/useonevi" || doc.Description != "Onevi" || doc.Symbol != "ONEVI" || doc.Name != "Onevi" {
				t.Errorf("incorrect Four.meme fields: image=%q socials=%v description=%q symbol=%q name=%q", doc.ImageURL, doc.Socials, doc.Description, doc.Symbol, doc.Name)
			}
			if string(doc.Raw) != string(sample) {
				t.Fatal("raw must retain original envelope, including quote symbol BNB")
			}
		})
	}
}
