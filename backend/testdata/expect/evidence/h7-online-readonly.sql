-- H7 pre-deployment source check; no DDL or data writes.
\pset pager off
\timing on
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '60s';
SELECT current_timestamp AT TIME ZONE 'UTC' AS observed_at_utc,
       (SELECT max(name) FROM schema_migrations) AS latest_migration,
       to_regclass('parser_status') AS parser_status,
       to_regclass('parse_retries') AS parse_retries;
SELECT chain,head,anchor,position,greatest(head-position,0) AS forward_lag,
       trunc(extract(epoch FROM (current_timestamp-last_block_time))) AS lag_seconds
FROM stream_cursors WHERE stream='forward' ORDER BY chain;
SELECT chain,reason,count(*) AS open_gap_rows,sum(to_pos-from_pos+1) AS open_gap_blocks
FROM stream_gaps WHERE status<>'filled' GROUP BY chain,reason ORDER BY chain,reason;
SELECT chain,count(*) FILTER (WHERE status<>'skipped' AND parsed_version IS NULL) AS unparsed,
       max(block_num) FILTER (WHERE parsed_version IS NOT NULL) AS parser_position
FROM chain_blocks GROUP BY chain ORDER BY chain;
SELECT chain,decoder,count(*) AS failures_24h
FROM decode_failures WHERE created_at>=current_timestamp-interval '24 hours' AND created_at<current_timestamp
GROUP BY chain,decoder ORDER BY chain,decoder;
SELECT id,chain,block_num,tx_index,decoder,created_at
FROM decode_failures ORDER BY created_at DESC,id DESC LIMIT 5;
ROLLBACK;
