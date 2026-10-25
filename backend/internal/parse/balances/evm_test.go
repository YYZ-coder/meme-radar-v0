package balances_test

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"slices"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/parse/balances"
)

func TestTransferDeltas(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/expect/h5-evm-deltas.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]struct{ Tracked, Deltas []string }
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	z, err := os.ReadFile("../../evm/testdata/four_meme.json.zst")
	if err != nil {
		t.Fatal(err)
	}
	d, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	data, err = d.DecodeAll(z, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Samples []struct {
			Name        string
			Tx, Receipt json.RawMessage
		}
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	for _, sample := range rec.Samples {
		t.Run(sample.Name, func(t *testing.T) {
			payload := []byte(`{"block":{"timestamp":"0x0","transactions":[` + string(sample.Tx) + `]},"receipts":[` + string(sample.Receipt) + `]}`)
			block, err := evm.ParseBlock(payload)
			if err != nil {
				t.Fatal(err)
			}
			tx := block.Txs[0]
			tx.Index = 7
			tracked := map[string]struct{}{}
			for _, address := range expected[sample.Name].Tracked {
				a := evm.MustAddress(address)
				tracked[string(a[:])] = struct{}{}
			}
			var got []string
			for _, b := range balances.EVM(tx, tracked) {
				if b.Tx != 7 {
					t.Fatalf("tx index=%d;want 7", b.Tx)
				}
				got = append(got, fmt.Sprintf("%d|0x%x|0x%x|%s", b.Ix, b.Token, b.Owner, b.Delta))
			}
			slices.Sort(got)
			if !slices.Equal(got, expected[sample.Name].Deltas) {
				t.Errorf("deltas=%v;want %v", got, expected[sample.Name].Deltas)
			}
			if got := balances.EVM(tx, nil); len(got) != 0 {
				t.Errorf("untracked deltas: %v", got)
			}
			tx.Failed = true
			if got := balances.EVM(tx, tracked); len(got) != 0 {
				t.Errorf("failed deltas: %v", got)
			}
		})
	}
	t.Run("mint", func(t *testing.T) {
		token := evm.Address{1}
		owner := evm.Address{2}
		to := evm.Hash{}
		copy(to[12:], owner[:])
		tx := evm.Tx{Index: 3, Logs: []evm.Log{{Index: 4, Address: token, Topics: []evm.Hash{evm.Topic("Transfer(address,address,uint256)"), {}, to}, Data: new(big.Int).SetInt64(100).FillBytes(make([]byte, 32))}}}
		got := balances.EVM(tx, map[string]struct{}{string(token[:]): {}})
		if len(got) != 1 || got[0].Delta != "100" || hex.EncodeToString(got[0].Owner) != hex.EncodeToString(owner[:]) {
			t.Fatalf("mint deltas=%+v;want receiver +100", got)
		}
	})
}
