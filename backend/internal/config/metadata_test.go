package config_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/config"
)

func TestMetadataConfiguration(t *testing.T) {
	c, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Metadata.IPFSGateways, []string{"https://gateway.pinata.cloud/ipfs/", "https://ipfs.io/ipfs/"}) || c.Metadata.RPS != 10 || c.Metadata.PerHostRPS != 2 || c.Metadata.FourMemeRPS != 2 || c.Metadata.FourMemeAPIURL != "https://four.meme/meme-api/v1/private/token/get/v2" {
		t.Errorf("metadata defaults=%+v; want D155/D156 endpoints and 10/2/2 RPS", c.Metadata)
	}
	const chain = "chains: [{name: base, family: evm, ingest: {rpc_url: 'https://rpc.example', rps: 1, block_interval: 2s, poll: 1s}}]\n"
	for _, body := range []string{
		`metadata: {ipfs_gateways: []}`, `metadata: {ipfs_gateways: [file:///tmp/a]}`,
		`metadata: {ipfs_gateways: [https:///nohost]}`, `metadata: {ipfs_gateways: ['https://example.org/ipfs/?x=1']}`,
		`metadata: {rps: 0}`, `metadata: {per_host_rps: -1}`, `metadata: {fourmeme_rps: .nan}`,
		`metadata: {rps: .inf}`, `metadata: {fourmeme_api_url: 'javascript:alert(1)'}`,
	} {
		if _, err := config.Load(write(t, chain+body)); err == nil {
			t.Errorf("accepted invalid metadata configuration: %s", body)
		} else if !strings.Contains(err.Error(), "metadata") {
			t.Errorf("wrong configuration error: %v", err)
		}
	}
	custom, err := config.Load(write(t, chain+`metadata: {ipfs_gateways: ['https://custom.example/ipfs/'], fourmeme_api_url: '', rps: 3, per_host_rps: 1, fourmeme_rps: 0.5}`))
	if err != nil || custom.Metadata.FourMemeAPIURL != "" || custom.Metadata.RPS != 3 || !reflect.DeepEqual(custom.Metadata.IPFSGateways, []string{"https://custom.example/ipfs/"}) {
		t.Errorf("custom/disabled config=%+v err=%v", custom.Metadata, err)
	}
}

func TestMetadataHostCooldownConfiguration(t *testing.T) {
	c, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if time.Duration(c.Metadata.HostCooldown) != 10*time.Minute || c.Metadata.HostFailures != 3 {
		t.Errorf("cooldown defaults=%+v; want 10m and 3", c.Metadata)
	}
	const chain = "chains: [{name: base, family: evm, ingest: {rpc_url: 'https://rpc.example', rps: 1, block_interval: 2s, poll: 1s}}]\n"
	for _, body := range []string{`metadata: {host_cooldown: 0s}`, `metadata: {host_cooldown: -1m}`, `metadata: {host_failures: 0}`, `metadata: {host_failures: -1}`} {
		if _, err := config.Load(write(t, chain+body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	custom, err := config.Load(write(t, chain+`metadata: {host_cooldown: 2m, host_failures: 5}`))
	if err != nil || time.Duration(custom.Metadata.HostCooldown) != 2*time.Minute || custom.Metadata.HostFailures != 5 {
		t.Errorf("custom=%+v err=%v", custom.Metadata, err)
	}
}
