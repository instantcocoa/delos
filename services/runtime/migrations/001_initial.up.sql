-- Gateway state: virtual keys and per-month usage accounting.
-- The gateway is otherwise stateless; this is the only schema it owns.

CREATE TABLE IF NOT EXISTS virtual_keys (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    hash         TEXT NOT NULL,
    models       TEXT[] NOT NULL DEFAULT '{}',
    token_budget BIGINT NOT NULL DEFAULT 0,
    usd_budget   DOUBLE PRECISION NOT NULL DEFAULT 0,
    revoked      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_virtual_keys_prefix ON virtual_keys (prefix) WHERE NOT revoked;

CREATE TABLE IF NOT EXISTS key_usage (
    key_id  TEXT NOT NULL REFERENCES virtual_keys (id) ON DELETE CASCADE,
    month   TEXT NOT NULL, -- "2026-08"
    tokens  BIGINT NOT NULL DEFAULT 0,
    usd     DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (key_id, month)
);
