package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/contracttest"
	"github.com/meme-radar/radar/internal/httpapi"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
	"github.com/meme-radar/radar/internal/testdb"
)

func TestPgHealthz(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.New(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(pgapi.New(st, config.Config{}, nil), httpapi.Options{})
	check := func(t *testing.T, status int, want api.Health) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != status {
			t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
		}
		contracttest.Check(t, http.MethodGet, "/healthz", rec)
		var body api.Health
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body != want {
			t.Errorf("health = %+v, want %+v", body, want)
		}
		if got := rec.Header().Get("X-Radar-Mock"); got != "" {
			t.Errorf("PostgreSQL health carries mock header %q", got)
		}
	}
	t.Run("connected", func(t *testing.T) {
		check(t, http.StatusOK, api.Health{Status: "ok", DB: "ok"})
	})
	st.Close()
	t.Run("disconnected", func(t *testing.T) {
		check(t, http.StatusServiceUnavailable, api.Health{Status: "degraded", DB: "unavailable"})
	})
}
