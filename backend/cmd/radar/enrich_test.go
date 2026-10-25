package main

import (
	"testing"
	"time"
)

func TestParseEnrichFlags(t *testing.T) {
	db := env(map[string]string{"RADAR_DATABASE_URL": "postgres://x"})
	got, err := parseEnrich([]string{"enrich"}, db)
	if err != nil || got.dbURL != "postgres://x" || got.interval != 30*time.Second || len(got.chains) != 3 {
		t.Errorf("defaults = %+v, %v; want every configured chain every 30s", got, err)
	}
	if got, err := parseEnrich([]string{"enrich", "--interval=1m"}, db); err != nil || got.interval != time.Minute {
		t.Errorf("--interval=1m = %+v, %v", got, err)
	}
	for _, args := range [][]string{{"enrich", "--interval=0s"}, {"enrich", "--chain=bsc"}, {"compute"}} {
		if _, err := parseEnrich(args, db); err == nil {
			t.Errorf("parseEnrich(%q) succeeded; want error", args)
		}
	}
}
