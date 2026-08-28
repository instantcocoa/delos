-- Two live virtual keys must never share a lookup prefix.
--
-- Prefixes were "dk_" plus five base64url characters (about 30 bits), stored
-- under a non-unique index, and GetKeyByPrefix took whichever row came back
-- first. A collision therefore silently attributed a request authenticated
-- with one key to the other key's budget and model scope. Prefixes are now 96
-- bits (services/runtime/keys.go), and this constraint makes a collision an
-- error at write time rather than a misrouted charge at read time.
--
-- The uniqueness is global, not partial on NOT revoked: a revoked key's prefix
-- stays reserved so revoking and re-creating can never resurrect an ambiguity.
CREATE UNIQUE INDEX IF NOT EXISTS idx_virtual_keys_prefix_unique ON virtual_keys (prefix);

-- Superseded by the unique index above, which serves the same lookups.
DROP INDEX IF EXISTS idx_virtual_keys_prefix;
