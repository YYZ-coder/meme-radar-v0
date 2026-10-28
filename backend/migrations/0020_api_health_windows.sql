-- Health reads exact open-gap counts by chain/reason and recent coverage
-- windows. Keep these reads on narrow covering indexes instead of revisiting
-- all historical range rows and gap heap pages for each configured chain.
CREATE INDEX stream_gaps_health_open
    ON stream_gaps(chain, reason) WHERE status <> 'filled';
CREATE INDEX coverage_ranges_health_window
    ON coverage_ranges(chain, to_block) INCLUDE (from_block);
