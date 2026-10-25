package compute_test

import (
	"testing"
	"time"

	"github.com/meme-radar/radar/internal/compute"
)

func liq(v float64) *float64 { return &v }

func TestChooseMainPoolFollowsD55(t *testing.T) {
	t0 := day.Add(12 * time.Hour)
	a, b, c := []byte("A"), []byte("B"), []byte("C")
	type step struct {
		name   string
		cur    compute.MainState
		cands  []compute.Candidate
		at     time.Time
		pool   string // main pool after the step
		cand   string // pending challenger after the step
		reason string // switch reason, "" when unchanged
	}
	steps := []step{
		{name: "first pick is the most liquid", cands: []compute.Candidate{{Pool: a, Status: "active", Liquidity1h: liq(100)}, {Pool: b, Status: "active", Liquidity1h: liq(300)}},
			at: t0, pool: "B", reason: "liquidity"},
		{name: "a 25% lead starts the 10-minute timer", cur: compute.MainState{Pool: a},
			cands: []compute.Candidate{{Pool: a, Status: "active", Liquidity1h: liq(100)}, {Pool: b, Status: "active", Liquidity1h: liq(125)}},
			at:    t0, pool: "A", cand: "B"},
		{name: "after 9 minutes still waiting", cur: compute.MainState{Pool: a, Candidate: b, CandidateSince: t0},
			cands: []compute.Candidate{{Pool: a, Status: "active", Liquidity1h: liq(100)}, {Pool: b, Status: "active", Liquidity1h: liq(125)}},
			at:    t0.Add(9 * time.Minute), pool: "A", cand: "B"},
		{name: "after 10 minutes the challenger wins", cur: compute.MainState{Pool: a, Candidate: b, CandidateSince: t0},
			cands: []compute.Candidate{{Pool: a, Status: "active", Liquidity1h: liq(100)}, {Pool: b, Status: "active", Liquidity1h: liq(125)}},
			at:    t0.Add(10 * time.Minute), pool: "B", reason: "liquidity"},
		{name: "a lead under 20% clears the timer", cur: compute.MainState{Pool: a, Candidate: b, CandidateSince: t0},
			cands: []compute.Candidate{{Pool: a, Status: "active", Liquidity1h: liq(100)}, {Pool: b, Status: "active", Liquidity1h: liq(119)}},
			at:    t0.Add(11 * time.Minute), pool: "A"},
		{name: "a new leader restarts the timer", cur: compute.MainState{Pool: a, Candidate: b, CandidateSince: t0},
			cands: []compute.Candidate{{Pool: a, Status: "active", Liquidity1h: liq(100)}, {Pool: b, Status: "active", Liquidity1h: liq(130)}, {Pool: c, Status: "active", Liquidity1h: liq(200)}},
			at:    t0.Add(12 * time.Minute), pool: "A", cand: "C"},
		{name: "a graduated curve switches at once to its new pool", cur: compute.MainState{Pool: a},
			cands: []compute.Candidate{{Pool: a, Status: "graduated", GraduatedTo: c, Liquidity1h: liq(900)}, {Pool: b, Status: "active", Liquidity1h: liq(500)}, {Pool: c, Status: "active", Liquidity1h: liq(10)}},
			at:    t0, pool: "C", reason: "graduation"},
		{name: "unknown liquidity falls back to 1h volume", cur: compute.MainState{},
			cands: []compute.Candidate{{Pool: a, Status: "active", Liquidity1h: liq(1000), Volume1h: 5}, {Pool: b, Status: "active", Volume1h: 50}},
			at:    t0, pool: "B", reason: "liquidity"},
		{name: "no active pool keeps the main pool", cur: compute.MainState{Pool: a},
			cands: []compute.Candidate{{Pool: a, Status: "graduated"}}, at: t0, pool: "A"},
	}
	for _, s := range steps {
		next, reason := compute.ChooseMainPool(s.cur, s.cands, s.at)
		if string(next.Pool) != s.pool || string(next.Candidate) != s.cand || reason != s.reason {
			t.Errorf("%s: main=%s candidate=%s reason=%q; want main=%s candidate=%s reason=%q",
				s.name, next.Pool, next.Candidate, reason, s.pool, s.cand, s.reason)
		}
		if s.cand != "" && string(s.cur.Candidate) != s.cand && !next.CandidateSince.Equal(s.at) {
			t.Errorf("%s: candidate since %v; want %v", s.name, next.CandidateSince, s.at)
		}
	}
}
