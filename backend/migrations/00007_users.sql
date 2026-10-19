-- +goose Up
-- User layer: auth, follows, collections, alerts, trade intents (D96–D99, D105).

CREATE TABLE users (
    id            UUID        PRIMARY KEY,
    chain         chain_id    NOT NULL,
    address       BYTEA       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ,
    UNIQUE (chain, address)
);

CREATE TABLE auth_nonces (
    nonce       TEXT        PRIMARY KEY,
    chain       chain_id    NOT NULL,
    address     BYTEA       NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

CREATE INDEX auth_nonces_expiry ON auth_nonces (expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE auth_sessions (
    id            UUID        PRIMARY KEY,
    token_hash    BYTEA       NOT NULL UNIQUE,
    user_id       UUID        NOT NULL REFERENCES users (id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    revoke_reason TEXT
                  CHECK (revoke_reason IN ('logout', 'account_changed', 'relogin', 'admin')),
    user_agent    TEXT
);

CREATE INDEX auth_sessions_user ON auth_sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX auth_sessions_expiry ON auth_sessions (expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE follows (
    user_id     UUID     NOT NULL REFERENCES users (id),
    chain       chain_id NOT NULL,
    target_kind TEXT     NOT NULL CHECK (target_kind IN ('wallet', 'token')),
    address     BYTEA    NOT NULL,
    note        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chain, target_kind, address)
);

CREATE TABLE collections (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users (id),
    chain       chain_id    NOT NULL,
    name        TEXT        NOT NULL,
    description TEXT,
    cover_url   TEXT,
    is_public   BOOLEAN     NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX collections_user ON collections (user_id);
CREATE INDEX collections_public ON collections (chain, is_public)
    WHERE is_public;

CREATE TABLE collection_members (
    collection_id UUID     NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    address       BYTEA    NOT NULL,
    added_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, address)
);

CREATE TABLE blacklist (
    user_id     UUID     NOT NULL REFERENCES users (id),
    chain       chain_id NOT NULL,
    target_kind TEXT     NOT NULL CHECK (target_kind IN ('token', 'address')),
    address     BYTEA    NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chain, target_kind, address)
);

CREATE TABLE alert_rules (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users (id),
    kind        TEXT        NOT NULL
                CHECK (kind IN ('wallet_trade', 'token_price', 'token_signal')),
    chain       chain_id,
    address     BYTEA,
    params      JSONB       NOT NULL DEFAULT '{}',
    enabled     BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX alert_rules_user ON alert_rules (user_id) WHERE enabled;

CREATE TABLE alert_events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users (id),
    rule_id     UUID        NOT NULL REFERENCES alert_rules (id),
    fired_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload     JSONB       NOT NULL
);

CREATE INDEX alert_events_user_time ON alert_events (user_id, fired_at DESC);

CREATE TABLE trade_intents (
    id              UUID        PRIMARY KEY,
    user_id         UUID        NOT NULL REFERENCES users (id),
    chain           chain_id    NOT NULL,
    token           BYTEA       NOT NULL,
    side            TEXT        NOT NULL CHECK (side IN ('buy', 'sell')),
    quote_json      JSONB,
    quote_expires   TIMESTAMPTZ,
    tx_hash         BYTEA,
    status          TEXT        NOT NULL DEFAULT 'quoted'
                    CHECK (status IN (
                        'quoted', 'built', 'submitted', 'pending',
                        'confirmed', 'failed', 'timeout', 'unknown'
                    )),
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX trade_intents_user ON trade_intents (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS trade_intents;
DROP TABLE IF EXISTS alert_events;
DROP TABLE IF EXISTS alert_rules;
DROP TABLE IF EXISTS blacklist;
DROP TABLE IF EXISTS collection_members;
DROP TABLE IF EXISTS collections;
DROP TABLE IF EXISTS follows;
DROP TABLE IF EXISTS auth_sessions;
DROP TABLE IF EXISTS auth_nonces;
DROP TABLE IF EXISTS users;
