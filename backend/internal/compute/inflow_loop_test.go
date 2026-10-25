package compute_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/meme-radar/radar/internal/compute"
	"github.com/meme-radar/radar/internal/metrics"
	"github.com/meme-radar/radar/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

type inflowPublication struct {
	chain, window string
	at            time.Time
}
type recordingInflow struct {
	mu        sync.Mutex
	published []inflowPublication
	active    bool
	overlap   bool
}

func (s *recordingInflow) RebuildInflow(ctx context.Context, chain, window string, at time.Time) (store.ProjectionBuild, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		s.overlap = true
	}
	s.active = true
	s.published = append(s.published, inflowPublication{chain, window, at})
	s.active = false
	return store.ProjectionBuild{AsOf: at, Rebuilt: true, Rows: 1}, nil
}

// H4-12b/38 uses the public scheduling/persistence seam and a virtual clock.
// Ten chain/window starts occupy hand-written 0/0.5/.../4.5s phases.
// Each phase still repeats every 5s (short) or 15s (long), per D153.
func TestInflowLoopUsesIndependentWindowSchedulesAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &recordingInflow{}
		intervals := map[string]time.Duration{"1m": 5 * time.Second, "5m": 5 * time.Second, "1h": 15 * time.Second, "4h": 15 * time.Second, "24h": 15 * time.Second}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		started := time.Now()
		go func() { done <- compute.NewInflow(s, []string{"bsc", "base"}, intervals, nil).Run(ctx) }()
		synctest.Wait()
		time.Sleep(19500 * time.Millisecond)
		synctest.Wait()
		cancel()
		synctest.Wait()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("stop=%v", err)
		}
		byWindow := map[string][]time.Duration{}
		for _, v := range s.published {
			byWindow[v.chain+"/"+v.window] = append(byWindow[v.chain+"/"+v.window], v.at.Sub(started))
		}
		want := map[string][]time.Duration{
			"bsc/1m":   {0, 5000 * time.Millisecond, 10000 * time.Millisecond, 15000 * time.Millisecond},
			"base/1m":  {500 * time.Millisecond, 5500 * time.Millisecond, 10500 * time.Millisecond, 15500 * time.Millisecond},
			"bsc/5m":   {1000 * time.Millisecond, 6000 * time.Millisecond, 11000 * time.Millisecond, 16000 * time.Millisecond},
			"base/5m":  {1500 * time.Millisecond, 6500 * time.Millisecond, 11500 * time.Millisecond, 16500 * time.Millisecond},
			"bsc/1h":   {2000 * time.Millisecond, 17000 * time.Millisecond},
			"base/1h":  {2500 * time.Millisecond, 17500 * time.Millisecond},
			"bsc/4h":   {3000 * time.Millisecond, 18000 * time.Millisecond},
			"base/4h":  {3500 * time.Millisecond, 18500 * time.Millisecond},
			"bsc/24h":  {4000 * time.Millisecond, 19000 * time.Millisecond},
			"base/24h": {4500 * time.Millisecond, 19500 * time.Millisecond},
		}
		if !reflect.DeepEqual(byWindow, want) {
			t.Errorf("publication cutoffs=%v; want staggered D153 schedules %v", byWindow, want)
		}
		if s.overlap {
			t.Error("projection writes overlapped")
		}
	})
}

// H4-12c observes the promised monitoring boundary, not private loop calls.
func TestInflowLoopReportsPublishedRowsAndDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var m dto.Metric
		metrics.InflowRebuildSeconds.WithLabelValues("bsc", "1m").(prometheus.Metric).Write(&m)
		count := m.GetHistogram().GetSampleCount()
		intervals := map[string]time.Duration{"1m": 5 * time.Second, "5m": 5 * time.Second, "1h": 15 * time.Second, "4h": 15 * time.Second, "24h": 15 * time.Second}
		s := &recordingInflow{}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- compute.NewInflow(s, []string{"bsc"}, intervals, nil).Run(ctx) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		<-done
		if got := testutil.ToFloat64(metrics.InflowRows.WithLabelValues("bsc", "1m")); got != 1 {
			t.Errorf("published row gauge=%v; want 1", got)
		}
		metrics.InflowRebuildSeconds.WithLabelValues("bsc", "1m").(prometheus.Metric).Write(&m)
		if got := m.GetHistogram().GetSampleCount() - count; got != 1 {
			t.Errorf("published duration samples=%d; want 1", got)
		}
	})
}
