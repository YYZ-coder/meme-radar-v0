-- PancakeSwap Infinity pool keys (CLPoolManager.poolIdToPoolKey) are cached
-- like token lookups; address holds the 32-byte PoolId.
ALTER TABLE token_lookups DROP CONSTRAINT token_lookups_kind_check;
ALTER TABLE token_lookups ADD CONSTRAINT token_lookups_kind_check CHECK (kind IN ('four_meme', 'erc20', 'infinity_pool'));
