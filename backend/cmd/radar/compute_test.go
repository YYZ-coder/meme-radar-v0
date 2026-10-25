package main

import (
	"fmt"
	"testing"
	"time"
)

func TestParseComputeFlags(t *testing.T) {
	db := env(map[string]string{"RADAR_DATABASE_URL": "postgres://x"})
	got, err := parseCompute([]string{"compute"}, db)
	if err != nil || got.dbURL != "postgres://x" || got.interval != 10*time.Second || len(got.chains) != 3 ||
		len(got.prices.Sources) != 2 || got.prices.Sources[0].Type != "binance" || got.prices.Sources[1].Type != "coinbase" {
		t.Errorf("defaults = %+v, %v; want all 3 configured chains, a 10s cycle and the configured price sources", got, err)
	}
	if fmt.Sprint(got.natives()) != "[{solana SOL} {base ETH} {bsc BNB}]" {
		t.Errorf("natives = %v", got.natives())
	}
	got, err = parseCompute([]string{"compute", "--interval=30s"}, db)
	if err != nil || got.interval != 30*time.Second {
		t.Errorf("explicit = %+v, %v", got, err)
	}
	// Price endpoints are configuration only (D147): the old flags are gone.
	for _, args := range [][]string{{"compute", "--interval=0s"}, {"compute", "--binance-url=http://b"}, {"ingest"}} {
		if _, err := parseCompute(args, db); err == nil {
			t.Errorf("parseCompute(%q) succeeded; want error", args)
		}
	}
	if _, err := parseCompute([]string{"compute"}, env(nil)); err == nil {
		t.Errorf("parseCompute without RADAR_DATABASE_URL succeeded")
	}
}

// H4-12e: command configuration must reach the independent projection loop.
func TestComputeReceivesInflowSchedule(t *testing.T) {
	got, err := parseCompute([]string{"compute"}, env(map[string]string{"RADAR_DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if time.Duration(got.inflow.InflowIntervals["1m"]) != 5*time.Second || time.Duration(got.inflow.InflowIntervals["24h"]) != 15*time.Second {
		t.Fatalf("compute inflow schedule=%+v; want D153 defaults", got.inflow)
	}
}
