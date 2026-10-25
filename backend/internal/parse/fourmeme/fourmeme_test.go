package fourmeme_test

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
	"github.com/meme-radar/radar/internal/parse/fourmeme"
)

// lookup replays eth_call responses recorded from BSC (../../chaininfo/testdata).
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

// samples are real BSC transactions with receipts (testdata in ../../evm).
type sample struct {
	Name    string
	Tx      json.RawMessage
	Receipt json.RawMessage
}

func samples(t *testing.T) []sample {
	t.Helper()
	z, _ := os.ReadFile("../../evm/testdata/four_meme.json.zst")
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

// tx builds the one-transaction block payload ingest would store.
func tx(t *testing.T, s sample) evm.Tx {
	t.Helper()
	var txi struct {
		TransactionIndex string `json:"transactionIndex"`
	}
	json.Unmarshal(s.Tx, &txi)
	payload := `{"block":{"timestamp":"0x0","transactions":[` + string(s.Tx) + `]},"receipts":[` + string(s.Receipt) + `]}`
	blk, err := evm.ParseBlock([]byte(payload))
	if err != nil {
		t.Fatalf("%s: %v", s.Name, err)
	}
	return blk.Txs[0]
}

func golden(t *testing.T) map[string][]string {
	b, _ := os.ReadFile("testdata/expect.txt")
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
		} else {
			out = append(out, fmt.Sprintf("token %s seen dec=%s %s", h(t.Address), dec, t.LaunchPlatform))
		}
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

func TestDecodeMatchesIndependentABIDecoder(t *testing.T) {
	d := fourmeme.New(fourmeme.Config{
		ManagerV1:     evm.MustAddress("0xEC4549caDcE5DA21Df6E6422d448034B5233bFbC"),
		ManagerV2:     evm.MustAddress("0x5c952063c7fc8610FFDB798152D69F0B9550762b"),
		WrappedNative: evm.MustAddress("0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c"), NativeSymbol: "BNB",
		QuoteSymbols: map[evm.Address]string{evm.MustAddress("0x55d398326f99059fF775485246999027B3197955"): "USDT"},
	}, lookup(t))
	want := golden(t)
	for _, s := range samples(t) {
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
