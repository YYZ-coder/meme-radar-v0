package pump_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/pump"
	"github.com/meme-radar/radar/internal/solana"
)

type sample struct {
	Name  string
	Index int
	Tx    json.RawMessage
}

// golden reads testdata/expect.txt: per sample ("## name"), the facts an
// independent Python decoder derived from the official IDLs
// (backend/testdata/expect/expect_pump.py, generic Borsh over the IDL type definitions).
func golden(t *testing.T) map[string][]string {
	t.Helper()
	b, err := os.ReadFile("testdata/expect.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	name := ""
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if n, ok := strings.CutPrefix(l, "## "); ok {
			name = n
			out[name] = nil
			continue
		}
		out[name] = append(out[name], l)
	}
	return out
}

func samples(t *testing.T) []sample {
	t.Helper()
	z, err := os.ReadFile("../../solana/testdata/pump_txs.json.zst")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := zstd.NewReader(nil)
	defer d.Close()
	b, err := d.DecodeAll(z, nil)
	if err != nil {
		t.Fatal(err)
	}
	var s []sample
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func b58(b []byte) string {
	if b == nil {
		return "-"
	}
	return solana.EncodeBase58(b)
}

// lines renders facts in the golden format.
func lines(f parse.Facts) []string {
	var out []string
	for _, t := range f.Tokens {
		dec := ""
		if t.Decimals != nil {
			dec = fmt.Sprint(*t.Decimals)
		}
		platform := t.LaunchPlatform
		if platform == "" {
			platform = "-"
		}
		if t.Created {
			out = append(out, fmt.Sprintf("token %s created name=%s symbol=%s uri=%s dec=%s supply=%s creator=%s %s",
				b58(t.Address), t.Name, t.Symbol, t.URI, dec, t.TotalSupply, b58(t.Creator), platform))
		} else {
			out = append(out, fmt.Sprintf("token %s seen dec=%s %s", b58(t.Address), dec, platform))
		}
	}
	for _, p := range f.Pools {
		state := "seen"
		if p.Created {
			state = "created"
		}
		out = append(out, fmt.Sprintf("pool %s token=%s quote=%s %s %s %s status=%s grad=%s rt=%s rq=%s",
			b58(p.ID), b58(p.Token), b58(p.Quote), p.QuoteSymbol, p.Protocol, state, p.Status, b58(p.GraduatedTo), p.ReserveToken, p.ReserveQuote))
	}
	for _, r := range f.Trades {
		out = append(out, fmt.Sprintf("trade %d pool=%s token=%s wallet=%s %s %s tok=%s quote=%s %s price=%s value=%s %s",
			r.Ix, b58(r.Pool), b58(r.Token), b58(r.Wallet), r.Attribution, r.Side, r.TokenAmount, r.QuoteAmount, r.QuoteSymbol,
			r.PriceNative, r.ValueNative, r.Decoder))
	}
	for _, l := range f.Liquidity {
		out = append(out, fmt.Sprintf("liq %d pool=%s token=%s wallet=%s %s tok=%s quote=%s %s value=%s %s",
			l.Ix, b58(l.Pool), b58(l.Token), b58(l.Wallet), l.Kind, l.TokenAmount, l.QuoteAmount, l.QuoteSymbol, l.ValueNative, l.Decoder))
	}
	return out
}

func TestDecodeMatchesIndependentIDLDecoder(t *testing.T) {
	want := golden(t)
	for _, s := range samples(t) {
		tx, err := solana.ParseTx(s.Index, s.Tx)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		f := pump.Decode(tx)
		got, exp := lines(f), slices.Clone(want[s.Name])
		slices.Sort(got)
		slices.Sort(exp)
		if !slices.Equal(got, exp) {
			t.Errorf("%s facts:\n got  %s\n want %s", s.Name, strings.Join(got, "\n      "), strings.Join(exp, "\n      "))
		}
		if len(f.Failures) != 0 {
			t.Errorf("%s failures = %+v; want none", s.Name, f.Failures)
		}
		for _, r := range f.Trades {
			if solana.EncodeBase58(r.TxHash) != solana.EncodeBase58(tx.Signature[:]) || r.Tx != s.Index || r.Version != pump.Version {
				t.Errorf("%s trade tx fields = hash %x tx %d v%d", s.Name, r.TxHash, r.Tx, r.Version)
			}
		}
	}
}
