package evmrpc_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/ingest"
	"github.com/meme-radar/radar/internal/ingest/evmrpc"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
)

func recorded(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(name, ".zst") {
		d, _ := zstd.NewReader(nil)
		defer d.Close()
		if b, err = d.DecodeAll(b, nil); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// source answers each method with its body; params are recorded per method.
func source(t *testing.T, bodies map[string][]byte, params map[string]string) *evmrpc.Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string
			Params json.RawMessage
		}
		json.NewDecoder(r.Body).Decode(&req)
		if params != nil {
			params[req.Method] = string(req.Params)
		}
		body, ok := bodies[req.Method]
		if !ok {
			t.Errorf("unexpected method %s", req.Method)
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return evmrpc.New(jsonrpc.New(srv.URL, srv.Client(), nil, nil))
}

func result(t *testing.T, body []byte) []byte {
	t.Helper()
	var r struct{ Result json.RawMessage }
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	json.Compact(&buf, r.Result)
	return buf.Bytes()
}

func TestBlockDecodesRecordedBlocks(t *testing.T) {
	// Expectations: fields of the recorded responses, hex converted by hand.
	for _, tc := range []struct {
		chain, block, receipts string
		num                    uint64
		hexNum                 string
		hash, parent           string
		unix                   int64
		txs                    int
	}{
		{"base", "base_block_52965315.json.zst", "base_receipts_52965315.json.zst", 52965315, "0x3282fc3",
			"6d823b5808085e89f242bda1d0de04f4752ddade14ebb566da503097c6eba702",
			"e81b71785b4aa6fb2facfd5565d5fc4fe3c30200338beb0c5e2ece9e74f060d2", 1792719977, 303},
		{"bsc", "bsc_block_129236326.json.zst", "bsc_receipts_129236326.json.zst", 129236326, "0x7b3fd66",
			"b3020a0997629ad2a7bee42b3755a795a1521909b007f1aea63bb15f65d9491a",
			"53d23a6a7c0947e97f4b35d75c952fc062fad0c7686f091749c0d9d827337db4", 1792719980, 100},
	} {
		blockBody, rcptBody := recorded(t, tc.block), recorded(t, tc.receipts)
		params := map[string]string{}
		src := source(t, map[string][]byte{"eth_getBlockByNumber": blockBody, "eth_getBlockReceipts": rcptBody}, params)
		b, err := src.Block(context.Background(), tc.num)
		if err != nil {
			t.Fatalf("%s Block: %v", tc.chain, err)
		}
		if b.Num != tc.num || hex.EncodeToString(b.Hash) != tc.hash || hex.EncodeToString(b.ParentHash) != tc.parent ||
			!b.Time.Equal(time.Unix(tc.unix, 0)) || b.TxCount != tc.txs {
			t.Errorf("%s Block = num %d hash %x parent %x time %d txs %d; want %d %s %s %d %d", tc.chain,
				b.Num, b.Hash, b.ParentHash, b.Time.Unix(), b.TxCount, tc.num, tc.hash, tc.parent, tc.unix, tc.txs)
		}
		var payload struct{ Block, Receipts json.RawMessage }
		if err := json.Unmarshal(b.Payload, &payload); err != nil {
			t.Fatalf("%s payload: %v", tc.chain, err)
		}
		var gotBlock, gotRcpt bytes.Buffer
		json.Compact(&gotBlock, payload.Block)
		json.Compact(&gotRcpt, payload.Receipts)
		if !bytes.Equal(gotBlock.Bytes(), result(t, blockBody)) || !bytes.Equal(gotRcpt.Bytes(), result(t, rcptBody)) {
			t.Errorf("%s payload does not hold the recorded block and receipts results unchanged", tc.chain)
		}
		hexNum := `"` + tc.hexNum + `"`
		if params["eth_getBlockByNumber"] != "["+hexNum+",true]" || params["eth_getBlockReceipts"] != "["+hexNum+"]" {
			t.Errorf("%s params = %v; want [%s,true] and [%s]", tc.chain, params, hexNum, hexNum)
		}
	}
}

func TestBlockNotAvailable(t *testing.T) {
	block := recorded(t, "bsc_block_129236326.json.zst")
	for name, bodies := range map[string]map[string][]byte{
		"null block (recorded Base future block)": {"eth_getBlockByNumber": []byte(`{"jsonrpc":"2.0","id":1,"result":null}`)},
		"receipts header not found (recorded BSC)": {"eth_getBlockByNumber": block,
			"eth_getBlockReceipts": recorded(t, "bsc_receipts_header_not_found.json")},
		"receipts of another block": {"eth_getBlockByNumber": block,
			"eth_getBlockReceipts": recorded(t, "base_receipts_52965315.json.zst")},
	} {
		if _, err := source(t, bodies, nil).Block(context.Background(), 129236326); !errors.Is(err, ingest.ErrNotAvailable) {
			t.Errorf("%s: err = %v; want ErrNotAvailable", name, err)
		}
	}
}

func TestHeadParsesHexBlockNumber(t *testing.T) {
	n, err := source(t, map[string][]byte{"eth_blockNumber": []byte(`{"jsonrpc":"2.0","id":1,"result":"0x3282fc3"}`)}, nil).
		Head(context.Background())
	if err != nil || n != 52965315 {
		t.Fatalf("Head = %d, %v; want 52965315 (0x3282fc3)", n, err)
	}
}
