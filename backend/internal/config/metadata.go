package config

import (
	"fmt"
	"math"
	"net/url"
)

// Metadata configures enrich's untrusted JSON fetching and optional API.
type Metadata struct {
	HostCooldown   Duration `yaml:"host_cooldown"`
	HostFailures   int      `yaml:"host_failures"`
	IPFSGateways   []string `yaml:"ipfs_gateways"`
	RPS            float64  `yaml:"rps"`
	PerHostRPS     float64  `yaml:"per_host_rps"`
	FourMemeAPIURL string   `yaml:"fourmeme_api_url"`
	FourMemeRPS    float64  `yaml:"fourmeme_rps"`
}

func (m Metadata) validate() error {
	if m.HostCooldown <= 0 {
		return fmt.Errorf("metadata.host_cooldown must be positive")
	}
	if m.HostFailures <= 0 {
		return fmt.Errorf("metadata.host_failures must be positive")
	}
	for _, r := range []struct {
		name  string
		value float64
	}{{"rps", m.RPS}, {"per_host_rps", m.PerHostRPS}, {"fourmeme_rps", m.FourMemeRPS}} {
		if r.value <= 0 || math.IsInf(r.value, 0) || math.IsNaN(r.value) {
			return fmt.Errorf("metadata.%s must be positive and finite", r.name)
		}
	}
	if len(m.IPFSGateways) == 0 {
		return fmt.Errorf("metadata.ipfs_gateways must not be empty")
	}
	for _, gateway := range m.IPFSGateways {
		u, err := metadataURL(gateway)
		if err != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("metadata.ipfs_gateways must contain http(s) base URLs without query/fragment")
		}
	}
	if m.FourMemeAPIURL != "" {
		if _, err := metadataURL(m.FourMemeAPIURL); err != nil {
			return fmt.Errorf("metadata.fourmeme_api_url must be empty or http(s)")
		}
	}
	return nil
}

func metadataURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("invalid metadata URL")
	}
	return u, nil
}
