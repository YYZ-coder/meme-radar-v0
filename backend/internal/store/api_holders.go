package store

import (
	"context"
	"fmt"
	"time"
)

// APIHolderPosition is the exact atomic balance and address at a page boundary.
type APIHolderPosition struct {
	Balance string `json:"balance"`
	Owner   []byte `json:"owner"`
}

// APIHolder is one positive, non-burn holder in whole token units.
type APIHolder struct {
	Owner      []byte
	Amount     string
	Atomic     string
	SharePct   *string
	Dev        bool
	SmartMoney bool
}

// APIHolders contains compute's coverage and a bounded keyset page.
type APIHolders struct {
	Total       *int64
	Coverage    string
	Since, AsOf *time.Time
	Rows        []APIHolder
}

// APIHolders reads the state and page in one snapshot. The supply projection is
// in whole units, so the numerator is scaled before computing a percentage.
func (s *Store) APIHolders(ctx context.Context, chain string, token []byte, after *APIHolderPosition, limit int, smartOnly bool) (APIHolders, error) {
	var amount *string
	var owner []byte
	if after != nil {
		amount = &after.Balance
		owner = after.Owner
	}
	rows, err := s.pool.Query(ctx, `SELECT
   CASE WHEN h.coverage<>'unknown' THEN CASE WHEN $6::boolean THEN (
      SELECT count(*) FROM token_balances tb JOIN sm_list_members sm
       ON sm.version=active.version AND sm.chain=tb.chain AND sm.address=tb.owner AND NOT sm.excluded
      WHERE tb.chain=t.chain AND tb.token_address=t.address AND tb.balance>0 AND is_holder_owner(tb.chain,tb.owner)
     ) ELSE h.holders END END,coalesce(h.coverage,'unknown'),h.since,h.as_of,
   b.owner,trim_scale(b.balance*10::numeric^(-t.supply_decimals))::text,
   trim_scale(b.balance)::text,
   trim_scale(b.balance*10::numeric^(-t.supply_decimals)*100/nullif(m.circulating_supply,0))::text,
   coalesce(b.owner=t.creator,false),b.member IS NOT NULL
  FROM token_info t
  LEFT JOIN token_holder_state h ON h.chain=t.chain AND h.token_address=t.address
  LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
  LEFT JOIN sm_lists active ON active.status='active'
  LEFT JOIN LATERAL (
   SELECT b.owner,b.balance,sm.address AS member FROM token_balances b
    LEFT JOIN sm_list_members sm ON sm.version=active.version AND sm.chain=b.chain AND sm.address=b.owner AND NOT sm.excluded
    WHERE b.chain=t.chain AND b.token_address=t.address
    AND b.balance>0 AND is_holder_owner(b.chain,b.owner)
    AND (NOT $6::boolean OR sm.address IS NOT NULL)
    AND ($3::numeric IS NULL OR b.balance<$3::numeric OR (b.balance=$3::numeric AND b.owner>$4::bytea))
   ORDER BY balance DESC,owner LIMIT $5
  )b ON true
  WHERE t.chain=$1 AND t.address=$2 ORDER BY b.balance DESC,b.owner`, chain, token, amount, owner, limit, smartOnly)
	if err != nil {
		return APIHolders{}, fmt.Errorf("store: API holders: %w", err)
	}
	defer rows.Close()
	out := APIHolders{Coverage: "unknown", Rows: []APIHolder{}}
	for rows.Next() {
		var row APIHolder
		var whole, atomic *string
		if err := rows.Scan(&out.Total, &out.Coverage, &out.Since, &out.AsOf, &row.Owner, &whole, &atomic, &row.SharePct, &row.Dev, &row.SmartMoney); err != nil {
			return APIHolders{}, fmt.Errorf("store: API holders: %w", err)
		}
		if row.Owner != nil && whole != nil && atomic != nil {
			row.Amount, row.Atomic = *whole, *atomic
			out.Rows = append(out.Rows, row)
		}
	}
	if err := rows.Err(); err != nil {
		return APIHolders{}, fmt.Errorf("store: API holders: %w", err)
	}
	return out, nil
}

// APITokenHoldings is compute's holder state and the observed developer share.
// A missing balance or supply stays unknown; it is never filled with zero.
type APITokenHoldings struct {
	Holders           *int64
	SMHolders         *int64
	Coverage          string
	Since, AsOf       *time.Time
	CreatorHoldingPct *string
}

// APITokenHoldings reads coverage, count and developer holdings in one snapshot.
func (s *Store) APITokenHoldings(ctx context.Context, chain string, token []byte) (APITokenHoldings, error) {
	var out APITokenHoldings
	err := s.pool.QueryRow(ctx, `SELECT CASE WHEN h.coverage<>'unknown' THEN h.holders END,
    coalesce(h.coverage,'unknown'),h.since,h.as_of,
    CASE WHEN h.coverage<>'unknown' THEN (
      SELECT count(*) FROM token_balances tb JOIN sm_list_members sm
       ON sm.version=active.version AND sm.chain=tb.chain AND sm.address=tb.owner AND NOT sm.excluded
      WHERE tb.chain=t.chain AND tb.token_address=t.address AND tb.balance>0 AND is_holder_owner(tb.chain,tb.owner)
    ) END,
    trim_scale(b.balance*10::numeric^(-t.supply_decimals)*100/nullif(m.circulating_supply,0))::text
   FROM token_info t
   LEFT JOIN sm_lists active ON active.status='active'
   LEFT JOIN token_holder_state h ON h.chain=t.chain AND h.token_address=t.address
   LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
   LEFT JOIN token_balances b ON b.chain=t.chain AND b.token_address=t.address AND b.owner=t.creator
   WHERE t.chain=$1 AND t.address=$2`, chain, token).Scan(&out.Holders, &out.Coverage, &out.Since, &out.AsOf, &out.SMHolders, &out.CreatorHoldingPct)
	if err != nil {
		return APITokenHoldings{}, fmt.Errorf("store: API token holdings: %w", err)
	}
	return out, nil
}
