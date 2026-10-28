-- Health reads the latest parsed position independently from the pending count.
-- The existing chain_blocks_unparsed index covers the latter; this index keeps
-- an ever-growing parsed history out of the request path without changing its
-- exact progress/NULL semantics. Both remain transactionally current on reorgs.
CREATE INDEX chain_blocks_parsed_position ON chain_blocks (chain, block_num DESC)
    WHERE parsed_version IS NOT NULL;
