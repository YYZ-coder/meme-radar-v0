package pancake_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/parse/pancake"
)

func lookup(t *testing.T) *chaininfo.RPC {
	t.Helper()
	b, err := os.ReadFile("../../chaininfo/testdata/bsc_calls.json")
	if err != nil {
		t.Fatal(err)
	}
	var rec []struct {
		To, Data string
		Response json.RawMessage
	}
	json.Unmarshal(b, &rec)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Params []json.RawMessage }
		json.NewDecoder(r.Body).Decode(&req)
		var call struct{ To, Data string }
		json.Unmarshal(req.Params[0], &call)
		for _, c := range rec {
			if strings.EqualFold(c.To, call.To) && strings.EqualFold(c.Data, call.Data) {
				w.Write(c.Response)
				return
			}
		}
		t.Errorf("unrecorded call %s %s", call.To, call.Data)
	}))
	t.Cleanup(srv.Close)
	return chaininfo.New(jsonrpc.New(srv.URL, srv.Client(), nil, nil), evm.MustAddress("0xF251F83e40a78868FcfA3FA4599Dad6494E46034"))
}

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
		out = append(out, fmt.Sprintf("token %s seen dec=%s %s", h(t.Address), dec, t.LaunchPlatform))
	}
	for _, p := range f.Pools {
		out = append(out, fmt.Sprintf("pool %s token=%s quote=%s %s %s seen status=%s grad=%s rt=%s rq=%s",
			h(p.ID), h(p.Token), h(p.Quote), p.QuoteSymbol, p.Protocol, p.Status, h(p.GraduatedTo), p.ReserveToken, p.ReserveQuote))
	}
	for _, l := range f.Liquidity {
		out = append(out, fmt.Sprintf("liq %d pool=%s token=%s wallet=%s %s tok=%s quote=%s %s value=%s %s",
			l.Ix, h(l.Pool), h(l.Token), h(l.Wallet), l.Kind, l.TokenAmount, l.QuoteAmount, l.QuoteSymbol, l.ValueNative, l.Decoder))
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

func bscConfig() pancake.Config {
	return pancake.Config{
		V2Factory:      evm.MustAddress("0xcA143Ce32Fe78f1f7019d7d551a6402fC5350c73"),
		V2InitCodeHash: evm.Hash(must(hex.DecodeString("00fb7f630766e6a796048ea87d01acd3068e8ff67d078148a3fa3f4a84f69bd5"))),
		WrappedNative:  evm.MustAddress("0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c"), NativeSymbol: "BNB",
		QuoteSymbols: map[evm.Address]string{evm.MustAddress("0x55d398326f99059fF775485246999027B3197955"): "USDT"},
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestV3AndInfinityMatchIndependentDecoder(t *testing.T) {
	cfg := bscConfig()
	cfg.V3Deployer = evm.MustAddress("0x41ff9AA7e16B8B1a8a8dc4f0eFacd93D02d071c9")
	cfg.V3InitCodeHash = evm.Hash(must(hex.DecodeString("6ce8eb472fa82df5469c6ab6d485f17c3ad13c8cd7af59b3d4a8026c5ce0f7e2")))
	cfg.V3Fees = []uint32{100, 500, 2500, 10000}
	cfg.InfinityCLPoolManager = evm.MustAddress("0xa0FfB9c1CE1Fe56963B0321B32E7A0302114058b")
	l := lookup(t)
	both := multi{pancake.NewV3(cfg, l), pancake.NewInfinityCL(cfg, l)}
	check(t, both, "pancake_v3_cl.json.zst", "v3cl_expect.txt")
}

// multi runs several decoders over a transaction.
type multi []interface {
	Decode(context.Context, evm.Tx) (parse.Facts, error)
}

func (m multi) Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error) {
	var f parse.Facts
	for _, d := range m {
		g, err := d.Decode(ctx, tx)
		if err != nil {
			return f, err
		}
		f.Add(g)
	}
	return f, nil
}

func TestV2MatchesIndependentDecoder(t *testing.T) {
	check(t, pancake.NewV2(bscConfig(), lookup(t)), "pancake_v2.json.zst", "v2_expect.txt")
}

// trackAll treats every non-quote token as tracked, so real Mint/Burn samples
// of non-Four.meme pairs verify the liquidity decoding; metadata is replayed.
type trackAll struct{ *chaininfo.RPC }

var bscQuotes = map[evm.Address]bool{
	evm.MustAddress("0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c"): true,
	evm.MustAddress("0x55d398326f99059fF775485246999027B3197955"): true,
	evm.MustAddress("0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d"): true,
}

func (trackAll) FourMeme(_ context.Context, t evm.Address) (chaininfo.FourMeme, error) {
	return chaininfo.FourMeme{Found: !bscQuotes[t]}, nil
}

func TestLiquidityMatchesIndependentDecoder(t *testing.T) {
	cfg := bscConfig()
	cfg.V3Deployer = evm.MustAddress("0x41ff9AA7e16B8B1a8a8dc4f0eFacd93D02d071c9")
	cfg.V3InitCodeHash = evm.Hash(must(hex.DecodeString("6ce8eb472fa82df5469c6ab6d485f17c3ad13c8cd7af59b3d4a8026c5ce0f7e2")))
	cfg.V3Fees = []uint32{100, 500, 2500, 10000}
	cfg.VanitySuffixes = [][]byte{{}} // every address passes the suffix gate
	l := trackAll{lookup(t)}
	// The samples' swaps are tracked too here; only liquidity lines are compared.
	check(t, liquidityOnly{multi{pancake.NewV2(cfg, l), pancake.NewV3(cfg, l)}}, "pancake_liquidity.json.zst", "liq_expect.txt")
}

type liquidityOnly struct{ d multi }

func (o liquidityOnly) Decode(ctx context.Context, tx evm.Tx) (parse.Facts, error) {
	f, err := o.d.Decode(ctx, tx)
	return parse.Facts{Liquidity: f.Liquidity, Failures: f.Failures}, err
}
