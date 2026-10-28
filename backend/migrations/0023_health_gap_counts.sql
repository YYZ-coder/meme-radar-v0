-- Exact open-gap counts follow the gap writer's transaction. Aggregate bulk
-- transitions once per chain/reason; shards disperse concurrent writer locks.
SET LOCAL lock_timeout = '2s';
LOCK TABLE stream_gaps IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE health_gap_counts (
 chain chain_id NOT NULL,
 reason text NOT NULL,
 shard smallint NOT NULL CHECK(shard>=0 AND shard<64),
 open_count bigint NOT NULL,
 PRIMARY KEY(chain,reason,shard)
);
INSERT INTO health_gap_counts(chain,reason,shard,open_count)
 SELECT chain,reason,0,count(*) FROM stream_gaps WHERE status<>'filled' GROUP BY chain,reason;

CREATE FUNCTION maintain_health_gap_statement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO health_gap_counts(chain,reason,shard,open_count)
 SELECT chain,reason,(pg_backend_pid()%64)::smallint,TG_ARGV[0]::bigint*count(*)
 FROM changed WHERE status<>'filled' GROUP BY chain,reason ORDER BY chain,reason
 ON CONFLICT(chain,reason,shard) DO UPDATE SET open_count=health_gap_counts.open_count+excluded.open_count;
 RETURN NULL;
END
$$;
CREATE TRIGGER health_gap_insert AFTER INSERT ON stream_gaps
 REFERENCING NEW TABLE AS changed FOR EACH STATEMENT EXECUTE FUNCTION maintain_health_gap_statement('1');
CREATE TRIGGER health_gap_delete AFTER DELETE ON stream_gaps
 REFERENCING OLD TABLE AS changed FOR EACH STATEMENT EXECUTE FUNCTION maintain_health_gap_statement('-1');

CREATE FUNCTION maintain_health_gap_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_open boolean := OLD.status<>'filled';new_open boolean := NEW.status<>'filled';
BEGIN
 IF old_open=new_open AND OLD.chain=NEW.chain AND OLD.reason=NEW.reason THEN RETURN NULL; END IF;
 INSERT INTO health_gap_counts(chain,reason,shard,open_count)
 SELECT chain,reason,(pg_backend_pid()%64)::smallint,sum(delta)::bigint
 FROM(VALUES(OLD.chain,OLD.reason,-old_open::integer),(NEW.chain,NEW.reason,new_open::integer))d(chain,reason,delta)
 GROUP BY chain,reason HAVING sum(delta)<>0 ORDER BY chain,reason
 ON CONFLICT(chain,reason,shard) DO UPDATE SET open_count=health_gap_counts.open_count+excluded.open_count;
 RETURN NULL;
END
$$;
CREATE TRIGGER health_gap_update AFTER UPDATE OF chain,reason,status ON stream_gaps
 FOR EACH ROW WHEN(OLD.chain IS DISTINCT FROM NEW.chain OR OLD.reason IS DISTINCT FROM NEW.reason OR OLD.status IS DISTINCT FROM NEW.status)
 EXECUTE FUNCTION maintain_health_gap_update();

CREATE FUNCTION clear_health_gap_counts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN DELETE FROM health_gap_counts;RETURN NULL;END
$$;
CREATE TRIGGER health_gap_truncate AFTER TRUNCATE ON stream_gaps
 FOR EACH STATEMENT EXECUTE FUNCTION clear_health_gap_counts();
