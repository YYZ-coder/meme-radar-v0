-- H4: smart-money trade copies owned by compute.
CREATE TABLE sm_trades (
 version bigint NOT NULL, block_time timestamptz NOT NULL, chain chain_id NOT NULL, block_num bigint NOT NULL,
 tx_index integer NOT NULL, ix_index integer NOT NULL, token_address bytea NOT NULL, wallet bytea NOT NULL,
 side text NOT NULL, value_usd numeric, value_native numeric, is_large boolean,
 PRIMARY KEY(version,block_time,chain,block_num,tx_index,ix_index)
);
CREATE INDEX sm_trades_token ON sm_trades(version,chain,token_address,block_time);
CREATE INDEX sm_trades_time ON sm_trades(version,chain,block_time);
CREATE TABLE smtrades_dirty(chain chain_id NOT NULL,minute timestamptz NOT NULL,PRIMARY KEY(chain,minute));

-- H3's deployed migration remains immutable; H4 owns preparation readiness.
ALTER TABLE sm_lists ADD COLUMN prepared_at timestamptz;
CREATE SEQUENCE projection_version_seq;
CREATE TABLE projection_heads (
 name text NOT NULL, chain chain_id NOT NULL, "window" text NOT NULL, projection_version bigint NOT NULL,
 as_of timestamptz NOT NULL,list_version bigint NOT NULL,updated_at timestamptz NOT NULL,
 rebuild_pending boolean NOT NULL DEFAULT false,
 PRIMARY KEY(name,chain,"window")
);

CREATE TABLE inflow_rows (
 projection_version bigint NOT NULL,chain chain_id NOT NULL,"window" text NOT NULL,token_address bytea NOT NULL,
 symbol text NOT NULL DEFAULT '',name text NOT NULL DEFAULT '',image_url text,launch_platform text,launch_at timestamptz,
 price_usd numeric,price_native numeric,market_cap_usd numeric,market_cap_native numeric,
 price_change_pct numeric,price_change_pct_native numeric,liquidity_usd numeric,liquidity_native numeric,
 trades bigint,buys bigint,sells bigint,observed_trades bigint NOT NULL DEFAULT 0,observed_buys bigint NOT NULL DEFAULT 0,
 observed_sells bigint NOT NULL DEFAULT 0,coverage text NOT NULL DEFAULT 'partial',volume_usd numeric,volume_native numeric,
 net_inflow_usd numeric,net_inflow_native numeric,sm_holders bigint,sm_trades bigint,holders bigint,age_seconds bigint,
 dexes text[] NOT NULL DEFAULT '{}',socials jsonb NOT NULL DEFAULT '{}',sparkline numeric[] NOT NULL DEFAULT '{}',
 sparkline_native numeric[] NOT NULL DEFAULT '{}',dev_holding_pct numeric,
 retired_at timestamptz,
 PRIMARY KEY(projection_version,chain,"window",token_address)
);
CREATE INDEX inflow_retired ON inflow_rows(retired_at) WHERE retired_at IS NOT NULL;
