package pgapi_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// A repeat health observation must not retransmit unchanged historical failure
// payloads over a constrained database path. Progress still comes from the DB.
func TestPgRepeatedHealthWithUnchangedFailuresWithinWireBudget(t *testing.T) {
	f := healthFixture(t)
	message := strings.Repeat("x", 256)
	_, err := f.conn.Exec(t.Context(), `INSERT INTO decode_failures(chain,block_num,tx_index,decoder,error,created_at) SELECT 'bsc',200+n,0,'four_meme',$1,$2::timestamptz-n*interval '1 second' FROM generate_series(1,50)n`, message, f.now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 125*time.Millisecond, 32<<10, false)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "1")
	u.RawQuery = q.Encode()
	st, err := store.Open(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err = st.WarmAPIReads(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(pgapi.New(st, f.cfg, func() time.Time { return f.now }), httpapi.Options{AdminToken: "test-admin"})
	request := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer test-admin")
		return r
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, request(t.Context()))
	if warm.Code != 200 {
		t.Fatalf("initial health=%d:%s", warm.Code, warm.Body.String())
	}
	_, err = f.conn.Exec(t.Context(), "UPDATE stream_cursors SET head=1001 WHERE chain='bsc' AND stream='forward'")
	if err != nil {
		t.Fatal(err)
	}
	enable()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, request(ctx))
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("repeated health over125ms/32KiBps database link=%d in%s; want200 <=200ms:%s", rec.Code, elapsed, rec.Body.String())
	}
	var got api.AdminHealth
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Chains[0].Head == nil || *got.Chains[0].Head != 1001 || got.Chains[0].BacklogBlocks == nil || *got.Chains[0].BacklogBlocks != 11 {
		t.Fatalf("must read newly committed progress, got%+v", got.Chains[0])
	}
	if len(got.Failures) != 50 || got.Failures[0].Position != "201/0" || got.Failures[49].Position != "250/0" {
		t.Fatalf("latest50 changed:%+v", got.Failures)
	}
	for _, failure := range got.Failures {
		if failure.Error != message || failure.Attempts != 1 || failure.Status != "failed" {
			t.Fatalf("exact failure payload changed:%+v", failure)
		}
	}
	t.Logf("fresh progress and all50 exact historical failures returned in%s", elapsed)
}

// Regression supplement: cached transport values must follow actual committed
// error content, retry state and deletions, while ignoring uncommitted writes.
func TestPgHealthFailureReuseFollowsCommittedChanges(t *testing.T) {
	f := healthFixture(t)
	id := f.failure(t, "bsc", 970, 1, "four_meme", f.now.Add(-time.Minute))
	h := httpapi.New(pgapi.New(f.st, f.cfg, func() time.Time { return f.now }), httpapi.Options{AdminToken: "test-admin"})
	read := func() api.AdminHealth {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
		r.Header.Set("Authorization", "Bearer test-admin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 200 {
			t.Fatalf("health=%d:%s", rec.Code, rec.Body.String())
		}
		var b api.AdminHealth
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	check := func(message, status string, attempts int64) {
		t.Helper()
		b := read()
		if len(b.Failures) != 1 || b.Failures[0].Error != message || b.Failures[0].Status != status || b.Failures[0].Attempts != attempts || b.Failures[0].Position != "970/1" {
			t.Fatalf("actual failure changed:%+v", b.Failures)
		}
	}
	check("independent fixture failure", "failed", 1)
	tx, err := f.conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "UPDATE decode_failures SET error='uncommitted' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	check("independent fixture failure", "failed", 1)
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(t.Context(), "UPDATE decode_failures SET error='committed replacement' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	check("committed replacement", "failed", 1)
	if _, err = f.st.QueueParseRetry(t.Context(), id, f.now); err != nil {
		t.Fatal(err)
	}
	check("committed replacement", "queued", 1)
	if _, err = f.conn.Exec(t.Context(), "UPDATE parse_retries SET done_at=$1 WHERE failure_id=$2", f.now, id); err != nil {
		t.Fatal(err)
	}
	check("committed replacement", "failed", 2)
	if _, err = f.conn.Exec(t.Context(), "DELETE FROM decode_failures WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got.Failures) != 0 {
		t.Fatalf("deleted failure retained:%+v", got.Failures)
	}
	if got := read(); len(got.Failures) != 0 {
		t.Fatalf("empty page changed:%+v", got.Failures)
	}
}

// BenchmarkHealthFailureObservation exercises fresh HTTP observations with the
// same50 full failure payloads; setup is excluded from the measured interval.
func BenchmarkHealthFailureObservation(b *testing.B) {
	f := newFixture(b)
	_, err := f.conn.Exec(b.Context(), `INSERT INTO decode_failures(chain,block_num,tx_index,decoder,error,created_at) SELECT 'bsc',200+n,0,'four_meme',$1,$2::timestamptz-n*interval '1 second' FROM generate_series(1,50)n`, strings.Repeat("x", 256), f.now)
	if err != nil {
		b.Fatal(err)
	}
	h := f.handler()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
		r.Header.Set("Authorization", "Bearer test-admin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 200 {
			b.Fatalf("health=%d:%s", rec.Code, rec.Body.String())
		}
	}
}
