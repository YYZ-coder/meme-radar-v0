package metadata_test

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/meme-radar/radar/internal/metadata"
)

// FuzzParseMetadata is robustness regression coverage, not retrospective TDD.
func FuzzParseMetadata(f *testing.F) {
	for _, file := range []string{"pump_1.json", "pump_2.json", "pump_3.json"} {
		raw, err := os.ReadFile("testdata/" + file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte(`{"extensions":{"twitter":"@a"},"links":[{"type":"website","url":"javascript:bad"}]}`))
	f.Add([]byte(`{"description":"\u0000\ud800","image":"https://example.org/a\u0000b","twitter":"@a\u0000b"}`))
	f.Add([]byte(`{"description":"\ud83d\ude00","literal":"\\u0000"}`))
	f.Add([]byte(`null`))
	f.Add([]byte{0xff, 0, 1})
	f.Fuzz(func(t *testing.T, raw []byte) {
		doc, err := metadata.ParseMetadata(raw, "https://gateway.pinata.cloud/ipfs/")
		if err != nil {
			return
		}
		if utf8.RuneCountInString(doc.Name) > 128 || utf8.RuneCountInString(doc.Symbol) > 32 || utf8.RuneCountInString(doc.Description) > 1000 {
			t.Fatal("text length exceeds frozen limits")
		}
		checkURL := func(value string) {
			if value == "" {
				return
			}
			if utf8.RuneCountInString(value) > 256 {
				t.Fatal("URL exceeds 256 characters")
			}
			u, err := url.Parse(value)
			if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
				t.Fatalf("non-http(s) display URL: %q", value)
			}
		}
		for _, value := range []string{doc.Name, doc.Symbol, doc.Description, doc.ImageURL} {
			if strings.IndexFunc(value, unicode.IsControl) >= 0 {
				t.Fatalf("control character in display field %q", value)
			}
		}
		if doc.RawOmitted != "" && len(doc.Raw) > 0 {
			t.Fatal("omitted raw was retained")
		}
		checkURL(doc.ImageURL)
		for key, value := range doc.Socials {
			if key != "twitter" && key != "telegram" && key != "website" && key != "github" {
				t.Fatalf("undeclared social field %q", key)
			}
			if strings.IndexFunc(value, unicode.IsControl) >= 0 {
				t.Fatal("control character in social URL")
			}
			checkURL(value)
		}
		if len(doc.Raw) > 64<<10 || (len(doc.Raw) > 0 && !json.Valid(doc.Raw)) {
			t.Fatal("raw violates JSON/size limits")
		}
	})
}
