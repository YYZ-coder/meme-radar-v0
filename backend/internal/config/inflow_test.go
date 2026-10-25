package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/config"
)

// H4-11a takes D153 literally; display names are copied from mock's universe.
func TestInflowConfigurationDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		window   string
		interval time.Duration
	}{{"1m", 5 * time.Second}, {"5m", 5 * time.Second}, {"1h", 15 * time.Second}, {"4h", 15 * time.Second}, {"24h", 15 * time.Second}} {
		if got := time.Duration(cfg.Compute.InflowIntervals[tc.window]); got != tc.interval {
			t.Errorf("%s interval=%s, want %s", tc.window, got, tc.interval)
		}
	}
	if cfg.DisplayNames.Dexes["pumpswap"] != "PumpSwap" || cfg.DisplayNames.Dexes["pancakeswap_v2"] != "PancakeSwap v2" || cfg.DisplayNames.Platforms["pump_fun"] != "Pump.fun" || cfg.DisplayNames.Platforms["four_meme"] != "Four.meme" {
		t.Errorf("display names=%+v; want mock names", cfg.DisplayNames)
	}
}

// Invalid values are an input contract: a supplied compute section cannot
// silently acquire missing D153 windows from the embedded defaults.
func TestInflowIntervalsRejectNonPositiveAndMissing(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"zero", "compute: {inflow_intervals: {1m: 0s, 5m: 5s, 1h: 15s, 4h: 15s, 24h: 15s}}"},
		{"negative", "compute: {inflow_intervals: {1m: 5s, 5m: -1s, 1h: 15s, 4h: 15s, 24h: 15s}}"},
		{"missing window", "compute: {inflow_intervals: {1m: 5s, 5m: 5s, 1h: 15s, 4h: 15s}}"},
		{"empty intervals", "compute: {inflow_intervals: {}}"},
		{"empty section", "compute: {}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(write(t, tc.yaml))
			if err == nil || !strings.Contains(err.Error(), "compute.inflow_intervals") {
				t.Errorf("Load(%s)=%v; want compute.inflow_intervals error", tc.name, err)
			}
		})
	}
	cfg, err := config.Load(write(t, "chains: []"))
	if err != nil || time.Duration(cfg.Compute.InflowIntervals["1m"]) != 5*time.Second {
		t.Fatalf("omitted compute must retain D153 defaults: %+v, %v", cfg.Compute, err)
	}
}
