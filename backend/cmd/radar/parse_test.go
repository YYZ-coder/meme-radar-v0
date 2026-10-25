package main

import (
	"fmt"
	"testing"
	"time"
)

func TestParseParseFlags(t *testing.T) {
	db := env(map[string]string{"RADAR_DATABASE_URL": "postgres://x"})
	got, err := parseParse([]string{"parse", "--chain=solana"}, db)
	if err != nil || got.chain != "solana" || got.family != "solana" || got.dbURL != "postgres://x" || got.batch != 50 || got.poll != time.Second ||
		fmt.Sprint(got.decoders) != "[pump_fun pumpswap]" {
		want := "solana defaults with pump_fun and pumpswap"
		t.Errorf("defaults = %+v, %v; want %+v", got, err, want)
	}
	bsc, err := parseParse([]string{"parse", "--chain=bsc"}, db)
	if err != nil || bsc.chain != "bsc" || bsc.family != "evm" || fmt.Sprint(bsc.decoders) != "[four_meme pancake_v2 pancake_v3 pancake_infinity_cl]" || bsc.lookupURL != "https://bsc-dataseed.bnbchain.org" {
		t.Errorf("bsc = %+v, %v; want the four_meme decoder with lookups on bsc-dataseed", bsc, err)
	}
	base, err := parseParse([]string{"parse", "--chain=base"}, db)
	if err != nil || fmt.Sprint(base.decoders) != "[clanker_v4 doppler]" || len(base.skipped) != 0 {
		t.Errorf("base = decoders %v skipped %v, %v; want clanker_v4 with uniswap_v4 as its dependency", base.decoders, base.skipped, err)
	}
	got, err = parseParse([]string{"parse", "--chain=solana", "--batch=5", "--debug-addr=127.0.0.1:6061"}, db)
	if err != nil || got.batch != 5 || got.debugAddr != "127.0.0.1:6061" {
		t.Errorf("explicit = %+v, %v", got, err)
	}
	for _, tc := range []struct {
		args []string
		env  func(string) string
	}{
		{[]string{"parse", "--chain=solana"}, env(nil)}, // no database URL
		{[]string{"parse"}, db},
		{[]string{"parse", "--chain=solana", "--batch=0"}, db},
		{[]string{"parse", "--chain=solana", "--debug-addr=0.0.0.0:1"}, db},
	} {
		if _, err := parseParse(tc.args, tc.env); err == nil {
			t.Errorf("parseParse(%q) succeeded; want error", tc.args)
		}
	}
}
