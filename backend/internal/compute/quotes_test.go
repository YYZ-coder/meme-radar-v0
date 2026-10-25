package compute_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/config"
)

// fakeDecimals answers from a table keyed by hex address and records asks.
type fakeDecimals struct {
	m     map[string]int16
	asked []string
}

func (f *fakeDecimals) Decimals(ctx context.Context, asset []byte) (int16, error) {
	k := fmt.Sprintf("%x", asset)
	f.asked = append(f.asked, k)
	d, ok := f.m[k]
	if !ok {
		return 0, fmt.Errorf("no decimals for %s", k)
	}
	return d, nil
}

func TestQuoteAssetsFromChainConfig(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	bsc, _ := cfg.Chain("bsc")
	// On-chain decimals (BSC's USDT and USDC have 18, unlike Solana and Base).
	look := &fakeDecimals{m: map[string]int16{
		"bb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c": 18,
		"55d398326f99059ff775485246999027b3197955": 18,
		"8ac76a51cc950d9822d68b83fe1ad97b32cd580d": 18,
		"8d0d000ee44948fc98c9b98a4fa4921476f08b0d": 18, // USD1, decimals() read on bsc-dataseed 2026-10-23
	}}
	got, err := compute.QuoteAssets(context.Background(), bsc, look)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, q := range got {
		lines = append(lines, fmt.Sprintf("%s %x %s %s %d", q.Chain, q.Asset, q.Symbol, q.Kind, q.Decimals))
	}
	slices.Sort(lines)
	want := []string{
		"bsc 0000000000000000000000000000000000000000 BNB native 18", // native coin: no lookup
		"bsc 55d398326f99059ff775485246999027b3197955 USDT stable 18",
		"bsc 8ac76a51cc950d9822d68b83fe1ad97b32cd580d USDC stable 18",
		"bsc 8d0d000ee44948fc98c9b98a4fa4921476f08b0d USD1 stable 18", // D149
		"bsc bb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c BNB native 18",  // wrapped native is priced as BNB
	}
	if !slices.Equal(lines, want) {
		t.Errorf("quote assets:\n got  %v\n want %v", lines, want)
	}
	if slices.Contains(look.asked, "0000000000000000000000000000000000000000") {
		t.Errorf("looked up the native coin's decimals")
	}
	// A configured quote asset that is neither native nor a stablecoin is a config error.
	bsc.QuoteAssets = map[string]string{"GMEB": "0x0000000000000000000000000000000000000001"}
	if _, err := compute.QuoteAssets(context.Background(), bsc, look); err == nil {
		t.Errorf("QuoteAssets accepted GMEB as a quote asset")
	}
}
