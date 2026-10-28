package pgapi_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// delayedPostgres relays real PostgreSQL messages. After authentication and
// warmup, releasing each ReadyForQuery response incurs a fixed transport delay.
// This models database RTT at the external protocol boundary; no query or call
// counts are asserted, and no database response is fabricated.
func delayedPostgres(t *testing.T, upstream string, delay time.Duration, firstOnly ...bool) (string, func()) {
	return limitedPostgres(t, upstream, delay, 0, len(firstOnly) > 0 && firstOnly[0])
}

// limitedPostgres also models a bounded PostgreSQL result-transfer rate. The
// real messages are buffered unchanged; delay depends only on their byte size.
func limitedPostgres(t *testing.T, upstream string, delay time.Duration, bytesPerSecond int64, firstOnly bool, uploadDelay ...time.Duration) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var enabled atomic.Bool
	var accepted atomic.Int64
	var held atomic.Bool
	var mu sync.Mutex
	active := map[net.Conn]bool{}
	var workers sync.WaitGroup
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for c := range active {
			c.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			ordinal := accepted.Add(1)
			mu.Lock()
			active[client] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				backend, err := net.DialTimeout("tcp", upstream, 3*time.Second)
				if err != nil {
					client.Close()
					return
				}
				mu.Lock()
				active[backend] = true
				mu.Unlock()
				copied := make(chan struct{})
				go func() {
					defer backend.Close()
					defer close(copied)
					if len(uploadDelay) == 0 {
						io.Copy(backend, client)
						return
					}
					buf := make([]byte, 32<<10)
					for {
						n, readErr := client.Read(buf)
						if n > 0 {
							if enabled.Load() && held.CompareAndSwap(false, true) {
								time.Sleep(uploadDelay[0])
							}
							if _, err := backend.Write(buf[:n]); err != nil {
								return
							}
						}
						if readErr != nil {
							return
						}
					}
				}()
				defer func() {
					client.Close()
					backend.Close()
					<-copied
					mu.Lock()
					delete(active, client)
					delete(active, backend)
					mu.Unlock()
				}()
				var queued bytes.Buffer
				initialReady := true
				for {
					header := make([]byte, 5)
					if _, err := io.ReadFull(backend, header); err != nil {
						return
					}
					length := binary.BigEndian.Uint32(header[1:])
					if length < 4 || length > 16<<20 {
						return
					}
					body := make([]byte, int(length)-4)
					if _, err := io.ReadFull(backend, body); err != nil {
						return
					}
					if header[0] == 'Z' && initialReady {
						initialReady = false
						if enabled.Load() && len(uploadDelay) > 1 {
							time.Sleep(uploadDelay[1])
						}
					}
					if !enabled.Load() || (firstOnly && ordinal != 1) {
						if _, err = client.Write(append(header, body...)); err != nil {
							return
						}
						continue
					}
					queued.Write(header)
					queued.Write(body)
					if header[0] == 'Z' {
						transfer := time.Duration(0)
						if bytesPerSecond > 0 {
							transfer = time.Duration(int64(queued.Len()) * int64(time.Second) / bytesPerSecond)
						}
						time.Sleep(delay + transfer)
						if _, err = client.Write(queued.Bytes()); err != nil {
							return
						}
						queued.Reset()
					}
				}
			}()
		}
	}()
	return listener.Addr().String(), func() { enabled.Store(true) }
}

func TestPgAdminHealthWithinBudgetWithDatabaseRTT(t *testing.T) {
	f := healthFixture(t)
	cfg := f.conn.Config()
	address, enable := delayedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 75*time.Millisecond)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "1")
	u.RawQuery = q.Encode()
	st, err := store.Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }, AdminToken: "test-admin"})
	request := func(ctx context.Context) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer test-admin")
		return req
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, request(context.Background()))
	if warm.Code != http.StatusOK {
		t.Fatalf("database warmup status=%d: %s", warm.Code, warm.Body.String())
	}
	enable()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("75ms database RTT health status=%d after %s; want 200 within 200ms: %s", rec.Code, elapsed, rec.Body.String())
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("75ms database RTT health took %s; want <=200ms", elapsed)
	}
	contracttest.Check(t, http.MethodGet, "/api/v1/admin/health", rec)
	var got api.AdminHealth
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 3 || got.Chains[0].Head == nil || *got.Chains[0].Head != 1000 || got.Chains[0].BacklogBlocks == nil || *got.Chains[0].BacklogBlocks != 10 {
		t.Fatalf("health progress=%+v; want three configured chains and exact BSC head/backlog 1000/10", got.Chains)
	}
	t.Logf("exact health with 75ms database RTT returned in %s", elapsed)
}
