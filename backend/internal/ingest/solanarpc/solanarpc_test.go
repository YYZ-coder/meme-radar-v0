package solanarpc_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/meme-radar/radar/internal/ingest"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/ingest/solanarpc"
)

// recorded returns a response file from ../testdata, decompressing .zst.
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

// source serves body for every call and records the last request.
func source(t *testing.T, body []byte, req *[]byte) *solanarpc.Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if req != nil {
			*req, _ = io.ReadAll(r.Body)
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return solanarpc.New(jsonrpc.New(srv.URL, srv.Client(), nil, nil))
}

func TestBlockDecodesRecordedMainnetBlock(t *testing.T) {
	body := recorded(t, "solana_getblock_460261066.json.zst")
	var req []byte
	b, err := source(t, body, &req).Block(context.Background(), 460261066)
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	// Expectations: the recorded response fields, hashes base58-decoded with
	// an independent Python implementation.
	if b.Num != 460261066 ||
		hex.EncodeToString(b.Hash) != "fd051a670032dca73d50978f9127cabca2933aa30fb1594f4bea72511a5071a5" ||
		hex.EncodeToString(b.ParentHash) != "395a386578bf6a416aab60b066e59772ff625e3bcdc2f30cd6b9d69c3e925ef1" ||
		!b.Time.Equal(time.Unix(1792719992, 0)) || b.TxCount != 346 {
		t.Fatalf("Block = num %d hash %x parent %x time %s txs %d; want 460261066 fd05…71a5 395a…5ef1 %s 346",
			b.Num, b.Hash, b.ParentHash, b.Time, b.TxCount, time.Unix(1792719992, 0))
	}
	var resp struct{ Result json.RawMessage }
	if err := json.Unmarshal(body, &resp); err != nil || !bytes.Equal(b.Payload, resp.Result) {
		t.Fatalf("Payload is %d bytes; want the %d-byte result object unchanged", len(b.Payload), len(resp.Result))
	}
	var sent struct {
		Method string
		Params []json.RawMessage
	}
	json.Unmarshal(req, &sent)
	var opts map[string]any
	if sent.Method != "getBlock" || len(sent.Params) != 2 || string(sent.Params[0]) != "460261066" || json.Unmarshal(sent.Params[1], &opts) != nil ||
		opts["commitment"] != "confirmed" || opts["encoding"] != "base64" || opts["transactionDetails"] != "full" ||
		opts["rewards"] != false || opts["maxSupportedTransactionVersion"] != float64(1) {
		t.Fatalf("request = %s; want getBlock [slot, {confirmed, base64, full, rewards false, maxSupportedTransactionVersion 1}]", req)
	}
}

func TestBlockMapsRecordedErrors(t *testing.T) {
	for _, tc := range []struct {
		file string
		want error
	}{
		{"solana_getblock_skipped.json", ingest.ErrSkipped},            // -32007
		{"solana_getblock_not_available.json", ingest.ErrNotAvailable}, // -32004
	} {
		_, err := source(t, recorded(t, tc.file), nil).Block(context.Background(), 1)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v; want %v", tc.file, err, tc.want)
		}
	}
}

func TestHeadReturnsConfirmedSlot(t *testing.T) {
	var req []byte
	slot, err := source(t, []byte(`{"jsonrpc":"2.0","result":460260995,"id":1}`), &req).Head(context.Background())
	if err != nil || slot != 460260995 {
		t.Fatalf("Head = %d, %v; want 460260995", slot, err)
	}
	if !bytes.Contains(req, []byte(`"getSlot"`)) || !bytes.Contains(req, []byte(`"commitment":"confirmed"`)) {
		t.Fatalf("request = %s; want getSlot with confirmed commitment", req)
	}
}
