package metadata

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGatewayHostCooldown(t *testing.T) {
	const uri = "https://original.example/ipfs/bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"
	t.Run("three_failures_skip_ten_minutes_then_resume", func(t *testing.T) {
		now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
		f := NewFetcher(Options{IPFSGateways: []string{"https://gateway.example/ipfs/"}, Now: func() time.Time { return now }, HostFailures: 3, HostCooldown: 10 * time.Minute})
		var seen []string
		recovered := false
		f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			seen = append(seen, r.URL.Hostname())
			if r.URL.Hostname() == "original.example" && !recovered {
				return nil, context.DeadlineExceeded
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}, Request: r}, nil
		})
		for i := 0; i < 4; i++ {
			if _, err := f.Fetch(context.Background(), uri); err != nil {
				t.Fatal(err)
			}
		}
		now = now.Add(10*time.Minute - time.Nanosecond)
		if _, err := f.Fetch(context.Background(), uri); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Nanosecond)
		recovered = true
		if _, err := f.Fetch(context.Background(), uri); err != nil {
			t.Fatal(err)
		}
		want := []string{"original.example", "gateway.example", "original.example", "gateway.example", "original.example", "gateway.example", "gateway.example", "gateway.example", "original.example"}
		if !reflect.DeepEqual(seen, want) {
			t.Errorf("request order=%v; want %v", seen, want)
		}
	})
	t.Run("success_resets_consecutive_failures", func(t *testing.T) {
		f := NewFetcher(Options{IPFSGateways: []string{"https://gateway.example/ipfs/"}})
		originals, gateways := 0, 0
		f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() == "original.example" {
				originals++
				if originals != 3 {
					return nil, context.DeadlineExceeded
				}
			} else {
				gateways++
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}, Request: r}, nil
		})
		for i := 0; i < 7; i++ {
			if _, err := f.Fetch(context.Background(), uri); err != nil {
				t.Fatal(err)
			}
		}
		// Original failures #1,2; success #3 resets; failures #4,5,6; #7 skips original.
		if originals != 6 || gateways != 6 {
			t.Errorf("original=%d gateway=%d; want 6/6", originals, gateways)
		}
	})
}

// Regression coverage of the same cooldown policy at the redirect boundary.
func TestGatewayCooldownAlsoSkipsRedirectDestinations(t *testing.T) {
	const cid = "bafkreieiqmlygrwswplz6boqi6zrwd6a2d6u52dm6lugw7ibe6kquw63vm"
	f := NewFetcher(Options{IPFSGateways: []string{"https://first.example/ipfs/", "https://second.example/ipfs/"}})
	var seen []string
	f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		host := r.URL.Hostname()
		seen = append(seen, host)
		code := 200
		header := http.Header{}
		if host == "cold.example" {
			code = 403
		} else if host != "second.example" {
			code = 302
			header.Set("Location", "https://cold.example/ipfs/"+cid)
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(`{}`)), Header: header, Request: r}, nil
	})
	for i := 0; i < 3; i++ {
		if _, err := f.Fetch(context.Background(), "https://cold.example/no-cid"); err == nil {
			t.Fatal("403 must fail")
		}
	}
	if _, err := f.Fetch(context.Background(), "https://original.example/ipfs/"+cid); err != nil {
		t.Fatal(err)
	}
	want := []string{"cold.example", "cold.example", "cold.example", "original.example", "first.example", "second.example"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("redirect requests=%v; want %v", seen, want)
	}
}
