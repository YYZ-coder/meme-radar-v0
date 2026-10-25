package chaininfo_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/chaininfo"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
)

// replay serves eth_call responses recorded from bsc-dataseed.bnbchain.org
// on 2026-10-23 (testdata/bsc_calls.json), keyed by target and calldata.
func replay(t *testing.T) *chaininfo.RPC { return replayFile(t, "testdata/bsc_calls.json") }

// recordedAsset is a Doppler asset whose getAssetData is recorded with a
// non-zero WETH numeraire (the first such entry in base_calls.json).
func recordedAsset(t *testing.T) evm.Address {
	b, _ := os.ReadFile("testdata/base_calls.json")
	var rec []struct {
		Data     string
		Response struct{ Result string }
	}
	json.Unmarshal(b, &rec)
	for _, c := range rec {
		if strings.HasPrefix(c.Response.Result, "0x0000000000000000000000004200000000000000000000000000000000000006") {
			return evm.MustAddress("0x" + c.Data[34:])
		}
	}
	t.Fatal("no recorded Doppler asset")
	return evm.Address{}
}

func replayFile(t *testing.T, file string) *chaininfo.RPC {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var rec []struct {
		To, Data string
		Response json.RawMessage
	}
	json.Unmarshal(b, &rec)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string
			Params []json.RawMessage
		}
		json.NewDecoder(r.Body).Decode(&req)
		var call struct{ To, Data string }
		json.Unmarshal(req.Params[0], &call)
		for _, c := range rec {
			if req.Method == "eth_call" && strings.EqualFold(c.To, call.To) && strings.EqualFold(c.Data, call.Data) {
				w.Write(c.Response)
				return
			}
		}
		t.Errorf("unrecorded call %s %s", call.To, call.Data)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"unrecorded"}}`))
	}))
	t.Cleanup(srv.Close)
	return chaininfo.New(jsonrpc.New(srv.URL, srv.Client(), nil, nil), evm.MustAddress("0xF251F83e40a78868FcfA3FA4599Dad6494E46034"))
}

func TestFourMemeDecodesRecordedTokenInfo(t *testing.T) {
	r := replay(t)
	ctx := context.Background()
	// Expectations: the recorded return words read by hand (version 2,
	// TokenManager2, quote USDT / BNB), and all zeros for USDT itself.
	for token, want := range map[string]chaininfo.FourMeme{
		"0x3b7ef5fa9edf3b6b763768ac654d3a5b0d2d4444": {Found: true, Version: 2, Manager: evm.MustAddress("0x5c952063c7fc8610ffdb798152d69f0b9550762b"), Quote: evm.MustAddress("0x55d398326f99059ff775485246999027b3197955")},
		"0x179f8ab62895b0f3125c7f3723c2e218b425ffff": {Found: true, Version: 2, Manager: evm.MustAddress("0x5c952063c7fc8610ffdb798152d69f0b9550762b")},
		"0x55d398326f99059ff775485246999027b3197955": {},
	} {
		got, err := r.FourMeme(ctx, evm.MustAddress(token))
		if err != nil || got != want {
			t.Errorf("FourMeme(%s) = %+v, %v; want %+v", token, got, err, want)
		}
	}
}

func TestERC20DecodesRecordedMetadata(t *testing.T) {
	got, err := replay(t).ERC20(context.Background(), evm.MustAddress("0x46ceefda28dd7207059ed19b0acdc026955bb15c"))
	if err != nil || got != (chaininfo.ERC20{Decimals: 18, Symbol: "GMEB"}) {
		t.Fatalf("ERC20 = %+v, %v; want 18 GMEB", got, err)
	}
}

func TestInfinityPoolKeyDecodesRecordedKey(t *testing.T) {
	// Recorded poolIdToPoolKey of a WBNB / …4444 pool: words read by hand.
	got, err := replay(t).InfinityPoolKey(context.Background(), evm.MustAddress("0xa0FfB9c1CE1Fe56963B0321B32E7A0302114058b"), poolID(t))
	if err != nil || got.Currency0 != evm.MustAddress("0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c") || got.Currency1 != evm.MustAddress("0xeccbb861c0dda7efd964010085488b69317e4444") {
		t.Fatalf("InfinityPoolKey = %+v, %v; want WBNB / 0xeccb…4444", got, err)
	}
}

// poolID is the full id of the recorded pool starting 0ac01d4ccad0.
func poolID(t *testing.T) evm.Hash {
	b, _ := os.ReadFile("testdata/bsc_calls.json")
	var rec []struct{ To, Data string }
	json.Unmarshal(b, &rec)
	for _, c := range rec {
		if strings.HasPrefix(c.Data, "0x0e2d484a0ac01d4ccad0") {
			var h evm.Hash
			copy(h[:], must(hex.DecodeString(c.Data[10:])))
			return h
		}
	}
	t.Fatal("recorded pool key not found")
	return evm.Hash{}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestDopplerAssetDecodesRecordedData(t *testing.T) {
	r := replayFile(t, "testdata/base_calls.json")
	ctx, airlock := context.Background(), evm.MustAddress("0x660eAaEdEBc968f8f3694354FA8EC0b4c5Ba8D12")
	// Words of the recorded getAssetData read by hand: numeraire WETH,
	// NoOpMigrator, DopplerHookInitializer, pool = the asset itself.
	got, err := r.DopplerAsset(ctx, airlock, recordedAsset(t))
	if err != nil || !got.Found || got.Numeraire != evm.MustAddress("0x4200000000000000000000000000000000000006") ||
		got.Initializer != evm.MustAddress("0xbdf938149ac6a781f94faa0ed45e6a0e984c6544") {
		t.Fatalf("DopplerAsset = %+v, %v; want WETH numeraire via DopplerHookInitializer", got, err)
	}
	if n, err := r.DopplerAsset(ctx, airlock, evm.MustAddress("0x4200000000000000000000000000000000000006")); err != nil || n.Found {
		t.Fatalf("DopplerAsset(WETH) = %+v, %v; want not found", n, err)
	}
}

// Expectations: the recorded uint256 results decoded independently in Python.
func TestBalanceOfDecodesRecordedCall(t *testing.T) {
	r := replayFile(t, "testdata/bsc_balance_calls.json")
	pool := evm.MustAddress("0xfad3b1b70868e16e93e323ea1375e2136a6c829a")
	for token, want := range map[string]string{
		"0x0a43fc31a73013089df59194872ecae4cae14444": "1972248601832277892231848",
		"0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c": "3276432421007413848",
	} {
		got, err := r.BalanceOf(context.Background(), evm.MustAddress(token), pool)
		if err != nil || got.String() != want {
			t.Errorf("BalanceOf(%s) = %v, %v; want %s", token, got, err, want)
		}
	}
}

// Recorded totalSupply() of a Four.meme token: 1e9 tokens x 1e18.
func TestTotalSupplyDecodesRecordedCall(t *testing.T) {
	r := replayFile(t, "testdata/bsc_balance_calls.json")
	got, err := r.TotalSupply(context.Background(), evm.MustAddress("0x0a43fc31a73013089df59194872ecae4cae14444"))
	if err != nil || got.String() != "1000000000000000000000000000" {
		t.Errorf("TotalSupply = %v, %v; want 1e27", got, err)
	}
}
