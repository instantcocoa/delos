CREATE INDEX IF NOT EXISTS idx_virtual_keys_prefix ON virtual_keys (prefix) WHERE NOT revoked;
DROP INDEX IF EXISTS idx_virtual_keys_prefix_unique;
