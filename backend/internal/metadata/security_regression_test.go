package metadata

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
)

func TestFetchRejectsSharedAndReservedAddresses(t *testing.T) {
	for _, ip := range []string{"100.64.0.1", "198.18.0.1", "240.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			f := NewFetcher(Options{Resolver: resolverFunc(func(context.Context, string) ([]net.IPAddr, error) { return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil })})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if _, err := f.Fetch(ctx, "http://special.test:9/a"); !errors.Is(err, ErrBlocked) {
				t.Errorf("non-public %s error=%v; want ErrBlocked before dialing", ip, err)
			}
		})
	}
}

func TestMetadataReportsBlockedGatewayHost(t *testing.T) {
	st, _ := metadataDB(t)
	now := metadataDay.Add(12 * time.Hour)
	metadataFacts(t, st, "solana", 1, now.Add(-time.Minute), []parse.Token{{Address: bytes.Repeat([]byte{1}, 32), URI: "https://original.example/ipfs/QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr"}}, true)
	f := replayFetcher(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	f.options.IPFSGateways = []string{"http://127.0.0.1/ipfs/"}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	if err := New(st, "solana", f, nil, logger).Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"host":"127.0.0.1"`) {
		t.Errorf("SSRF log must identify blocked gateway, got %s", logs.String())
	}
}

func TestFetchRejectsIPv6BenchmarkAddresses(t *testing.T) {
	// IANA IPv6 Special-Purpose registry: 2001:2::/48 is Benchmarking,
	// Globally Reachable=false, the IPv6 counterpart of 198.18.0.0/15.
	f := NewFetcher(Options{Resolver: resolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("2001:2::26")}}, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := f.Fetch(ctx, "https://benchmark.example/a"); !errors.Is(err, ErrBlocked) {
		t.Errorf("IPv6 benchmark error=%v; want ErrBlocked before dialing", err)
	}
}
