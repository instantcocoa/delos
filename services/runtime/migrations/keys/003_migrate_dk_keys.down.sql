-- Roll back the migration ledger only. The migrated governance rows are the
-- migration's product; dropping them would delete data the migration created,
-- so the down file removes the ledger and leaves both the source tables and
-- the migrated governance rows in place (non-destructive in both directions).

DROP TABLE IF EXISTS key_migration;
