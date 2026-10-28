// Package config loads the chain configuration (chains, their ingest
// settings and enabled protocols). The embedded config.default.yaml is used
// unless a file is given; adding a chain whose family and protocol types are
// supported needs only configuration (02-data-model: chain_id is extended
// at startup).
package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed config.default.yaml
var defaultYAML []byte

// Duration is a time.Duration written as "2s" or "400ms".
type Duration time.Duration

// Config is the whole file.
type Config struct {
	Compute      Compute      `yaml:"compute"`
	DisplayNames DisplayNames `yaml:"display_names"`
	Chains       []Chain      `yaml:"chains"`
	Prices       Prices       `yaml:"prices"`
	Retention    Retention    `yaml:"retention"`
	Metadata     Metadata     `yaml:"metadata"`
}

// Compute configures independently scheduled projection refreshes.
type Compute struct {
	InflowIntervals map[string]Duration `yaml:"inflow_intervals"`
	present         bool
}

// UnmarshalYAML preserves the distinction between omitted defaults and an
// explicitly incomplete schedule, while retaining strict key validation.
func (c *Compute) UnmarshalYAML(n *yaml.Node) error {
	type plain Compute
	var v plain
	if err := n.Decode(&v); err != nil {
		return err
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value != "inflow_intervals" {
			return fmt.Errorf("line %d: unknown compute field %q", n.Content[i].Line, n.Content[i].Value)
		}
	}
	*c = Compute(v)
	c.present = true
	return nil
}

// DisplayNames maps protocol IDs to user-facing names, falling back to the ID.
type DisplayNames struct {
	Dexes     map[string]string `yaml:"dexes"`
	Platforms map[string]string `yaml:"platforms"`
}

// Prices lists the native-coin USD price sources compute tries in order
// (D147): binance, coinbase or pyth.
type Prices struct {
	Sources []PriceSource `yaml:"sources"`
}

// PriceSource is one price provider. Secrets never sit in the file: a key
// is read from the environment variable APIKeyEnv and sent in the header
// APIKeyHeader (default x-api-key).
type PriceSource struct {
	Type         string            `yaml:"type"`
	URL          string            `yaml:"url"`
	APIKeyEnv    string            `yaml:"api_key_env"`
	APIKeyHeader string            `yaml:"api_key_header"`
	Feeds        map[string]string `yaml:"feeds"` // pyth: symbol -> price feed id
}

// Retention sets automatic deletion; zero keeps rows until deleted by hand.
type Retention struct {
	RawBlocksDays int `yaml:"raw_blocks_days"` // raw_blocks (D143)
	LedgerDays    int `yaml:"ledger_days"`     // chain_blocks, trades, liquidity_events
}

// Backfill configures the backfill role: it fills gaps (stream_gaps) within
// Hours behind the head from RPCURL (empty: ingest.rpc_url).
type Backfill struct {
	Hours    float64 `yaml:"hours"`
	RPCURL   string  `yaml:"rpc_url"`
	RPS      float64 `yaml:"rps"`
	BlockRPS float64 `yaml:"block_rps"`
	Prefetch int     `yaml:"prefetch"`
	// PauseLag pauses backfill while the forward follower is more than this
	// many blocks behind the head (default 50; forward first).
	PauseLag uint64 `yaml:"pause_lag"`
}

// Chain is one configured chain.
type Chain struct {
	// RPCURLEnv names an optional private endpoint; public role URLs remain fallback sources.
	RPCURLEnv string `yaml:"rpc_url_env"`
	Name      string `yaml:"name"`
	Family    string `yaml:"family"` // evm, solana
	// QuoteAssets maps symbols to addresses of the chain's quote assets;
	// a pool whose other side is one of them tracks the other token.
	QuoteAssets map[string]string `yaml:"quote_assets"`
	// Native is the symbol of the chain coin; WrappedNative its wrapped token.
	Native        string `yaml:"native"`
	WrappedNative string `yaml:"wrapped_native"`
	// LookupRPCURL serves parse lookups and API native-balance reads;
	// empty means ingest.rpc_url.
	LookupRPCURL string `yaml:"lookup_rpc_url"`
	// LookupRPS limits each role's read-only lookups on LookupRPCURL.
	LookupRPS float64    `yaml:"lookup_rps"`
	Backfill  Backfill   `yaml:"backfill"`
	Ingest    Ingest     `yaml:"ingest"`
	Protocols []Protocol `yaml:"protocols"`
}

