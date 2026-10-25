package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

var apiTime = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

type fixture struct {
	st   *store.Store
	conn *pgx.Conn
	cfg  config.Config
	now  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	url := testdb.New(t)
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{st: st, conn: conn, cfg: cfg, now: apiTime.Add(30 * time.Second)}
}

func (f *fixture) handler() http.Handler {
	return httpapi.New(pgapi.New(f.st, f.cfg, func() time.Time { return f.now }), httpapi.Options{Now: func() time.Time { return f.now }, AdminToken: "test-admin"})
}

func getPG(t *testing.T, h http.Handler, route, target string, status int, out any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != status {
		t.Fatalf("GET %s = %d, want %d: %s", target, rec.Code, status, rec.Body.String())
	}
	contracttest.Check(t, http.MethodGet, route, rec)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return rec
}

// raw is an independently specified all-zero key with one terminal byte.
func raw(size int, tail byte) []byte {
	b := make([]byte, size)
	b[size-1] = tail
	return b
}

// apply mirrors the parse writer; no fixture facts are inserted with ad hoc SQL.
func (f *fixture) apply(t *testing.T, chain string, num uint64, at time.Time, facts parse.Facts) {
	t.Helper()
	ctx := context.Background()
	if err := f.st.Commit(ctx, store.Commit{Chain: chain, Block: store.Block{Num: num, Hash: raw(32, byte(num)), ParentHash: raw(32, byte(num-1)), Time: at, Status: store.StatusConfirmed, TxCount: 1, Payload: []byte(`{}`), Source: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.ApplyParsed(ctx, store.Parsed{Chain: chain, Num: num, Time: at, Version: 1, Facts: facts}); err != nil {
		t.Fatal(err)
	}
}
