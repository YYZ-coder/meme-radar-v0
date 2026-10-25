package main

import "testing"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestParseConfig(t *testing.T) {
	c, err := parseConfig([]string{"api", "--store=mock"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.addr != "127.0.0.1:8080" || c.adminToken != "mock-admin" || c.debugAddr != "" {
		t.Errorf("mock defaults = %+v; want 127.0.0.1:8080, mock-admin token, pprof off", c)
	}
	c, err = parseConfig([]string{"api", "--store=mock", "--addr=127.0.0.1:9000", "--debug-addr=127.0.0.1:6060"}, env(map[string]string{"RADAR_ADMIN_TOKEN": "s3cret"}))
	if err != nil || c.addr != "127.0.0.1:9000" || c.adminToken != "s3cret" || c.debugAddr != "127.0.0.1:6060" {
		t.Errorf("explicit config = %+v, %v", c, err)
	}
	for _, bad := range [][]string{
		nil,
		{"ingest"},
		{"api"},
		{"api", "--store=postgres"},
		{"api", "--store=mock", "--debug-addr=0.0.0.0:6060"},
		{"api", "--store=mock", "--nope"},
	} {
		if _, err := parseConfig(bad, env(nil)); err == nil {
			t.Errorf("parseConfig(%q) succeeded, want error", bad)
		}
	}
}
