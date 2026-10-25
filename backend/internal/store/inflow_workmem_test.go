package store

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meme-radar/radar/internal/testdb"
)

// H4-37 observes the publication statement's PostgreSQL execution context. The
// independent contract is 64MB in the transaction, with the caller's 6MB session
// setting restored after either commit or rollback; another session is unchanged.
func TestInflowWorkMemIsTransactionLocal(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "commit"
		if failure {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			url := testdb.New(t)
			cfg, err := pgxpool.ParseConfig(url)
			if err != nil {
				t.Fatal(err)
			}
			cfg.MaxConns = 1
			cfg.ConnConfig.RuntimeParams["work_mem"] = "6MB"
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			s := &Store{pool: pool}
			defer s.Close()
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			other, err := pgx.Connect(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close(ctx)
			var otherBefore string
			if err := other.QueryRow(ctx, "SHOW work_mem").Scan(&otherBefore); err != nil {
				t.Fatal(err)
			}
			_, err = pool.Exec(ctx, `CREATE FUNCTION inspect_publication_work_mem() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
 IF current_setting('work_mem') <> '64MB' THEN
  RAISE EXCEPTION 'publication work_mem=%, want 64MB',current_setting('work_mem');
 END IF;
 IF TG_ARGV[0]='fail' THEN RAISE EXCEPTION 'h4 work_mem forced rollback'; END IF;
 RETURN NULL;
 END $$`)
			if err != nil {
				t.Fatal(err)
			}
			arg := "success"
			if failure {
				arg = "fail"
			}
			if _, err := pool.Exec(ctx, "CREATE TRIGGER inspect_publication_work_mem AFTER INSERT ON inflow_rows FOR EACH STATEMENT EXECUTE FUNCTION inspect_publication_work_mem('"+arg+"')"); err != nil {
				t.Fatal(err)
			}
			result, err := s.RebuildInflow(ctx, "bsc", "24h", time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC))
			if failure {
				if err == nil || !strings.Contains(err.Error(), "h4 work_mem forced rollback") {
					t.Errorf("rollback publication error=%v; want forced rollback after 64MB check", err)
				}
			} else if err != nil || !result.Rebuilt {
				t.Errorf("publication=%+v error=%v; want successful publication with 64MB", result, err)
			}
			var restored, otherAfter string
			if err := pool.QueryRow(ctx, "SHOW work_mem").Scan(&restored); err != nil {
				t.Fatal(err)
			}
			if err := other.QueryRow(ctx, "SHOW work_mem").Scan(&otherAfter); err != nil {
				t.Fatal(err)
			}
			if restored != "6MB" || otherAfter != otherBefore {
				t.Errorf("work_mem leaked: reused session=%q want 6MB; other session=%q want %q", restored, otherAfter, otherBefore)
			}
		})
	}
}
