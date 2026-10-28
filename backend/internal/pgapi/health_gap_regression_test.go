package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/meme-radar/radar/internal/api"
)

func checkGapHealth(t *testing.T, f *fixture, total int64, reasons map[string]int64) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
	req.Header.Set("Authorization", "Bearer test-admin")
	f.handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("health=%d %s", rec.Code, rec.Body.String())
	}
	var got api.AdminHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, c := range got.Chains {
		if c.Chain == api.BSC {
			if c.GapsOpen != total || !reflect.DeepEqual(c.GapsByReason, reasons) {
				t.Fatalf("gaps=%d/%v want%d/%v", c.GapsOpen, c.GapsByReason, total, reasons)
			}
			return
		}
	}
	t.Fatal("BSC missing from health")
}

// Regression supplements observe exact counts and reason maps over committed
// gap states. Counter tables and shard layout are not observable expectations.
func TestPgHealthGapCountsFollowTransactions(t *testing.T) {
	f := newFixture(t)
	_, err := f.conn.Exec(t.Context(), `INSERT INTO stream_gaps(chain,from_pos,to_pos,reason,status,created_at,updated_at)VALUES
 ('bsc',1,1,'startup','open',now(),now()),('bsc',3,3,'startup','filling',now(),now()),
 ('bsc',5,5,'rate_limited','open',now(),now()),('bsc',7,7,'startup','filled',now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	initial := map[string]int64{"startup": 2, "rate_limited": 1}
	checkGapHealth(t, f, 3, initial)
	apply := func(tx pgx.Tx) {
		t.Helper()
		for _, q := range []string{
			`UPDATE stream_gaps SET status='filled' WHERE chain='bsc' AND from_pos=1`,
			`UPDATE stream_gaps SET reason='rate_limited' WHERE chain='bsc' AND from_pos=3`,
			`DELETE FROM stream_gaps WHERE chain='bsc' AND from_pos=5`,
			`INSERT INTO stream_gaps(chain,from_pos,to_pos,reason,status,created_at,updated_at)VALUES('bsc',9,9,'rate_limited','open',now(),now())`,
		} {
			if _, err := tx.Exec(t.Context(), q); err != nil {
				t.Fatal(err)
			}
		}
	}
	tx, err := f.conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	apply(tx)
	checkGapHealth(t, f, 3, initial)
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkGapHealth(t, f, 3, initial)
	tx, err = f.conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	apply(tx)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkGapHealth(t, f, 2, map[string]int64{"rate_limited": 2})
	if _, err = f.conn.Exec(t.Context(), `UPDATE stream_gaps SET status=status,reason=reason;DELETE FROM stream_gaps`); err != nil {
		t.Fatal(err)
	}
	checkGapHealth(t, f, 0, map[string]int64{})
	if _, err = f.conn.Exec(t.Context(), `INSERT INTO stream_gaps(chain,from_pos,to_pos,reason,status,created_at,updated_at)VALUES('bsc',1,1,'fetch_failed','open',now(),now());TRUNCATE stream_gaps CASCADE`); err != nil {
		t.Fatal(err)
	}
	checkGapHealth(t, f, 0, map[string]int64{})
}

func TestPgHealthGapMigrationBackfillsExistingGaps(t *testing.T) {
	f := newFixture(t)
	_, err := f.conn.Exec(t.Context(), `DROP TRIGGER health_gap_insert ON stream_gaps;DROP TRIGGER health_gap_delete ON stream_gaps;DROP TRIGGER health_gap_update ON stream_gaps;DROP TRIGGER health_gap_truncate ON stream_gaps;
 DROP FUNCTION maintain_health_gap_statement();DROP FUNCTION maintain_health_gap_update();DROP FUNCTION clear_health_gap_counts();DROP TABLE health_gap_counts;
 DELETE FROM schema_migrations WHERE name='0023_health_gap_counts.sql';
 INSERT INTO stream_gaps(chain,from_pos,to_pos,reason,status,created_at,updated_at)VALUES('bsc',1,1,'startup','open',now(),now()),('bsc',3,3,'disconnect','filled',now(),now())`)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkGapHealth(t, f, 1, map[string]int64{"startup": 1})
	if _, err = f.conn.Exec(t.Context(), `UPDATE stream_gaps SET status='filled' WHERE chain='bsc' AND from_pos=1`); err != nil {
		t.Fatal(err)
	}
	checkGapHealth(t, f, 0, map[string]int64{})
	if err = f.st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkGapHealth(t, f, 0, map[string]int64{})
}
