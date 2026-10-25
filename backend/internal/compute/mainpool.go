package compute

import (
	"bytes"
	"time"
)

// MainState is a token's main pool and pending challenger (D55).
type MainState struct {
	Pool, Candidate []byte
	CandidateSince  time.Time
}

// Candidate is one of a token's pools with its selection scores.
type Candidate struct {
	Pool, GraduatedTo []byte
	Status            string
	Liquidity1h       *float64 // 1-hour average USD liquidity; nil when unknown
	Volume1h          float64  // USD volume of the last hour
}

const (
	switchLead = 1.2              // a challenger must lead by 20%
	switchHold = 10 * time.Minute // for this long
)

// ChooseMainPool applies D55 at time now; reason is "liquidity" or
// "graduation" when the main pool changes, "" otherwise. Pools are scored
// by 1-hour average liquidity, or by 1-hour volume when any active pool's
// liquidity is unknown.
func ChooseMainPool(cur MainState, cands []Candidate, now time.Time) (MainState, string) {
	var active []Candidate
	byLiquidity := true
	for _, c := range cands {
		if c.Status == "active" {
			active = append(active, c)
			byLiquidity = byLiquidity && c.Liquidity1h != nil
		}
	}
	if len(active) == 0 {
		return cur, ""
	}
	score := func(c Candidate) float64 {
		if byLiquidity {
			return *c.Liquidity1h
		}
		return c.Volume1h
	}
	best := active[0]
	for _, c := range active[1:] {
		if s, bs := score(c), score(best); s > bs || (s == bs && bytes.Compare(c.Pool, best.Pool) < 0) {
			best = c
		}
	}
	if cur.Pool == nil {
		return MainState{Pool: best.Pool}, "liquidity"
	}
	var current *Candidate
	for i := range active {
		if bytes.Equal(active[i].Pool, cur.Pool) {
			current = &active[i]
		}
	}
	if current == nil { // graduated, closed or gone: switch at once
		for _, c := range cands {
			if bytes.Equal(c.Pool, cur.Pool) && c.GraduatedTo != nil {
				for _, a := range active {
					if bytes.Equal(a.Pool, c.GraduatedTo) {
						return MainState{Pool: a.Pool}, "graduation"
					}
				}
			}
		}
		return MainState{Pool: best.Pool}, "graduation"
	}
	if bytes.Equal(best.Pool, cur.Pool) || score(best) < switchLead*score(*current) {
		return MainState{Pool: cur.Pool}, ""
	}
	if !bytes.Equal(cur.Candidate, best.Pool) {
		return MainState{Pool: cur.Pool, Candidate: best.Pool, CandidateSince: now}, ""
	}
	if now.Sub(cur.CandidateSince) >= switchHold {
		return MainState{Pool: best.Pool}, "liquidity"
	}
	return cur, ""
}