// Ingest holds the block source settings.
type Ingest struct {
	RPCURL        string  `yaml:"rpc_url"`
	RPS           float64 `yaml:"rps"`
	BlockRPS      float64 `yaml:"block_rps"`
	FollowHead    bool    `yaml:"follow_head"`
	Confirmations uint64  `yaml:"confirmations"`
	ReorgDepth    uint64  `yaml:"reorg_depth"`
	Prefetch      int     `yaml:"prefetch"`
	// MaxLagBlocks: how far forward may fall behind the head before it jumps
	// there and leaves a gap for the backfill role (0: the backfill window).
	MaxLagBlocks  uint64   `yaml:"max_lag_blocks"`
	BlockInterval Duration `yaml:"block_interval"`
	Poll          Duration `yaml:"poll"`
}

// Protocol enables one decoder type with its contract addresses.
type Protocol struct {
	Type      string            `yaml:"type"`
	Addresses map[string]string `yaml:"addresses"`
	Hooks     []string          `yaml:"hooks"`
	Fees      []int             `yaml:"fees"`
}

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

// Protocols each family can decode or follow. Types listed here but without
// a decoder are rejected by the parse role at startup.
var Protocols = map[string][]string{
	"solana": {"pump_fun", "pumpswap"},
	"evm":    {"four_meme", "pancake_v2", "pancake_v3", "pancake_infinity_cl", "uniswap_v4", "clanker_v4", "doppler", "uniswap_v2", "uniswap_v3"},
}

var (
	chainName  = regexp.MustCompile(`^[a-z][a-z0-9_]{1,30}$`)
	evmAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	evmHash    = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

// Load reads path, or the embedded defaults when path is empty.
func Load(path string) (Config, error) {
	data := defaultYAML
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config: %w", err)
		}
		data = b
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// Optional metadata fields inherit the embedded defaults, including in
	// older custom chain files; an explicit empty API URL still disables it.
	var defaults struct {
		Metadata     Metadata     `yaml:"metadata"`
		Compute      Compute      `yaml:"compute"`
		DisplayNames DisplayNames `yaml:"display_names"`
	}
	if err := yaml.Unmarshal(defaultYAML, &defaults); err != nil {
		return Config{}, err
	}
	c := Config{Metadata: defaults.Metadata}
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", source(path), err)
	}
	if !c.Compute.present {
		c.Compute = defaults.Compute
	}
	if c.DisplayNames.Dexes == nil {
		c.DisplayNames.Dexes = defaults.DisplayNames.Dexes
	}
	if c.DisplayNames.Platforms == nil {
		c.DisplayNames.Platforms = defaults.DisplayNames.Platforms
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", source(path), err)
	}
	for i := range c.Prices.Sources {
		if c.Prices.Sources[i].APIKeyHeader == "" {
			c.Prices.Sources[i].APIKeyHeader = "x-api-key"
		}
	}
	for i := range c.Chains {
		if c.Chains[i].LookupRPS == 0 {
			c.Chains[i].LookupRPS = 5
		}
		if c.Chains[i].Backfill.PauseLag == 0 {
			c.Chains[i].Backfill.PauseLag = 50
		}
	}
	return c, nil
}

func source(path string) string {
	if path == "" {
		return "(embedded defaults)"
	}
	return path
}

