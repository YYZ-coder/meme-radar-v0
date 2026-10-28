package pgapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/meme-radar/radar/internal/api"
)

// Regression supplements: only committed chain_blocks changes are observable
// in health. No assertions depend on counter tables, shards or trigger calls.
func TestPgHealthPendingCountsFollowTransactions(t *testing.T) {
	f := newFixture(t)
	if err := f.st.SaveParserStatus(t.Context(), "bsc", 1, []string{"fixture"}, f.now); err != nil {
		t.Fatal(err)
	}
	_, err := f.conn.Exec(t.Context(), `INSERT INTO chain_blocks(chain,block_num,block_hash,block_time,status,ingested_at,parsed_version)
 VALUES('bsc',1,'\x01',now(),'confirmed',now(),NULL),('bsc',2,'\x02',now(),'pending',now(),NULL),
 ('bsc',3,NULL,NULL,'skipped',now(),NULL),('bsc',4,'\x04',now(),'confirmed',now(),1),('bsc',5,'\x05',now(),'confirmed',now(),NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	h := f.handler()
	check := func(want int64) {
		t.Helper()
		var got api.AdminHealth
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
		req.Header.Set("Authorization", "Bearer test-admin")
		h.ServeHTTP(r, req)
		if r.Code != 200 {
			t.Fatalf("health=%d %s", r.Code, r.Body.String())
		}
		if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Parsers) != 1 || got.Parsers[0].LagBlocks == nil || *got.Parsers[0].LagBlocks != want {
			t.Fatalf("pending=%+v want%d", got.Parsers, want)
		}
	}
	check(3)
	apply := func(tx pgx.Tx) {
		t.Helper()
		for _, q := range []string{
			`UPDATE chain_blocks SET parsed_version=1 WHERE chain='bsc' AND block_num=1`,
			`UPDATE chain_blocks SET status='skipped',block_hash=NULL,block_time=NULL WHERE chain='bsc' AND block_num=2`,
			`UPDATE chain_blocks SET status='confirmed',block_hash='\x03',block_time=now() WHERE chain='bsc' AND block_num=3`,
			`UPDATE chain_blocks SET parsed_version=NULL WHERE chain='bsc' AND block_num=4`,
			`DELETE FROM chain_blocks WHERE chain='bsc' AND block_num=5`,
			`INSERT INTO chain_blocks(chain,block_num,status,block_hash,block_time,ingested_at)VALUES('bsc',1,'confirmed','\x01',now(),now()) ON CONFLICT(chain,block_num) DO UPDATE SET parsed_version=NULL`,
			`INSERT INTO chain_blocks(chain,block_num,status,block_hash,block_time,ingested_at)VALUES('bsc',6,'confirmed','\x06',now(),now())`,
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
	check(3)
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(3)
	tx, err = f.conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	apply(tx)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(4)
	// Repeated writer updates do not count the same pending block twice.
	if _, err = f.conn.Exec(t.Context(), `UPDATE chain_blocks SET status=status,parsed_version=parsed_version;DELETE FROM chain_blocks WHERE chain='bsc' AND block_num IN(3,6)`); err != nil {
		t.Fatal(err)
	}
	check(2)
	if _, err = f.conn.Exec(t.Context(), `TRUNCATE chain_blocks CASCADE`); err != nil {
		t.Fatal(err)
	}
	check(0)
}

// Simulate an existing installation at migration21. Applying migration22 must
// backfill current data exactly once and keep subsequent writer changes exact.
func TestPgHealthPendingMigrationBackfillsExistingLedger(t *testing.T) {
	f := newFixture(t)
	_, err := f.conn.Exec(t.Context(), `DROP TRIGGER parser_pending_insert ON chain_blocks;
 DROP TRIGGER parser_pending_delete ON chain_blocks;DROP TRIGGER parser_pending_update ON chain_blocks;DROP TRIGGER parser_pending_truncate ON chain_blocks;
 DROP FUNCTION maintain_parser_pending_statement();DROP FUNCTION maintain_parser_pending_update();DROP FUNCTION clear_parser_pending_counts();
 DROP TABLE parser_pending_counts;DELETE FROM schema_migrations WHERE name='0022_health_pending_counts.sql';
 INSERT INTO chain_blocks(chain,block_num,block_hash,block_time,status,ingested_at,parsed_version)
 VALUES('bsc',1,'\x01',now(),'confirmed',now(),NULL),('bsc',2,'\x02',now(),'confirmed',now(),1),('bsc',3,NULL,NULL,'skipped',now(),NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.SaveParserStatus(t.Context(), "bsc", 1, []string{"fixture"}, f.now); err != nil {
		t.Fatal(err)
	}
	if err = f.st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	check := func(want int64) {
		t.Helper()
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
		req.Header.Set("Authorization", "Bearer test-admin")
		f.handler().ServeHTTP(r, req)
		if r.Code != 200 {
			t.Fatalf("health=%d %s", r.Code, r.Body.String())
		}
		var got api.AdminHealth
		if err = json.Unmarshal(r.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Parsers) != 1 || got.Parsers[0].LagBlocks == nil || *got.Parsers[0].LagBlocks != want {
			t.Fatalf("migrated pending=%+v want%d", got.Parsers, want)
		}
	}
	check(1)
	if _, err = f.conn.Exec(t.Context(), `UPDATE chain_blocks SET parsed_version=1 WHERE chain='bsc' AND block_num=1`); err != nil {
		t.Fatal(err)
	}
	check(0)
	if err = f.st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(0)
}
