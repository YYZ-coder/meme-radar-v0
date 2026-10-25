package compute_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"encoding/hex"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"
	"math/big"
	"net/http"

	"github.com/meme-radar/radar/internal/evm"
	"github.com/meme-radar/radar/internal/ingest/jsonrpc"
	"github.com/meme-radar/radar/internal/parse/balances"
)

// TestBalancesMatchChainForCoveredToken is deliberately manual: run after the
// user schedules deployment, full backfill and a paused compute snapshot.
// It never pauses a service or writes cloud data. Historical eth_call pins
// reconciliation to the accumulated block, avoiding changes at the latest tip.
func TestBalancesMatchChainForCoveredToken(t *testing.T) {
	url, rpcURL := os.Getenv("RADAR_H5_RECONCILE_DATABASE_URL"), os.Getenv("RADAR_H5_RECONCILE_RPC_URL")
	if url == "" || rpcURL == "" {
		t.Skip("manual deployed full-token reconciliation; requires explicit DB/RPC URLs")
	}
	if os.Getenv("RADAR_H5_COMPUTE_PAUSED") != "1" {
		t.Fatal("user must arrange a paused compute snapshot before this manual check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid read-only reconciliation config")
	}
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["statement_timeout"] = "15000"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("connect read-only reconciliation DB failed")
	}
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var token []byte
	var cutoff int64
	err = tx.QueryRow(ctx, `SELECT h.token_address,(SELECT max(block_num) FROM chain_blocks c
        WHERE c.chain=h.chain AND c.balance_version=$1 AND c.balance_parsed_at<=h.as_of)
        FROM token_holder_state h JOIN tokens t ON t.chain=h.chain AND t.address=h.token_address
        WHERE h.chain='bsc' AND h.coverage='full' AND h.holders>=20 AND t.launch_platform='four_meme'
        ORDER BY h.holders DESC,h.token_address LIMIT 1`, balances.Version).Scan(&token, &cutoff)
	if err != nil {
		t.Fatalf("select deployed full Four.meme token: %v", err)
	}
	rows, err := tx.Query(ctx, `SELECT owner,balance::text FROM token_balances WHERE chain='bsc' AND token_address=$1
        AND balance>0 AND is_holder_owner(chain,owner) ORDER BY balance DESC,owner LIMIT 20`, token)
	if err != nil {
		t.Fatal(err)
	}
	type holding struct {
		owner  []byte
		amount string
	}
	holdings, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (holding, error) {
		var h holding
		err := r.Scan(&h.owner, &h.amount)
		return h, err
	})
	if err != nil {
		t.Fatal(err)
	}
	rpc := jsonrpc.New(rpcURL, &http.Client{Timeout: 10 * time.Second}, rate.NewLimiter(5, 1), nil)
	address := evm.Address(token).Hex()
	tag := fmt.Sprintf("0x%x", cutoff)
	for _, h := range holdings {
		if len(h.owner) != 20 {
			t.Fatal("non-EVM owner in holder rows")
		}
		data := "0x70a08231" + strings.Repeat("0", 24) + hex.EncodeToString(h.owner)
		raw, err := rpc.Call(ctx, "eth_call", []any{map[string]string{"to": address, "data": data}, tag})
		if err != nil {
			t.Fatal(err)
		}
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			t.Fatal(err)
		}
		actual, ok := new(big.Int).SetString(strings.TrimPrefix(encoded, "0x"), 16)
		if !ok {
			t.Fatal("invalid balanceOf result")
		}
		if actual.String() != h.amount {
			t.Errorf("%x balance=%s; chain at %s=%s", h.owner, h.amount, tag, actual)
		}
	}
	t.Logf("reconciled chain=bsc token=%s block=%d holders=%d", address, cutoff, len(holdings))
}
