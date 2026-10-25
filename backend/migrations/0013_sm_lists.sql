-- H3: immutable list snapshots and durable upload reports.
CREATE TABLE sm_lists (
    version bigint PRIMARY KEY,
    parent_version bigint REFERENCES sm_lists(version),
    status text NOT NULL CHECK (status IN ('building','active','retired')),
    build_status text NOT NULL CHECK (build_status IN ('queued','running','ready','activated','failed')),
    error_code text,
    added integer NOT NULL DEFAULT 0,
    merged integer NOT NULL DEFAULT 0,
    removed integer NOT NULL DEFAULT 0,
    total_members integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    activated_at timestamptz
);
CREATE UNIQUE INDEX sm_lists_one_active ON sm_lists ((true)) WHERE status='active';
INSERT INTO sm_lists(version,status,build_status,created_at,activated_at)
VALUES(1,'active','activated',now(),now());

CREATE TABLE sm_list_members (
    version bigint NOT NULL REFERENCES sm_lists(version),
    chain chain_id NOT NULL,
    address bytea NOT NULL,
    label text,
    tags text[] NOT NULL DEFAULT '{}',
    twitter text,
    sources text[] NOT NULL,
    added_at timestamptz NOT NULL,
    excluded boolean NOT NULL DEFAULT false,
    PRIMARY KEY(version,chain,address)
);
CREATE INDEX sm_list_members_wallet ON sm_list_members(chain,address,version);

CREATE TABLE sm_imports (
    id uuid PRIMARY KEY,
    file_name text NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    applied boolean NOT NULL DEFAULT false,
    applied_list_version bigint REFERENCES sm_lists(version),
    applied_at timestamptz,
    rows_archived boolean NOT NULL DEFAULT false,
    totals jsonb NOT NULL
);
-- Raw chain/address text intentionally preserves invalid input for the preview.
CREATE TABLE sm_import_rows (
    import_id uuid NOT NULL REFERENCES sm_imports(id),
    line integer NOT NULL,
    chain text,
    address text,
    label text,
    tags text[] NOT NULL DEFAULT '{}',
    twitter text,
    source text,
    status text NOT NULL CHECK(status IN ('valid','invalid','duplicate','existing')),
    error text,
    PRIMARY KEY(import_id,line)
);

-- API-owned management records represent the latest accepted draft.
CREATE TABLE sm_wallets (
    chain chain_id NOT NULL,
    address bytea NOT NULL,
    label text,
    tags text[] NOT NULL DEFAULT '{}',
    twitter text,
    sources text[] NOT NULL,
    first_added_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY(chain,address)
);
CREATE TABLE sm_list_changes (
    id bigserial PRIMARY KEY,
    version bigint NOT NULL REFERENCES sm_lists(version),
    op text NOT NULL CHECK(op IN ('add','merge','remove','exclude','restore')),
    chain chain_id NOT NULL,
    address bytea NOT NULL,
    label text,
    tags text[] NOT NULL DEFAULT '{}',
    twitter text,
    source text,
    reason text
);
CREATE INDEX sm_list_changes_version ON sm_list_changes(version,id);
CREATE TABLE jobs (
    id bigserial PRIMARY KEY,
    kind text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL CHECK(status IN ('queued','running','done','failed')),
    attempts integer NOT NULL DEFAULT 0,
    lease_until timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX jobs_claim ON jobs(kind,status,lease_until,id);
CREATE TABLE sm_exclusions (
    chain chain_id NOT NULL,
    address bytea NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL,
    created_version bigint NOT NULL REFERENCES sm_lists(version),
    PRIMARY KEY(chain,address)
);
