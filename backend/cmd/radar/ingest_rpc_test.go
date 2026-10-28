package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

const privateFixtureBlock = uint64(52965315)
const privateFixtureHash = "6d823b5808085e89f242bda1d0de04f4752ddade14ebb566da503097c6eba702"

// The receipt and block are captured protocol samples; hash/height expectations
// are the literal independent expectations already documented with those samples.
func runPrivateSourceFixture(t *testing.T, role string) string {
	t.Helper()
	ctx := context.Background()
	database := testdb.New(t)
	st, err := store.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	responses := map[string][]byte{"eth_blockNumber": []byte(`{"jsonrpc":"2.0","id":1,"result":"0x3282fc3"}`)}
	for method, file := range map[string]string{
		"eth_getBlockByNumber": "base_block_52965315.json.zst",
		"eth_getBlockReceipts": "base_receipts_52965315.json.zst",
	} {
		compressed, err := os.ReadFile("../../internal/ingest/testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		response, err := decoder.DecodeAll(compressed, nil)
		if err != nil {
			t.Fatal(err)
		}
		responses[method] = response
	}
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Method string }
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		response, ok := responses[request.Method]
		if !ok {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(response)
	}))
	defer primary.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer public.Close()
	t.Setenv("RADAR_TEST_SOURCE_RPC_URL", primary.URL+"/private-credential/")
	path := filepath.Join(t.TempDir(), "radar.yaml")
	body := fmt.Sprintf("chains:\n  - name: base\n    family: evm\n    rpc_url_env: RADAR_TEST_SOURCE_RPC_URL\n    ingest: {rpc_url: %q, rps: 1000, confirmations: 0, prefetch: 1, block_interval: 2s, poll: 100ms}\n    backfill: {hours: 1, rps: 1000, prefetch: 1}\n", public.URL)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if role == "backfill" {
		parent, err := hex.DecodeString(privateFixtureHash)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Commit(ctx, store.Commit{Chain: "base", Block: store.Block{
			Num: privateFixtureBlock + 1, Hash: bytes.Repeat([]byte{0x77}, 32), ParentHash: parent,
			Time: time.Unix(1792719979, 0), Status: store.StatusConfirmed, Payload: []byte("{}"), Source: "fixture",
		}, NewGap: &store.Gap{From: privateFixtureBlock, To: privateFixtureBlock, Reason: store.GapStartup}}); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	switch role {
	case "ingest":
		configuration, err := parseIngest([]string{"ingest", "--chain=base", "--config=" + path}, env(map[string]string{"RADAR_DATABASE_URL": database}))
		if err != nil {
			t.Fatal(err)
		}
		go func() { done <- runIngest(runCtx, configuration, log) }()
	case "backfill":
		configuration, err := parseBackfill([]string{"backfill", "--chain=base", "--config=" + path}, env(map[string]string{"RADAR_DATABASE_URL": database}))
		if err != nil {
			t.Fatal(err)
		}
		go func() { done <- runBackfill(runCtx, configuration, log) }()
	default:
		t.Fatal("unsupported fixture role")
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		cancel()
		select {
		case err := <-done:
			stopped = true
			if err != nil {
				t.Errorf("%s stopped: %v", role, err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s failed to stop", role)
		}
	}
	defer stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		hash, err := st.BlockHash(ctx, "base", privateFixtureBlock)
		if err == nil {
			if hex.EncodeToString(hash) != privateFixtureHash {
				t.Fatalf("persisted hash=%x; want recorded private block %s", hash, privateFixtureHash)
			}
			stop()
			return logs.String()
		}
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("recorded private RPC block was not persisted; public-only source cannot serve it")
		case <-ticker.C:
		}
	}
}

func TestIngestPrivateRPCPersistsRecordedBlock(t *testing.T) {
	runPrivateSourceFixture(t, "ingest")
}

func TestPrivateRPCIngestLogsDoNotExposeCredential(t *testing.T) {
	logs := runPrivateSourceFixture(t, "ingest")
	if strings.Contains(logs, "private-credential") {
		t.Fatal("ingest logs expose the private RPC credential")
	}
}

func TestBackfillPrivateRPCPersistsRecordedBlock(t *testing.T) {
	runPrivateSourceFixture(t, "backfill")
}
