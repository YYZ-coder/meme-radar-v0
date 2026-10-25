package store

import (
	"context"
	"fmt"
	"time"
)

// APISmartHolder is one positive holder in the published membership snapshot.
type APISmartHolder struct {
	Owner                       []byte
	Name, Amount, SharePct      *string
	BuyVolumeUSD, SellVolumeUSD *string
	Dev                         bool
	LastTradeAt                 *time.Time
}

// APISmartMoney contains the list head and holders from one statement snapshot.
// Known distinguishes a known empty balance view from missing balance coverage.
type APISmartMoney struct {
	Version int64
	Known   bool
	Rows    []APISmartHolder
}

// APISmartMoney joins balances only to active, non-excluded same-chain members.
func (s *Store) APISmartMoney(ctx context.Context, chain string, token []byte, now time.Time) (APISmartMoney, error) {
	rows, err := s.pool.Query(ctx, `WITH active AS MATERIALIZED (SELECT version FROM sm_lists WHERE status='active'),
   coverage AS (SELECT EXISTS(SELECT 1 FROM coverage_ranges cr WHERE cr.chain=$1
    AND cr.from_time <= $3::timestamptz-interval '24 hours' AND cr.to_time >= $3) AS complete),
   volumes AS (
    SELECT st.wallet,
     CASE WHEN count(*) FILTER(WHERE st.side='buy' AND st.value_usd IS NULL)=0
      THEN coalesce(sum(st.value_usd) FILTER(WHERE st.side='buy'),0) END AS buy,
     CASE WHEN count(*) FILTER(WHERE st.side='sell' AND st.value_usd IS NULL)=0
      THEN coalesce(sum(st.value_usd) FILTER(WHERE st.side='sell'),0) END AS sell
    FROM sm_trades st JOIN active USING(version)
    WHERE st.chain=$1 AND st.token_address=$2
     AND st.block_time >= $3::timestamptz-interval '24 hours' AND st.block_time < $3
    GROUP BY st.wallet
   )
   SELECT active.version,coalesce(h.coverage<>'unknown',false),
    b.owner,b.label,trim_scale(b.balance*10::numeric^(-t.supply_decimals))::text,
    trim_scale(b.balance*10::numeric^(-t.supply_decimals)*100/nullif(m.circulating_supply,0))::text,
    coalesce(b.owner=t.creator,false),last_trade.block_time,
    CASE WHEN coverage.complete THEN CASE WHEN v.wallet IS NULL THEN '0' ELSE trim_scale(v.buy)::text END END,
    CASE WHEN coverage.complete THEN CASE WHEN v.wallet IS NULL THEN '0' ELSE trim_scale(v.sell)::text END END
   FROM token_info t CROSS JOIN active CROSS JOIN coverage
   LEFT JOIN token_holder_state h ON h.chain=t.chain AND h.token_address=t.address
   LEFT JOIN token_market m ON m.chain=t.chain AND m.address=t.address
   LEFT JOIN LATERAL (
    SELECT tb.owner,tb.balance,sm.label FROM sm_list_members sm
    JOIN token_balances tb ON tb.chain=sm.chain AND tb.owner=sm.address AND tb.token_address=t.address
    WHERE sm.version=active.version AND sm.chain=t.chain AND NOT sm.excluded
     AND tb.balance>0 AND is_holder_owner(tb.chain,tb.owner)
    ORDER BY tb.balance DESC,tb.owner
   )b ON true
   LEFT JOIN volumes v ON v.wallet=b.owner
   LEFT JOIN LATERAL (
    SELECT tr.block_time FROM trades tr
    WHERE tr.chain=t.chain AND tr.token_address=t.address AND tr.wallet=b.owner
     AND tr.block_time >= $3::timestamptz-interval '30 days' AND tr.block_time < $3
    ORDER BY tr.block_time DESC LIMIT 1
   )last_trade ON true
   WHERE t.chain=$1 AND t.address=$2 ORDER BY b.balance DESC,b.owner`, chain, token, now)
	if err != nil {
		return APISmartMoney{}, fmt.Errorf("store: API smart-money holders: %w", err)
	}
	defer rows.Close()
	out := APISmartMoney{Rows: []APISmartHolder{}}
	for rows.Next() {
		var row APISmartHolder
		if err := rows.Scan(&out.Version, &out.Known, &row.Owner, &row.Name, &row.Amount, &row.SharePct, &row.Dev, &row.LastTradeAt, &row.BuyVolumeUSD, &row.SellVolumeUSD); err != nil {
			return APISmartMoney{}, fmt.Errorf("store: API smart-money holders: %w", err)
		}
		if row.Owner != nil {
			out.Rows = append(out.Rows, row)
		}
	}
	if err := rows.Err(); err != nil {
		return APISmartMoney{}, fmt.Errorf("store: API smart-money holders: %w", err)
	}
	return out, nil
}
