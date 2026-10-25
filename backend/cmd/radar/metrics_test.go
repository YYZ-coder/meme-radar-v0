package main

import "testing"

func TestMetricsAddrDefaultsToPort9100(t *testing.T) {
	if got := metricsAddr(env(nil)); got != ":9100" {
		t.Errorf("default = %q; want :9100", got)
	}
	if got := metricsAddr(env(map[string]string{"RADAR_METRICS_ADDR": "off"})); got != "off" {
		t.Errorf("RADAR_METRICS_ADDR=off = %q", got)
	}
}