func (c Config) validate() error {
	for _, window := range []string{"1m", "5m", "1h", "4h", "24h"} {
		if c.Compute.InflowIntervals[window] <= 0 {
			return fmt.Errorf("compute.inflow_intervals.%s must be present and positive", window)
		}
	}
	for window := range c.Compute.InflowIntervals {
		if window != "1m" && window != "5m" && window != "1h" && window != "4h" && window != "24h" {
			return fmt.Errorf("compute.inflow_intervals: unknown window %q", window)
		}
	}

	if err := c.Metadata.validate(); err != nil {
		return err
	}
	if err := c.Prices.validate(); err != nil {
		return err
	}
	if c.Retention.RawBlocksDays < 0 || c.Retention.LedgerDays < 0 {
		return fmt.Errorf("retention days must not be negative")
	}
	seen := map[string]bool{}
	for i, ch := range c.Chains {
		at := fmt.Sprintf("chains[%d] %q", i, ch.Name)
		if !chainName.MatchString(ch.Name) {
			return fmt.Errorf("%s: name must match %s", at, chainName)
		}
		if seen[ch.Name] {
			return fmt.Errorf("%s: duplicate chain", at)
		}
		seen[ch.Name] = true
		known, ok := Protocols[ch.Family]
		if !ok {
			return fmt.Errorf("%s: family must be evm or solana, got %q", at, ch.Family)
		}
		if privateURL := strings.TrimSpace(os.Getenv(ch.RPCURLEnv)); privateURL != "" {
			parsed, err := url.Parse(privateURL)
			if err != nil || !httpURL(privateURL) || parsed.Hostname() == "" || parsed.Fragment != "" {
				return fmt.Errorf("%s: environment variable %s must hold a valid http(s) RPC URL without a fragment", at, ch.RPCURLEnv)
			}
		}
		in, bf := ch.Ingest, ch.Backfill
		for _, u := range []string{in.RPCURL, bf.RPCURL, ch.LookupRPCURL} {
			if u != "" && !httpURL(u) {
				return fmt.Errorf("%s: rpc url %q must be http(s)", at, u)
			}
		}
		switch {
		case bf.Hours < 0 || bf.BlockRPS < 0 || bf.Prefetch < 0 || bf.Prefetch > 64:
			return fmt.Errorf("%s: backfill.hours and block_rps must not be negative, prefetch 0–64", at)
		case bf.Hours > 0 && bf.RPS <= 0:
			return fmt.Errorf("%s: backfill.rps must be positive", at)
		case ch.LookupRPS < 0:
			return fmt.Errorf("%s: lookup_rps must not be negative", at)
		}
		switch {
		case in.RPCURL == "":
			return fmt.Errorf("%s: ingest.rpc_url is required", at)
		case in.RPS <= 0:
			return fmt.Errorf("%s: ingest.rps must be positive", at)
		case in.BlockInterval <= 0 || in.Poll <= 0:
			return fmt.Errorf("%s: ingest.block_interval and ingest.poll must be positive", at)
		case in.Prefetch < 0 || in.Prefetch > 64:
			return fmt.Errorf("%s: ingest.prefetch must be 0–64", at)
		}
		addrs := []string{}
		if ch.WrappedNative != "" {
			addrs = append(addrs, ch.WrappedNative)
		}
		for _, a := range ch.QuoteAssets {
			addrs = append(addrs, a)
		}
		for _, p := range ch.Protocols {
			if !contains(known, p.Type) {
				return fmt.Errorf("%s: unknown protocol type %q for family %s", at, p.Type, ch.Family)
			}
			for k, a := range p.Addresses {
				if strings.HasSuffix(k, "_hash") {
					if ch.Family == "evm" && !evmHash.MatchString(a) {
						return fmt.Errorf("%s: %s.%s %q is not a 32-byte hex hash", at, p.Type, k, a)
					}
					continue
				}
				addrs = append(addrs, a)
			}
			addrs = append(addrs, p.Hooks...)
		}
		if ch.Family == "evm" {
			for _, a := range addrs {
				if !evmAddress.MatchString(a) {
					return fmt.Errorf("%s: %q is not an EVM address", at, a)
				}
			}
		}
	}
	return nil
}

// priceTypes are the implemented price sources (internal/prices).
var priceTypes = []string{"binance", "coinbase", "pyth"}

func (p Prices) validate() error {
	for i, s := range p.Sources {
		at := fmt.Sprintf("prices.sources[%d] %q", i, s.Type)
		switch {
		case !contains(priceTypes, s.Type):
			return fmt.Errorf("%s: type must be one of %v", at, priceTypes)
		case !httpURL(s.URL):
			return fmt.Errorf("%s: url %q must be http(s)", at, s.URL)
		case s.Type == "pyth" && len(s.Feeds) == 0:
			return fmt.Errorf("%s: feeds (symbol: price feed id) are required", at)
		}
	}
	return nil
}

func httpURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Chain returns the named chain.
func (c Config) Chain(name string) (Chain, bool) {
	for _, ch := range c.Chains {
		if ch.Name == name {
			return ch, true
		}
	}
	return Chain{}, false
}

// Protocol returns the chain's protocol of type t.
func (ch Chain) Protocol(t string) (Protocol, bool) {
	for _, p := range ch.Protocols {
		if p.Type == t {
			return p, true
		}
	}
	return Protocol{}, false
}

// RPCURLs returns the ordered endpoints for one role; empty uses ingest's public URL.
func (c Chain) RPCURLs(publicURL string) []string {
	if publicURL == "" {
		publicURL = c.Ingest.RPCURL
	}
	privateURL := strings.TrimSpace(os.Getenv(c.RPCURLEnv))
	if privateURL == "" || privateURL == publicURL {
		return []string{publicURL}
	}
	return []string{privateURL, publicURL}
}
