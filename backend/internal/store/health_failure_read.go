package store

import (
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Only an exact signature read from this statement may reuse prior values.
// The digest includes every returned field and retry state, in response order.
// A null failure row means an unchanged page or an actually empty current page.
const healthFailureReadSQL = `WITH latest AS MATERIALIZED (
 SELECT ` + healthFailureColumns + ` FROM decode_failures f ORDER BY f.created_at DESC,f.id DESC LIMIT 50
 ), signature AS MATERIALIZED (
 SELECT encode(sha256(convert_to(coalesce(jsonb_agg(to_jsonb(latest) ORDER BY created_at DESC,id DESC)::text,'[]'),'UTF8')),'hex') AS version FROM latest
 ) SELECT CASE WHEN row_number() OVER (ORDER BY f.created_at DESC NULLS LAST,f.id DESC NULLS LAST)=1 THEN s.version ELSE '' END,f.id,f.chain,f.block_num,f.tx_index,f.decoder,f.error,f.created_at,f.attempts,f.queued
 FROM signature s LEFT JOIN latest f ON s.version<>$1
 ORDER BY f.created_at DESC NULLS LAST,f.id DESC NULLS LAST`

type healthFailureReadCache struct {
	mu      sync.Mutex
	version string
	values  []FailureHealth
}

func (c *healthFailureReadCache) snapshot() (string, []FailureHealth) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version, append([]FailureHealth(nil), c.values...)
}

func (c *healthFailureReadCache) publish(version string, values []FailureHealth) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version, c.values = version, append([]FailureHealth(nil), values...)
}

func collectCurrentFailures(rows pgx.Rows, cachedVersion string, cached []FailureHealth) ([]FailureHealth, string, error) {
	defer rows.Close()
	out := []FailureHealth{}
	version := ""
	hit := false
	for rows.Next() {
		var v string
		var id, block, attempts *int64
		var tx *int
		var chain, decoder, message *string
		var at *time.Time
		var queued *bool
		if err := rows.Scan(&v, &id, &chain, &block, &tx, &decoder, &message, &at, &attempts, &queued); err != nil {
			return nil, "", fmt.Errorf("store: health failure row: %w", err)
		}
		if v != "" {
			version = v
		}
		if id == nil {
			hit = v == cachedVersion
			continue
		}
		if chain == nil || block == nil || tx == nil || decoder == nil || message == nil || at == nil || attempts == nil || queued == nil {
			return nil, "", fmt.Errorf("store: incomplete health failure")
		}
		out = append(out, FailureHealth{ID: *id, Chain: *chain, BlockNum: *block, TxIndex: *tx, Decoder: *decoder, Error: *message, CreatedAt: *at, Attempts: *attempts, Queued: *queued})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: health failure page: %w", err)
	}
	if version == "" {
		return nil, "", fmt.Errorf("store: missing health failure signature")
	}
	if hit {
		out = append(out, cached...)
	}
	return out, version, nil
}
