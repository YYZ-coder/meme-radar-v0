package solana_test

import (
	"os"
	"reflect"
	"strconv"
	"testing"

	"encoding/json"
	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/solana"
)

func TestParseTxReadsPreTokenBalances(t *testing.T) {
	// Independent RPC-meta expectations: python3 testdata/expect/soltx.py.
	data, err := os.ReadFile("../../testdata/expect/h5-prebalances.json")
	if err != nil {
		t.Fatal(err)
	}
	type balance struct {
		Mint, Owner, Amount string
		AccountIndex        int `json:"account_index"`
	}
	var want map[string][]balance
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	z, err := os.ReadFile("../ingest/testdata/solana_getblock_460261066.json.zst")
	if err != nil {
		t.Fatal(err)
	}
	d, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	raw, err := d.DecodeAll(z, nil)
	if err != nil {
		t.Fatal(err)
	}
	var block struct {
		Result struct{ Transactions []json.RawMessage }
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{2, 7, 9} {
		tx, err := solana.ParseTx(index, block.Result.Transactions[index])
		if err != nil {
			t.Fatal(err)
		}
		var got []balance
		for _, b := range tx.PreBalances {
			got = append(got, balance{b.Mint.String(), b.Owner.String(), strconv.FormatUint(b.Amount, 10), b.AccountIndex})
		}
		if !reflect.DeepEqual(got, want[strconv.Itoa(index)]) {
			t.Errorf("tx %d pre balances = %+v; want %+v", index, got, want[strconv.Itoa(index)])
		}
	}
}
