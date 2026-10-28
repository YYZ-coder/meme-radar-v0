package pgapi_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

// No token has been queried before this request. A delayed initial packet on
// one path must not leave the healthy125ms PostgreSQL path too little of the
// public200ms budget. Prepared statements contain no primed token responses.
func TestPgColdDetailWithinBudgetWithSlowInitialPacket(t *testing.T) {
	f := detailFixture(t, false)
	cfg := f.conn.Config()
	address, enable := limitedPostgres(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), 125*time.Millisecond, 0, false, 250*time.Millisecond)
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Host = address
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "4")
	u.RawQuery = q.Encode()
	st, err := store.Open(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	b, err := pgapi.NewCached(pgapi.New(st, f.cfg, func() time.Time { return f.now }), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	h := httpapi.New(b, httpapi.Options{})
	enable()
	before := databaseClock(t, f)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/bsc/0x0000000000000000000000000000000000000061", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, r)
	elapsed := time.Since(start)
	if rec.Code != 200 || elapsed > 200*time.Millisecond {
		t.Fatalf("first token with one delayed initial packet=%d in%s; want200 <=200ms:%s", rec.Code, elapsed, rec.Body.String())
	}
	var got api.TokenDetail
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Volume24h == nil || *got.Volume24h != "400" || len(got.Windows) != 4 || len(got.SameName) != 2 {
		t.Fatalf("exact H1 token changed:%+v", got)
	}
	for i, win := range got.Windows {
		want := "400"
		if i == 0 {
			want = "350"
		}
		if win.Volume == nil || *win.Volume != want || win.Traders == nil || *win.Traders != 3 {
			t.Fatalf("window%d changed:%+v", i, win)
		}
	}
	at, err := time.Parse(time.RFC3339Nano, rec.Header().Get("X-Data-As-Of"))
	if err != nil || at.Before(before) || at.After(databaseClock(t, f)) || time.Since(at) > time.Second {
		t.Fatalf("actual read time invalid:%q", rec.Header().Get("X-Data-As-Of"))
	}
	t.Logf("first exact token observation returned in%s", elapsed)
}
