package metadata_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/meme-radar/radar/internal/metadata"
	"github.com/meme-radar/radar/internal/solana"
)

func TestDecodeMetaplexMetadata(t *testing.T) {
	raw, err := os.ReadFile("testdata/metaplex_accounts.json")
	if err != nil {
		t.Fatal(err)
	}
	var recordings struct {
		Samples []struct {
			Mint, PDA string
			Response  struct {
				Result struct{ Value struct{ Data []string } }
			}
		}
	}
	if err := json.Unmarshal(raw, &recordings); err != nil {
		t.Fatal(err)
	}
	// Literal expectations from python3 testdata/expect/expect_metaplex.py.
	wants := []struct{ name, symbol, uri, pda string }{
		{"USD Coin", "USDC", "", "5x38Kp4hvdomTCnCrAny4UtMUt5rQBdB6px2K1Ui45Wq"},
		{"Jupiter", "JUP", "https://static.jup.ag/jup/metadata.json", "5pddDLA4taryBwRYGdtKmS9qkwssXD8vHECeNbCZnwUy"},
	}
	if len(recordings.Samples) != 2 {
		t.Fatal("need two real accounts")
	}
	for i, sample := range recordings.Samples {
		data, err := base64.StdEncoding.DecodeString(sample.Response.Result.Value.Data[0])
		if err != nil {
			t.Fatal(err)
		}
		got, err := metadata.DecodeMetaplexMetadata(data)
		if err != nil {
			t.Errorf("decode %s: %v", sample.Mint, err)
		} else if got.Name != wants[i].name || got.Symbol != wants[i].symbol || got.URI != wants[i].uri || got.Mint.String() != sample.Mint {
			t.Errorf("decoded=%+v; want %+v mint=%s", got, wants[i], sample.Mint)
		}
		mint, err := solana.ParseKey(sample.Mint)
		if err != nil {
			t.Fatal(err)
		}
		pda, err := metadata.MetaplexPDA(mint)
		if err != nil || pda.String() != wants[i].pda {
			t.Errorf("PDA=%s err=%v; want %s", pda, err, wants[i].pda)
		}
		for _, bad := range [][]byte{nil, data[:1], data[:64], append([]byte{99}, data[1:]...)} {
			if _, err := metadata.DecodeMetaplexMetadata(bad); err == nil {
				t.Errorf("accepted invalid account prefix of %d bytes", len(bad))
			}
		}
	}
}
