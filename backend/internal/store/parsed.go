package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/meme-radar/radar/internal/parse"
)

// Parsed is one block's decode result.
type Parsed struct {
	Chain   string
	Num     uint64
	Time    time.Time
	Version int // parser version written to chain_blocks.parsed_version
	Facts   parse.Facts
	HitTxs  int // transactions with at least one fact or failure
	Events  int
}

// ApplyParsed replaces the block's facts and marks it parsed, in one
// transaction. Tokens and pools are upserted: creation events set metadata,
// pool reserves only move to a later (block, tx, ix) position, and a
// graduated pool stays graduated.
func (s *Store) ApplyParsed(ctx context.Context, p Parsed) error {
	return s.ApplyParsedMany(ctx, []Parsed{p})
}

// ApplyParsedMany applies several blocks in one transaction; an error in any
// of them applies none.
func (s *Store) ApplyParsedMany(ctx context.Context, ps []Parsed) error {
	if len(ps) == 0 {
		return nil
	}
	for _, p := range ps {
		for _, table := range []string{"trades", "liquidity_events"} {
			if err := s.ensurePartition(ctx, table, p.Time); err != nil {
				return err
			}
		}
	}
	var b pgx.Batch
	// Lock the batch's ledger rows in block order first, so concurrent
	// PromoteConfirmed calls cannot deadlock with this transaction.
	nums := make([]int64, len(ps))
	for i, p := range ps {
		nums[i] = int64(p.Num)
	}
	b.Queue(`SELECT block_num FROM chain_blocks WHERE chain = $1 AND block_num = ANY($2) ORDER BY block_num FOR UPDATE`, ps[0].Chain, nums)
	for _, p := range ps {
		queueParsed(&b, p)
	}
	first, last := ps[0], ps[len(ps)-1]
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: apply parsed %s %d…%d: %w", first.Chain, first.Num, last.Num, err)
	}
	defer tx.Rollback(ctx)
	if err := tx.SendBatch(ctx, &b).Close(); err != nil {
		return fmt.Errorf("store: apply parsed %s %d…%d: %w", first.Chain, first.Num, last.Num, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: apply parsed %s %d…%d: %w", first.Chain, first.Num, last.Num, err)
	}
	return nil
}

