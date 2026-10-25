package compute_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/lists"
	"github.com/meme-radar/radar/internal/parse"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/meme-radar/radar/internal/store"
)

func h4QueuedThird(t *testing.T, s *store.Store, now time.Time) (store.ListJob, []store.ListMember) {
	t.Helper()
	b := pgapi.New(s, config.Config{}, func() time.Time { return now })
	r, err := b.CreateImport(t.Context(), "third.csv", []byte("chain,address,label,tags,twitter\nbsc,0x0000000000000000000000000000000000000003,Third,watch,@third\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ApplyImport(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	j, ok, err := s.ClaimListBuild(t.Context(), now)
	if err != nil || !ok {
		t.Fatalf("claim third=%+v %v %v", j, ok, err)
	}
	parent, _, err := s.ListBuildInput(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	label, twitter := "Third", "@third"
	return j, append(parent, store.ListMember{Chain: "bsc", Address: h4Key(3), Label: &label, Tags: []string{"watch"}, Twitter: &twitter, Sources: []string{"import"}, AddedAt: j.CreatedAt})
}

// H4-3a reads committed preparation from the independent connection returned
// by open. The 47h/49h cutoffs and row counts are hand-counted from the facts.
func TestListBuildBackfills48Hours(t *testing.T) {
	s, c := open(t)
	now := day.Add(13 * time.Hour)
	h4Members(t, s, now, false)
	apply(t, s, "bsc", 1, now.Add(-49*time.Hour), h4Facts(h4Trade(1, 3, "1000000")))
	apply(t, s, "bsc", 2, now.Add(-47*time.Hour), parse.Facts{Trades: []parse.Trade{h4Trade(1, 3, "1000000")}})
	apply(t, s, "bsc", 3, now.Add(-2*time.Minute), parse.Facts{Trades: []parse.Trade{h4Trade(1, 1, "1000000")}})
	step := compute.NewSmTrades(s, []string{"bsc"})
	if err := step.Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	j, members := h4QueuedThird(t, s, now)
	if err := s.PrepareListBuild(t.Context(), j, members, now); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=3 AND wallet=$1`, h4Key(3)); got != "1" {
		t.Fatalf("before activation: 47h member trades=%s, want 1", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=3 AND block_num=1`); got != "0" {
		t.Fatalf("49h trades=%s, want 0", got)
	}
	if got := one(t, c, `SELECT status||' '||(prepared_at IS NOT NULL)::text FROM sm_lists WHERE version=3`); got != "building true" {
		t.Fatalf("prepared state=%s, want building true", got)
	}
	// Read both tables in one statement while Prepare atomically replaces them.
	stop := make(chan struct{})
	firstRead := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	readErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			var n, m int
			err := c.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM sm_list_members WHERE version=3),(SELECT count(*) FROM sm_trades WHERE version=3)`).Scan(&n, &m)
			select {
			case firstRead <- struct{}{}:
			default:
			}
			if err != nil || n != 3 || m != 2 {
				readErr <- fmt.Errorf("during repeated prepare members/trades=%d/%d err=%v, want 3/2", n, m, err)
				return
			}
		}
	}()
	<-firstRead
	err := s.PrepareListBuild(t.Context(), j, members, now)
	close(stop)
	wg.Wait()
	close(readErr)
	for e := range readErr {
		t.Error(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_list_members WHERE version=3`); got != "3" {
		t.Fatalf("idempotent members=%s, want 3", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=3`); got != "2" {
		t.Fatalf("idempotent trades=%s, want 2", got)
	}
	apply(t, s, "bsc", 4, now.Add(-time.Minute), parse.Facts{Trades: []parse.Trade{h4Trade(1, 3, "2000000")}})
	if err := step.Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=3 AND block_num=4`); got != "1" {
		t.Fatalf("new building trade before activation=%s, want 1", got)
	}
	if err := s.ActivateListBuild(t.Context(), j, members, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=3`); got != "3" {
		t.Fatalf("activated trades=%s, want 3", got)
	}
	if err := step.Step(t.Context(), now.Add(29*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=2`); got != "1" {
		t.Fatalf("retired trades before 30m=%s, want 1", got)
	}
	if err := step.Step(t.Context(), now.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=2`); got != "0" {
		t.Fatalf("retired trades after 30m=%s, want 0", got)
	}
}

// H4-3b's database trigger checks the precondition at the real worker's
// activation boundary, rather than mocking its internal method calls.
func TestListWorkerPreparesBeforeActivation(t *testing.T) {
	s, c := open(t)
	now := day.Add(13 * time.Hour)
	_, err := c.Exec(t.Context(), `CREATE FUNCTION require_prepared() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  IF NEW.status='active' AND OLD.status='building' AND OLD.prepared_at IS NULL THEN
   RAISE EXCEPTION 'missing committed preparation'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER require_prepared BEFORE UPDATE ON sm_lists FOR EACH ROW EXECUTE FUNCTION require_prepared()`)
	if err != nil {
		t.Fatal(err)
	}
	b := pgapi.New(s, config.Config{}, func() time.Time { return now })
	r, err := b.CreateImport(t.Context(), "worker.csv", []byte("chain,address\nbsc,0x0000000000000000000000000000000000000001\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ApplyImport(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err = lists.New(s, nil).Step(t.Context(), now); err != nil {
		t.Fatalf("worker should commit preparation before activation: %v", err)
	}
	if got := one(t, c, `SELECT status||' '||(prepared_at IS NOT NULL)::text FROM sm_lists WHERE version=2`); got != "active true" {
		t.Fatalf("worker published state=%s, want active true", got)
	}
}

// Regression equivalence for the user's frozen ActivateListBuild signature:
// identical input includes every nullable, array and timestamp member field.
func TestPreparedAndLegacyMemberSnapshotsMatch(t *testing.T) {
	var snapshots []string
	for _, prepared := range []bool{false, true} {
		s, c := open(t)
		now := day.Add(13 * time.Hour)
		h4Members(t, s, now, false)
		j, members := h4QueuedThird(t, s, now)
		members[0].Excluded = true
		members[1].Tags = []string{"alpha", "beta"}
		members[1].Sources = []string{"import", "automatic"}
		if prepared {
			if err := s.PrepareListBuild(t.Context(), j, members, now); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ActivateListBuild(t.Context(), j, members, now); err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, one(t, c, `SELECT jsonb_agg(to_jsonb(m)-'version' ORDER BY chain,address)::text FROM sm_list_members m WHERE version=3`))
	}
	if snapshots[0] != snapshots[1] {
		t.Fatalf("legacy/prepared snapshots differ:\n%s\n%s", snapshots[0], snapshots[1])
	}
}

// H4-3c: a failed prepared build is excluded immediately from incremental
// membership, then its trade copies expire 30 minutes after failure.
func TestFailedPreparedBuildIsIsolatedAndExpires(t *testing.T) {
	s, c := open(t)
	now := day.Add(13 * time.Hour)
	h4Members(t, s, now, false)
	apply(t, s, "bsc", 1, now.Add(-time.Minute), h4Facts(h4Trade(1, 3, "1000000")))
	step := compute.NewSmTrades(s, []string{"bsc"})
	if err := step.Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	j, members := h4QueuedThird(t, s, now)
	if err := s.PrepareListBuild(t.Context(), j, members, now); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareListBuild(t.Context(), j, members, now.Add(61*time.Second)); err != store.ErrJobLeaseLost {
		t.Fatalf("expired lease=%v, want ErrJobLeaseLost", err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_list_members WHERE version=3`); got != "3" {
		t.Fatalf("expired lease mutated members=%s, want 3", got)
	}
	if err := s.FailListBuild(t.Context(), j, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	apply(t, s, "bsc", 2, now.Add(-30*time.Second), parse.Facts{Trades: []parse.Trade{h4Trade(1, 3, "2000000")}})
	if err := step.Step(t.Context(), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=3 AND block_num=2`); got != "0" {
		t.Fatalf("failed build accepted new trade=%s, want 0", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=3`); got != "1" {
		t.Fatalf("retained failed copies before 30m=%s, want 1", got)
	}
	if err := step.Step(t.Context(), now.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_trades WHERE version=3`); got != "0" {
		t.Fatalf("failed build copies after 30m=%s, want 0", got)
	}
}

// The existing H3 worker failure path must contain a real SQL backfill error;
// old active and published head stay unchanged, and partial preparation rolls back.
func TestListBuildBackfillFailureLeavesActiveAndHeads(t *testing.T) {
	s, c := open(t)
	now := day.Add(13 * time.Hour)
	h4Members(t, s, now, false)
	apply(t, s, "bsc", 1, now.Add(-time.Minute), h4Facts(h4Trade(1, 3, "1000000")))
	_, err := c.Exec(t.Context(), `INSERT INTO projection_heads VALUES('inflow','bsc','5m',1,$1,2,$1,false)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Exec(t.Context(), `CREATE FUNCTION reject_sm_copy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected backfill failure'; END $$;
 CREATE TRIGGER reject_sm_copy BEFORE INSERT ON sm_trades FOR EACH ROW EXECUTE FUNCTION reject_sm_copy()`)
	if err != nil {
		t.Fatal(err)
	}
	b := pgapi.New(s, config.Config{}, func() time.Time { return now })
	r, err := b.CreateImport(t.Context(), "failure.csv", []byte("chain,address\nbsc,0x0000000000000000000000000000000000000003\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ApplyImport(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err = lists.New(s, nil).Step(t.Context(), now); err == nil {
		t.Fatal("backfill error should fail build")
	}
	if got := one(t, c, `SELECT status||' '||build_status FROM sm_lists WHERE version=3`); got != "building failed" {
		t.Fatalf("failed state=%s, want building failed", got)
	}
	if got := one(t, c, `SELECT version::text FROM sm_lists WHERE status='active'`); got != "2" {
		t.Fatalf("active after failure=%s, want 2", got)
	}
	if got := one(t, c, `SELECT list_version::text FROM projection_heads`); got != "2" {
		t.Fatalf("head after failure=%s, want 2", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM sm_list_members WHERE version=3`); got != "0" {
		t.Fatalf("rolled-back preparation members=%s, want 0", got)
	}
}

// H4-3d checks both effects of the activation hook in H3's existing transaction.
func TestListActivationSwitchesHeadsAndRollsBackOnFailure(t *testing.T) {
	s, c := open(t)
	now := day.Add(13 * time.Hour)
	h4Members(t, s, now, false)
	j, members := h4QueuedThird(t, s, now)
	if err := s.PrepareListBuild(t.Context(), j, members, now); err != nil {
		t.Fatal(err)
	}
	_, err := c.Exec(t.Context(), `INSERT INTO projection_heads VALUES('inflow','bsc','5m',1,$1,2,$1,false),('inflow','base','1h',2,$1,2,$1,false)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Exec(t.Context(), `CREATE FUNCTION reject_head_switch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.list_version=3 THEN RAISE EXCEPTION 'injected activation failure';END IF;RETURN NEW;END $$;
 CREATE TRIGGER reject_head_switch BEFORE UPDATE ON projection_heads FOR EACH ROW EXECUTE FUNCTION reject_head_switch()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ActivateListBuild(t.Context(), j, members, now); err == nil {
		t.Fatal("activation should fail when transactional head switch fails")
	}
	if got := one(t, c, `SELECT version::text FROM sm_lists WHERE status='active'`); got != "2" {
		t.Fatalf("active after rollback=%s, want 2", got)
	}
	if got := one(t, c, `SELECT count(*)::text FROM projection_heads WHERE list_version=2 AND NOT rebuild_pending`); got != "2" {
		t.Fatalf("heads changed on failed activation=%s, want 2", got)
	}
	if _, err = c.Exec(t.Context(), `DROP TRIGGER reject_head_switch ON projection_heads`); err != nil {
		t.Fatal(err)
	}
	if err = s.ActivateListBuild(t.Context(), j, members, now); err != nil {
		t.Fatal(err)
	}
	if got := one(t, c, `SELECT count(*)::text FROM projection_heads WHERE list_version=3 AND rebuild_pending`); got != "2" {
		t.Fatalf("activated heads/rebuild requests=%s, want 2", got)
	}
}
