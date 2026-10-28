-- Exact pending-block totals share the writers' transaction and survive
-- retries, rollback and reorg deletion. Shards avoid a hot row shared by all
-- ingest/parse connections. A shard may be negative; only its chain sum is a count.
SET LOCAL lock_timeout = '2s';
LOCK TABLE chain_blocks IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE parser_pending_counts (
 chain chain_id NOT NULL,
 shard smallint NOT NULL CHECK (shard >= 0 AND shard < 64),
 pending bigint NOT NULL,
 PRIMARY KEY(chain,shard)
);

INSERT INTO parser_pending_counts(chain,shard,pending)
 SELECT chain,0,count(*) FROM chain_blocks
 WHERE status<>'skipped' AND parsed_version IS NULL GROUP BY chain;

-- A transition table aggregates each bulk insert/delete once per chain, rather
-- than updating a counter for every row in a backfill or a reorg.
CREATE FUNCTION maintain_parser_pending_statement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO parser_pending_counts(chain,shard,pending)
 SELECT chain,(pg_backend_pid()%64)::smallint,TG_ARGV[0]::bigint*count(*)
 FROM changed WHERE status<>'skipped' AND parsed_version IS NULL
 GROUP BY chain ORDER BY chain
 ON CONFLICT(chain,shard) DO UPDATE SET pending=parser_pending_counts.pending+excluded.pending;
 RETURN NULL;
END
$$;
CREATE TRIGGER parser_pending_insert AFTER INSERT ON chain_blocks
 REFERENCING NEW TABLE AS changed FOR EACH STATEMENT
 EXECUTE FUNCTION maintain_parser_pending_statement('1');
CREATE TRIGGER parser_pending_delete AFTER DELETE ON chain_blocks
 REFERENCING OLD TABLE AS changed FOR EACH STATEMENT
 EXECUTE FUNCTION maintain_parser_pending_statement('-1');

CREATE FUNCTION maintain_parser_pending_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 old_pending boolean := OLD.status<>'skipped' AND OLD.parsed_version IS NULL;
 new_pending boolean := NEW.status<>'skipped' AND NEW.parsed_version IS NULL;
BEGIN
 IF old_pending=new_pending AND OLD.chain=NEW.chain THEN RETURN NULL; END IF;
 INSERT INTO parser_pending_counts(chain,shard,pending)
 SELECT chain,(pg_backend_pid()%64)::smallint,sum(delta)::bigint
 FROM (VALUES(OLD.chain,-old_pending::integer),(NEW.chain,new_pending::integer))d(chain,delta)
 GROUP BY chain HAVING sum(delta)<>0 ORDER BY chain
 ON CONFLICT(chain,shard) DO UPDATE SET pending=parser_pending_counts.pending+excluded.pending;
 RETURN NULL;
END
$$;
-- Balance-only updates do not execute the trigger. Repeated parses and status
-- writes with unchanged values also avoid counter writes.
CREATE TRIGGER parser_pending_update AFTER UPDATE OF chain,status,parsed_version ON chain_blocks
 FOR EACH ROW WHEN(OLD.chain IS DISTINCT FROM NEW.chain OR OLD.status IS DISTINCT FROM NEW.status OR OLD.parsed_version IS DISTINCT FROM NEW.parsed_version)
 EXECUTE FUNCTION maintain_parser_pending_update();

CREATE FUNCTION clear_parser_pending_counts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM parser_pending_counts;
 RETURN NULL;
END
$$;
CREATE TRIGGER parser_pending_truncate AFTER TRUNCATE ON chain_blocks
 FOR EACH STATEMENT EXECUTE FUNCTION clear_parser_pending_counts();
