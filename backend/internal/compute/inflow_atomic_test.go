package compute_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/parse"
)

// H4-9 uses one-statement readers at the head+rows persistence boundary. A
// committed snapshot contains exactly the old 1 row or the new 3 rows.
func TestProjectionHeadSwitchIsAtomic(t *testing.T) {
	s, c := open(t)
	now := day.Add(18 * time.Hour)
	apply(t, s, "bsc", 1, now.Add(-10*time.Minute), parse.Facts{})
	apply(t, s, "bsc", 2, now.Add(-time.Minute), h4Facts(h4Trade(1, 1, "1000000")))
	apply(t, s, "bsc", 3, now.Add(time.Second), parse.Facts{})
	if err := s.RefreshStats(t.Context(), "bsc", now); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshCoverage(t.Context(), "bsc"); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshSmTrades(t.Context(), "bsc", now); err != nil {
		t.Fatal(err)
	}
	old, err := s.RebuildInflow(t.Context(), "bsc", "5m", now)
	if err != nil {
		t.Fatal(err)
	}
	f := parse.Facts{}
	for i, tail := range []byte{81, 82} {
		tr := h4Trade(i+1, 1, "1000000")
		tr.Token = h4Key(tail)
		tr.Pool = h4Key(tail + 10)
		six := int16(6)
		f.Tokens = append(f.Tokens, parse.Token{Address: tr.Token, Symbol: fmt.Sprint(tail), Decimals: &six, Created: true})
		f.Pools = append(f.Pools, parse.Pool{ID: tr.Pool, Token: tr.Token, Quote: h4Key(99), Dex: "test", Protocol: "test"})
		f.Trades = append(f.Trades, tr)
	}
	apply(t, s, "bsc", 4, now.Add(30*time.Second), f)
	apply(t, s, "bsc", 5, now.Add(time.Minute+time.Second), parse.Facts{})
	if err = s.RefreshStats(t.Context(), "bsc", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = s.RefreshCoverage(t.Context(), "bsc"); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshSmTrades(t.Context(), "bsc", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	firstRead := make(chan struct{}, 1)
	readErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			var count int
			err := c.QueryRow(t.Context(), `SELECT count(r.token_address) FROM projection_heads h JOIN inflow_rows r
   ON r.projection_version=h.projection_version AND r.chain=h.chain AND r."window"=h."window"
   WHERE h.name='inflow' AND h.chain='bsc' AND h."window"='5m'`).Scan(&count)
			select {
			case firstRead <- struct{}{}:
			default:
			}
			if err != nil || (count != 1 && count != 3) {
				readErr <- fmt.Errorf("concurrent committed rows=%d error=%v, want 1 or 3", count, err)
				return
			}
		}
	}()
	<-firstRead
	current, err := s.RebuildInflow(t.Context(), "bsc", "5m", now.Add(time.Minute))
	close(stop)
	wg.Wait()
	close(readErr)
	for e := range readErr {
		t.Error(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !current.Rebuilt || current.Version == old.Version || current.Rows != 3 {
		t.Fatalf("new version=%+v, old=%+v; want changed full 3 rows", current, old)
	}
	if got := one(t, c, `SELECT count(*)::text FROM inflow_rows WHERE projection_version=$1`, old.Version); got != "1" {
		t.Fatalf("old retained rows=%s, want 1", got)
	}
	unchanged, err := s.RebuildInflow(t.Context(), "bsc", "5m", now.Add(15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Rebuilt || unchanged.Version != current.Version {
		t.Fatalf("same as_of should skip rebuild: %+v", unchanged)
	}
	if got := one(t, c, `SELECT count(*)::text FROM inflow_rows WHERE projection_version=$1`, old.Version); got != "1" {
		t.Fatalf("old rows before 15m retirement=%s, want 1", got)
	}
	if _, err = s.RebuildInflow(t.Context(), "bsc", "5m", now.Add(16*time.Minute+time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM inflow_rows WHERE projection_version=$1`, old.Version); got != "0" {
		t.Fatalf("old rows after 15m retirement=%s, want 0", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM inflow_rows WHERE projection_version=$1`, current.Version); got != "3" {
		t.Fatalf("current rows after pruning=%s, want 3", got)
	}
}

// H4-9c is a regression guard for the approved CopyFrom refactor. A trigger
// fails after the first new row, so the check covers a partly consumed copy.
func TestInflowCopyFailureRollsBackWholeVersion(t *testing.T) {
	s, c := open(t)
	now := day.Add(18 * time.Hour)
	apply(t, s, "bsc", 1, now.Add(-2*time.Hour), parse.Facts{})
	apply(t, s, "bsc", 2, now.Add(-time.Minute), h4Facts(h4Trade(1, 1, "1000000")))
	apply(t, s, "bsc", 3, now.Add(time.Second), parse.Facts{})
	for _, err := range []error{s.RefreshStats(t.Context(), "bsc", now), s.RefreshSmTrades(t.Context(), "bsc", now), s.RefreshCoverage(t.Context(), "bsc")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	old, err := s.RebuildInflow(t.Context(), "bsc", "1h", now)
	if err != nil {
		t.Fatal(err)
	}
	f := parse.Facts{}
	for i, tail := range []byte{81, 82} {
		tr := h4Trade(i+1, 1, "1000000")
		tr.Token = h4Key(tail)
		tr.Pool = h4Key(tail + 10)
		six := int16(6)
		f.Tokens = append(f.Tokens, parse.Token{Address: tr.Token, Decimals: &six, Created: true})
		f.Pools = append(f.Pools, parse.Pool{ID: tr.Pool, Token: tr.Token, Quote: h4Key(99), Dex: "test", Protocol: "test"})
		f.Trades = append(f.Trades, tr)
	}
	apply(t, s, "bsc", 4, now.Add(30*time.Second), f)
	apply(t, s, "bsc", 5, now.Add(time.Minute+time.Second), parse.Facts{})
	for _, err := range []error{s.RefreshStats(t.Context(), "bsc", now.Add(time.Minute)), s.RefreshSmTrades(t.Context(), "bsc", now.Add(time.Minute)), s.RefreshCoverage(t.Context(), "bsc")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = c.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION reject_partial_copy() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.projection_version<>%d AND EXISTS(SELECT 1 FROM inflow_rows WHERE projection_version=NEW.projection_version) THEN
 RAISE EXCEPTION 'h4 copy fixture mid-stream failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_partial_copy BEFORE INSERT ON inflow_rows FOR EACH ROW EXECUTE FUNCTION reject_partial_copy()`, old.Version))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RebuildInflow(t.Context(), "bsc", "1h", now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "h4 copy fixture mid-stream failure") {
		t.Fatalf("mid-copy error=%v", err)
	}
	if got := one(t, c, `SELECT projection_version::text FROM projection_heads WHERE chain='bsc' AND "window"='1h'`); got != fmt.Sprint(old.Version) {
		t.Fatalf("head changed on failed copy: %s", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM inflow_rows WHERE projection_version<>$1`, old.Version); got != "0" {
		t.Fatalf("partial rows left after rollback=%s", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM inflow_rows WHERE projection_version=$1 AND retired_at IS NULL`, old.Version); got != "1" {
		t.Fatalf("old complete version lost=%s", got)
	}
}
