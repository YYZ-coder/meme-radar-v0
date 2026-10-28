package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/testdb"
)

// The real command configuration and HTTP listener are the public deployment
// boundary. The opt-in flag must expose actual observation time end to end.
func TestPgAPIReadCacheStartup(t *testing.T) {
	db := testdb.New(t)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	if err = lis.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := parseConfig([]string{"api", "--store=pg", "--addr=" + addr, "--read-cache-max-age=1s"}, env(map[string]string{"RADAR_DATABASE_URL": db, "RADAR_ADMIN_TOKEN": "cache-test-admin"}))
	if err != nil {
		t.Fatalf("one-second read cache configuration rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, c, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("cached API shutdown: %v", err)
			}
		case <-time.After(6 * time.Second):
			t.Error("cached API did not stop")
		}
	})
	client := &http.Client{Timeout: time.Second}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		resp, e := client.Get("http://" + addr + "/healthz")
		if e == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("cached API not ready within5s")
		case <-poll.C:
		}
	}
	var first string
	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/api/v1/admin/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer cache-test-admin")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("cached health=%d: %s", response.StatusCode, body)
		}
		if err = contracttest.Validate(t, http.MethodGet, "/api/v1/admin/health", response.StatusCode, body); err != nil {
			t.Fatal(err)
		}
		at := response.Header.Get("X-Data-As-Of")
		observed, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			t.Fatalf("missing or invalid observation time %q: %v", at, err)
		}
		age, err := strconv.Atoi(response.Header.Get("X-Data-Age-Ms"))
		if err != nil || age < 0 || age > 1000 || time.Since(observed) > time.Second {
			t.Fatalf("observation %q age=%d err=%v exceeds1s", at, age, err)
		}
		if i == 0 {
			first = at
		} else if at != first {
			t.Fatalf("cache hit reset actual data time: first=%q next=%q", first, at)
		}
		if response.Header.Get("X-Radar-Mock") != "" {
			t.Fatal("real database response marked mock")
		}
	}
}

func TestReadCacheAgeBounds(t *testing.T) {
	for _, age := range []string{"-1ms", "1001ms"} {
		t.Run(age, func(t *testing.T) {
			if _, err := parseConfig([]string{"api", "--store=mock", "--read-cache-max-age=" + age}, env(nil)); err == nil {
				t.Fatalf("accepted cache age %s outside0..1s", age)
			}
		})
	}
}
