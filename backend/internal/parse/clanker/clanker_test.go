package clanker_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/clanker"
)

type sample struct {
	Name    string
	Tx      json.RawMessage
	Receipt json.RawMessage
}

func samples(t *testing.T, file string) []sample {
	t.Helper()
	z, _ := os.ReadFile("../../evm/testdata/" + file)
	d, _ := zstd.NewReader(nil)
	defer d.Close()
	b, err := d.DecodeAll(z, nil)
	if err != nil {
		t.Fatal(err)
	}
	var all struct{ Samples []sample }
	json.Unmarshal(b, &all)
	return all.Samples
}

func tx(t *testing.T, s sample) evm.Tx {
	t.Helper()
	blk, err := evm.ParseBlock([]byte(`{"block":{"timestamp":"0x0","transactions":[` + string(s.Tx) + `]},"receipts":[` + string(s.Receipt) + `]}`))
	if err != nil {
		t.Fatalf("%s: %v", s.Name, err)
	}
	return blk.Txs[0]
}

func golden(t *testing.T, file string) map[string][]string {
	b, _ := os.ReadFile("testdata/" + file)
	out, name := map[string][]string{}, ""
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if n, ok := strings.CutPrefix(l, "## "); ok {
			name, out[n] = n, nil
			continue
		}
		out[name] = append(out[name], l)
	}
	return out
}

func h(b []byte) string {
	if b == nil {
		return "-"
	}
	return "0x" + hex.EncodeToString(b)
}

func lines(f parse.Facts) []string {
	var out []string
	for _, t := range f.Tokens {
		dec := ""
		if t.Decimals != nil {
			dec = fmt.Sprint(*t.Decimals)
		}
		if t.Created {
			out = append(out, fmt.Sprintf("token %s created name=%s symbol=%s uri=%s dec=%s supply=%s creator=%s %s",
				h(t.Address), t.Name, t.Symbol, t.URI, dec, t.TotalSupply, h(t.Creator), t.LaunchPlatform))
			continue
		}
		out = append(out, fmt.Sprintf("token %s seen dec=%s %s", h(t.Address), dec, t.LaunchPlatform))
	}
	for _, p := range f.Pools {
		state := "seen"
		if p.Created {
			state = "created"
		}
		out = append(out, fmt.Sprintf("pool %s token=%s quote=%s %s %s %s status=%s grad=%s rt=%s rq=%s",
			h(p.ID), h(p.Token), h(p.Quote), p.QuoteSymbol, p.Protocol, state, p.Status, h(p.GraduatedTo), p.ReserveToken, p.ReserveQuote))
	}
	for _, r := range f.Trades {
		out = append(out, fmt.Sprintf("trade %d pool=%s token=%s wallet=%s %s %s tok=%s quote=%s %s price=%s value=%s %s",
			r.Ix, h(r.Pool), h(r.Token), h(r.Wallet), r.Attribution, r.Side, r.TokenAmount, r.QuoteAmount, r.QuoteSymbol, r.PriceNative, r.ValueNative, r.Decoder))
	}
	return out
}

func check(t *testing.T, d interface {
	Decode(context.Context, evm.Tx) (parse.Facts, error)
}, file, expect string) {
	want := golden(t, expect)
	for _, s := range samples(t, file) {
		f, err := d.Decode(context.Background(), tx(t, s))
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		got, exp := lines(f), slices.Clone(want[s.Name])
		slices.Sort(got)
		slices.Sort(exp)
		if !slices.Equal(got, exp) {
			t.Errorf("%s facts:\n got  %s\n want %s", s.Name, strings.Join(got, "\n      "), strings.Join(exp, "\n      "))
		}
		if len(f.Failures) != 0 {
			t.Errorf("%s failures = %+v", s.Name, f.Failures)
		}
	}
}

func TestDecodeMatchesIndependentDecoder(t *testing.T) {
	hooks := []evm.Address{evm.MustAddress("0x34a45c6B61876d739400Bd71228CbcbD4F53E8cC"), evm.MustAddress("0xDd5EeaFf7BD481AD55Db083062b13a3cdf0A68CC"),
		evm.MustAddress("0xd60D6B218116cFd801E28F78d011a203D2b068Cc"), evm.MustAddress("0xb429d62f8f3bFFb98CdB9569533eA23bF0Ba28CC")}
	weth := evm.MustAddress("0x4200000000000000000000000000000000000006")
	d := clanker.New(clanker.Config{Factory: evm.MustAddress("0xE85A59c628F7d27878ACeB4bf3b35733630083a9"),
		PoolManager: evm.MustAddress("0x498581fF718922c3f8e6A244956aF099b2652b2b"), Hooks: hooks, WrappedNative: weth, NativeSymbol: "ETH",
		QuoteSymbols: map[evm.Address]string{{}: "ETH", weth: "WETH", evm.MustAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"): "USDC"}}, noLookup{t})
	check(t, d, "clanker_v4.json.zst", "expect.txt")
}

// noLookup fails the test: the samples are quoted in ETH/WETH only.
type noLookup struct{ t *testing.T }

func (n noLookup) ERC20(_ context.Context, a evm.Address) (chaininfo.ERC20, error) {
	n.t.Errorf("unexpected ERC20 lookup of %s", a.Hex())
	return chaininfo.ERC20{}, nil
}
