package compute_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/config"
	"github.com/meme-radar/radar/internal/lists"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/pgapi"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// H3-3a: the real Cycle processes list jobs even during a price-source outage.
func TestCycleBuildsListJobs(t *testing.T) {
	st, conn := open(t)
	now := day.Add(time.Hour)
	b := pgapi.New(st, config.Config{}, func() time.Time { return now })
	report, err := b.CreateImport(t.Context(), "list.csv", []byte("chain,address\nbsc,0x0000000000000000000000000000000000000061\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ApplyImport(t.Context(), report.ID); err != nil {
		t.Fatal(err)
	}
	before := testutil.ToFloat64(metrics.ListBuilds.WithLabelValues("activated"))
	cy := compute.NewCycle(compute.CycleConfig{Store: st, Prices: downSource{}, FullEvery: time.Minute})
	_ = cy.Run(t.Context(), now) // The separate price outage remains reportable.
	var active int
	if err = conn.QueryRow(t.Context(), `SELECT version FROM sm_lists WHERE status='active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 2 {
		t.Fatalf("active after Cycle=%d, want 2", active)
	}
	if delta := testutil.ToFloat64(metrics.ListBuilds.WithLabelValues("activated")) - before; delta != 1 {
		t.Fatalf("activated metric increment=%v, want 1", delta)
	}
}

// H3-6: three accepted changes retain their chain and are published one per
// cycle; the backlog is visible while successors wait behind their parents.
func TestQueuedVersionsBuildInOrder(t *testing.T) {
	st, conn := open(t)
	now := day.Add(2 * time.Hour)
	b := pgapi.New(st, config.Config{}, func() time.Time { return now })
	report, err := b.CreateImport(t.Context(), "list.csv", []byte("chain,address\nbsc,0x0000000000000000000000000000000000000061\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ApplyImport(t.Context(), report.ID); err != nil {
		t.Fatal(err)
	}
	if err = lists.New(st, nil).Step(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000061", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Restore(t.Context(), api.BSC, "0x0000000000000000000000000000000000000061"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Exclude(t.Context(), api.BSC, "0x0000000000000000000000000000000000000061", "last"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ version, parent string }{{"3", ""}, {"4", "3"}, {"5", "4"}} {
		got, err := b.ListBuild(t.Context(), tc.version)
		if err != nil {
			t.Fatal(err)
		}
		if tc.parent == "" {
			if got.BlockingVersion != nil {
				t.Fatalf("ready child's blocker=%v", got.BlockingVersion)
			}
		} else if got.BlockingVersion == nil || *got.BlockingVersion != tc.parent {
			t.Fatalf("version %s blocker=%v, want %s", tc.version, got.BlockingVersion, tc.parent)
		}
	}
	cy := compute.NewCycle(compute.CycleConfig{Store: st, Prices: downSource{}, FullEvery: time.Minute})
	for i := 0; i < 3; i++ {
		_ = cy.Run(t.Context(), now.Add(time.Duration(i)*time.Minute))
		var active, queued int
		if err = conn.QueryRow(t.Context(), `SELECT (SELECT version FROM sm_lists WHERE status='active'),(SELECT count(*) FROM jobs WHERE status='queued')`).Scan(&active, &queued); err != nil {
			t.Fatal(err)
		}
		if active != 3+i || queued != 2-i {
			t.Fatalf("cycle %d active/queued=%d/%d, want %d/%d", i, active, queued, 3+i, 2-i)
		}
		if got := testutil.ToFloat64(metrics.JobsQueued.WithLabelValues("list_build")); got != float64(2-i) {
			t.Fatalf("cycle %d backlog metric=%v, want %d", i, got, 2-i)
		}
	}
	status, err := b.ListBuild(t.Context(), "3")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "retired" || status.BuildStatus != "activated" || status.BlockingVersion != nil {
		t.Fatalf("retired status=%+v", status)
	}
}
