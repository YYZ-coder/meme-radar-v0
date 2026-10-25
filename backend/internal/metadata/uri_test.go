package metadata_test

import (
	"testing"

	"github.com/meme-radar/radar/internal/metadata"
)

// Expectations are literal transformations from H6 §2-5; no network involved.
func TestNormalizeURI(t *testing.T) {
	const cid = "QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr"
	const raw = "bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"
	for _, tc := range []struct{ in, want string }{
		{"ipfs://" + cid, "https://gateway.pinata.cloud/ipfs/" + cid},
		{"ipfs://ipfs/" + cid + "/metadata.json", "https://gateway.pinata.cloud/ipfs/" + cid + "/metadata.json"},
		{"https://ipfs.io/ipfs/" + cid, "https://ipfs.io/ipfs/" + cid},
		{raw, "https://gateway.pinata.cloud/ipfs/" + raw},
		{"http://localhost:3000/a", ""}, {"http://LOCALHOST./a", ""},
		{"file:///etc/passwd", ""}, {"javascript:alert(1)", ""},
		{"https:///nohost", ""}, {"https://user:pass@example.org/a", ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := metadata.NormalizeURI(tc.in, "https://gateway.pinata.cloud/ipfs/")
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted unsafe URI %q as %q", tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("NormalizeURI = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	got, err := metadata.NormalizeURI("ipfs://"+cid, "https://gateway.example/ipfs/")
	if err != nil || got != "https://gateway.example/ipfs/"+cid {
		t.Fatalf("custom gateway: %q %v", got, err)
	}
}

// CID literals were read from the stored on-chain URIs; path/query are not CID bytes.
func TestNormalizeURIExtractsCID(t *testing.T) {
	const cid = "bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"
	for _, uri := range []string{
		cid, "ipfs://" + cid, "ipfs://ipfs/" + cid + "/metadata.json",
		"https://ipfs.io/ipfs/" + cid,
		"https://project.mypinata.cloud/ipfs/" + cid + "/metadata.json?download=1",
		"https://" + cid + ".ipfs.dweb.link/metadata.json",
	} {
		if got := metadata.ExtractCID(uri); got != cid {
			t.Errorf("ExtractCID(%q)=%q; want %q", uri, got, cid)
		}
	}
	if got := metadata.ExtractCID("https://example.org/no-cid"); got != "" {
		t.Errorf("non-IPFS URI yielded CID %q", got)
	}
}
