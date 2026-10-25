package compute

import (
	"bytes"
	"context"
	"time"

	"github.com/meme-radar/radar/internal/store"
)

// Market keeps tokens' main pools (D55) and current market state.
type Market struct {
	store  *store.Store
	chains []ChainNative
}

func NewMarket(s *store.Store, chains []ChainNative) *Market {
	return &Market{store: s, chains: chains}
}

// Step re-chooses the main pool of every token traded in the last 24 hours,
// then refreshes their price, market cap and liquidity. An incremental step
// (full false) only picks a first main pool for new tokens and refreshes
// tokens whose trades were priced since the last step.
func (m *Market) Step(ctx context.Context, now time.Time, full bool) error {
	for _, c := range m.chains {
		rows, err := m.store.MainPoolCandidates(ctx, c.Chain, now, !full)
		if err != nil {
			return err
		}
		var changes []store.MainPoolChange
		for i := 0; i < len(rows); {
			j := i
			var cands []Candidate
			for ; j < len(rows) && bytes.Equal(rows[j].Token, rows[i].Token); j++ {
				r := rows[j]
				cands = append(cands, Candidate{Pool: r.Pool, GraduatedTo: r.GraduatedTo, Status: r.Status, Liquidity1h: r.Liquidity1h, Volume1h: r.Volume1h})
			}
			r := rows[i]
			cur := MainState{Pool: r.Main, Candidate: r.Candidate}
			if r.CandidateSince != nil {
				cur.CandidateSince = *r.CandidateSince
			}
			next, reason := ChooseMainPool(cur, cands, now)
			if reason != "" || !bytes.Equal(next.Candidate, cur.Candidate) || !next.CandidateSince.Equal(cur.CandidateSince) {
				ch := store.MainPoolChange{Token: r.Token, From: r.Main, Pool: next.Pool, Candidate: next.Candidate, Reason: reason}
				if next.Candidate != nil {
					ch.CandidateSince = &next.CandidateSince
				}
				changes = append(changes, ch)
			}
			i = j
		}
		if err := m.store.SaveMainPools(ctx, c.Chain, changes, now); err != nil {
			return err
		}
		if err := m.store.UpdateMarket(ctx, c.Chain, now, !full); err != nil {
			return err
		}
	}
	return nil
}
