-- 003: migrate legacy dk_ virtual keys into Bifrost governance.
--
-- Non-destructive. This file creates the ledger that records the migration;
-- the row copy itself is performed by runtime.MigrateLegacyKeys, which runs
-- after Bifrost's config store has created the governance_* tables (the
-- gateway's legacy startup path embeds only migrations/*.sql, so this
-- keys/ subdirectory is applied only where Bifrost is present).
--
-- Mapping performed by runtime.MigrateLegacyKeys, one ledger row per source:
--
--   virtual_keys.id                      -> governance_virtual_keys.id
--   virtual_keys.name                    -> governance_virtual_keys.name
--                                            (suffixed with " (migrated <id>)"
--                                             on a name collision)
--   virtual_keys.models (TEXT[])         -> governance_virtual_keys.metadata
--                                            ["delos.models"] (JSON array)
--   virtual_keys.usd_budget (>0)         -> governance_budgets (reset "1M")
--   virtual_keys.token_budget (>0)       -> governance_rate_limits (token, "1M")
--   virtual_keys.revoked                 -> governance_virtual_keys.is_active
--                                            (false for every migrated key)
--   virtual_keys.created_at              -> governance_virtual_keys.created_at
--
-- The plaintext secret cannot be recovered: Delos stored only an argon2id
-- hash, so every migrated key is created INACTIVE with a placeholder value
-- ("dk-migrated-<id>"). Operators mint a replacement session key through the
-- KeyService; the old key's metadata and budgets are preserved for audit.
--
-- The source tables virtual_keys and key_usage are never read for writes,
-- modified, or dropped.

CREATE TABLE IF NOT EXISTS key_migration (
    old_id      TEXT PRIMARY KEY,
    old_prefix  TEXT NOT NULL,
    new_id      TEXT NOT NULL,
    migrated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    note        TEXT NOT NULL
);
