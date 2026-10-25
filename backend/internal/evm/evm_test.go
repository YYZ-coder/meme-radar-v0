package evm_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/evm"
)

func unzst(t *testing.T, path string) []byte {
	t.Helper()
	z, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := zstd.NewReader(nil)
	defer d.Close()
	b, err := d.DecodeAll(z, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTopicMatchesKnownSignatures(t *testing.T) {
	// ERC-20 Transfer; Uniswap V2 Swap as computed by the independent Python
	// Keccak, which matched real V2 Swap logs on Base.
	for sig, want := range map[string]string{
		"Transfer(address,address,uint256)":                     "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
		"Swap(address,uint256,uint256,uint256,uint256,address)": "d78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822",
	} {
		if got := evm.Topic(sig); hex.EncodeToString(got[:]) != want {
			t.Errorf("Topic(%s) = %x; want %s", sig, got, want)
		}
	}
}

func TestParseBlockJoinsTransactionsAndReceipts(t *testing.T) {
	// The recorded BSC block 129236326 as ingest stores it.
	blk := unzst(t, "../ingest/testdata/bsc_block_129236326.json.zst")
	rcpt := unzst(t, "../ingest/testdata/bsc_receipts_129236326.json.zst")
	var b, r struct{ Result json.RawMessage }
	json.Unmarshal(blk, &b)
	json.Unmarshal(rcpt, &r)
	payload := []byte(`{"block":` + string(b.Result) + `,"receipts":` + string(r.Result) + `}`)
	got, err := evm.ParseBlock(payload)
	if err != nil {
		t.Fatalf("ParseBlock: %v", err)
	}
	// Expectations read from the recorded JSON with an independent script.
	var raw struct {
		Block struct {
			Timestamp    string
			Transactions []struct{ Hash, From, To, Value string }
		}
		Receipts []struct {
			Status string
			Logs   []struct {
				Address, Data, LogIndex string
				Topics                  []string
			}
		}
	}
	json.Unmarshal(payload, &raw)
	if got.Time.Unix() != 1792719980 || len(got.Txs) != 100 {
		t.Fatalf("block time %d txs %d; want 1792719980 and 100", got.Time.Unix(), len(got.Txs))
	}
	logs, failed := 0, 0
	for i, tx := range got.Txs {
		rt, rr := raw.Block.Transactions[i], raw.Receipts[i]
		if tx.Index != i || "0x"+hex.EncodeToString(tx.Hash[:]) != rt.Hash || tx.From.Hex() != rt.From {
			t.Fatalf("tx %d = index %d hash %x from %s; want %s %s", i, tx.Index, tx.Hash, tx.From.Hex(), rt.Hash, rt.From)
		}
		if tx.Failed != (rr.Status == "0x0") || len(tx.Logs) != len(rr.Logs) {
			t.Fatalf("tx %d failed %v logs %d; want status %s logs %d", i, tx.Failed, len(tx.Logs), rr.Status, len(rr.Logs))
		}
		for j, l := range tx.Logs {
			if l.Address.Hex() != rr.Logs[j].Address || "0x"+hex.EncodeToString(l.Data) != rr.Logs[j].Data || len(l.Topics) != len(rr.Logs[j].Topics) {
				t.Fatalf("tx %d log %d mismatch", i, j)
			}
		}
		logs += len(tx.Logs)
		if tx.Failed {
			failed++
		}
	}
	t.Logf("%d logs, %d failed transactions", logs, failed)
}
