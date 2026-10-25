-- Doppler Airlock.getAssetData answers are cached like other lookups.
ALTER TABLE token_lookups DROP CONSTRAINT token_lookups_kind_check;
ALTER TABLE token_lookups ADD CONSTRAINT token_lookups_kind_check CHECK (kind IN ('four_meme', 'erc20', 'infinity_pool', 'doppler_asset'));