// queueParsed adds one block's statements to b.
func queueParsed(b *pgx.Batch, p Parsed) {
	num := int64(p.Num)
	// Marking the block first locks its ledger row, so a concurrent
	// PromoteConfirmed either runs before (facts are written confirmed) or
	// waits and then promotes the facts written here.
	b.Queue(`UPDATE chain_blocks SET parsed_version = $3, parsed_at = now(), hit_txs = $4, events = $5
		WHERE chain = $1 AND block_num = $2`, p.Chain, num, p.Version, p.HitTxs, p.Events)
	b.Queue("DELETE FROM trades WHERE block_time = $1 AND chain = $2 AND block_num = $3", p.Time, p.Chain, num)
	b.Queue("DELETE FROM liquidity_events WHERE block_time = $1 AND chain = $2 AND block_num = $3", p.Time, p.Chain, num)
	b.Queue("DELETE FROM decode_failures WHERE chain = $1 AND block_num = $2", p.Chain, num)
	for _, t := range p.Facts.Tokens {
		var launchAt *time.Time
		var createdBlock *int64
		if t.Created {
			launchAt, createdBlock = &p.Time, &num
		}
		b.Queue(`INSERT INTO tokens (chain, address, symbol, name, decimals, total_supply, creator, launch_platform,
				launch_at, created_block, metadata_uri, first_seen_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
			ON CONFLICT (chain, address) DO UPDATE SET
				symbol = CASE WHEN $13 THEN EXCLUDED.symbol ELSE tokens.symbol END,
				name = CASE WHEN $13 THEN EXCLUDED.name ELSE tokens.name END,
				total_supply = COALESCE(EXCLUDED.total_supply, tokens.total_supply),
				creator = COALESCE(EXCLUDED.creator, tokens.creator),
				decimals = COALESCE(tokens.decimals, EXCLUDED.decimals),
				launch_platform = CASE WHEN $13 THEN EXCLUDED.launch_platform
					ELSE COALESCE(tokens.launch_platform, EXCLUDED.launch_platform) END,
				launch_at = COALESCE(EXCLUDED.launch_at, tokens.launch_at),
				created_block = COALESCE(EXCLUDED.created_block, tokens.created_block),
				metadata_uri = COALESCE(EXCLUDED.metadata_uri, tokens.metadata_uri),
				first_seen_at = LEAST(tokens.first_seen_at, EXCLUDED.first_seen_at),
				updated_at = now()`,
			p.Chain, t.Address, cleanText(t.Symbol, 32), cleanText(t.Name, 128), t.Decimals, null(t.TotalSupply), t.Creator,
			null(t.LaunchPlatform), launchAt, createdBlock, null(cleanText(t.URI, 2048)), p.Time, t.Created)
	}
	for _, pl := range p.Facts.Pools {
		var createdAt *time.Time
		var createdBlock, stateBlock *int64
		var stateTx, stateIx *int
		var stateAt *time.Time
		if pl.Created {
			createdAt, createdBlock = &p.Time, &num
		}
		if pl.ReserveToken != "" && pl.ReserveQuote != "" {
			stateBlock, stateTx, stateIx, stateAt = &num, &pl.Tx, &pl.Ix, &p.Time
		}
		b.Queue(`INSERT INTO pools (chain, pool_id, token_address, quote_asset, quote_symbol, protocol, dex, status,
				graduated_to, created_at, created_block, reserve_token, reserve_quote, state_block, state_tx, state_ix,
				state_at, first_seen_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, 'active'), $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, now())
			ON CONFLICT (chain, pool_id) DO UPDATE SET
				status = CASE WHEN pools.status = 'graduated' THEN pools.status ELSE COALESCE($8, pools.status) END,
				graduated_to = COALESCE(EXCLUDED.graduated_to, pools.graduated_to),
				quote_symbol = CASE WHEN pools.quote_symbol = '' THEN EXCLUDED.quote_symbol ELSE pools.quote_symbol END,
				created_at = COALESCE(EXCLUDED.created_at, pools.created_at),
				created_block = COALESCE(EXCLUDED.created_block, pools.created_block),
				reserve_token = CASE WHEN `+newer+` THEN EXCLUDED.reserve_token ELSE pools.reserve_token END,
				reserve_quote = CASE WHEN `+newer+` THEN EXCLUDED.reserve_quote ELSE pools.reserve_quote END,
				state_at = CASE WHEN `+newer+` THEN EXCLUDED.state_at ELSE pools.state_at END,
				state_tx = CASE WHEN `+newer+` THEN EXCLUDED.state_tx ELSE pools.state_tx END,
				state_ix = CASE WHEN `+newer+` THEN EXCLUDED.state_ix ELSE pools.state_ix END,
				state_block = CASE WHEN `+newer+` THEN EXCLUDED.state_block ELSE pools.state_block END,
				first_seen_at = LEAST(pools.first_seen_at, EXCLUDED.first_seen_at),
				updated_at = now()`,
			p.Chain, pl.ID, pl.Token, pl.Quote, pl.QuoteSymbol, pl.Protocol, pl.Dex, null(pl.Status), pl.GraduatedTo,
			createdAt, createdBlock, null(pl.ReserveToken), null(pl.ReserveQuote), stateBlock, stateTx, stateIx, stateAt, p.Time)
	}
	for _, t := range p.Facts.Trades {
		b.Queue(`INSERT INTO trades (block_time, chain, block_num, tx_index, ix_index, tx_hash, pool_id, token_address, wallet,
				attribution, side, token_amount, quote_amount, quote_symbol, price_native, value_native, confirmed, decoder, decoder_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, `+blockConfirmed+`, $17, $18)`,
			p.Time, p.Chain, num, t.Tx, t.Ix, t.TxHash, t.Pool, t.Token, t.Wallet, t.Attribution, t.Side,
			t.TokenAmount, t.QuoteAmount, t.QuoteSymbol, null(t.PriceNative), null(t.ValueNative), t.Decoder, t.Version)
	}
	for _, l := range p.Facts.Liquidity {
		b.Queue(`INSERT INTO liquidity_events (block_time, chain, block_num, tx_index, ix_index, tx_hash, pool_id, token_address,
				wallet, kind, token_amount, quote_amount, quote_symbol, value_native, confirmed, decoder, decoder_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, `+blockConfirmed+`, $15, $16)`,
			p.Time, p.Chain, num, l.Tx, l.Ix, l.TxHash, l.Pool, l.Token, l.Wallet, l.Kind, l.TokenAmount, l.QuoteAmount,
			l.QuoteSymbol, null(l.ValueNative), l.Decoder, l.Version)
	}
	for _, f := range p.Facts.Failures {
		b.Queue(`INSERT INTO decode_failures (chain, block_num, tx_index, tx_hash, decoder, error, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())`, p.Chain, num, f.Tx, f.TxHash, f.Decoder, cleanText(f.Err, 1000))
	}
}

// blockConfirmed reads the block's current status ($2 chain, $3 number).
const blockConfirmed = `(SELECT status <> 'pending' FROM chain_blocks WHERE chain = $2 AND block_num = $3)`

// newer is true when the incoming pool state is later than the stored one.
const newer = `(EXCLUDED.state_block IS NOT NULL AND (pools.state_block IS NULL OR
	(EXCLUDED.state_block, EXCLUDED.state_tx, EXCLUDED.state_ix) > (pools.state_block, pools.state_tx, pools.state_ix)))`

// UnparsedBlocks returns up to limit non-skipped blocks of chain not yet
// parsed, newest first, with their times and statuses.
func (s *Store) UnparsedBlocks(ctx context.Context, chain string, limit int) ([]Block, error) {
	rows, err := s.pool.Query(ctx, `SELECT block_num, block_time, status FROM chain_blocks
		WHERE chain = $1 AND status <> 'skipped' AND parsed_version IS NULL ORDER BY block_num DESC LIMIT $2`, chain, limit)
	if err != nil {
		return nil, fmt.Errorf("store: unparsed %s: %w", chain, err)
	}
	blocks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Block, error) {
		var b Block
		var n int64
		err := r.Scan(&n, &b.Time, &b.Status)
		b.Num = uint64(n)
		return b, err
	})
	if err != nil {
		return nil, fmt.Errorf("store: unparsed %s: %w", chain, err)
	}
	return blocks, nil
}

// ResetParsed marks blocks parsed by an older version as unparsed and
// returns how many were reset.
func (s *Store) ResetParsed(ctx context.Context, chain string, version int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE chain_blocks SET parsed_version = NULL
		WHERE chain = $1 AND parsed_version < $2`, chain, version)
	if err != nil {
		return 0, fmt.Errorf("store: reset parsed %s: %w", chain, err)
	}
	return tag.RowsAffected(), nil
}

// null maps "" to SQL NULL.
func null(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// cleanText applies D103: invalid UTF-8 replaced, control characters
// (including NUL, which PostgreSQL text rejects) removed, at most max runes.
func cleanText(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}
