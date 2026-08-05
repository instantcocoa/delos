-- Initialize the Delos database.
-- Runs once, on first PostgreSQL startup.
--
-- Tables live in the public schema and are created by the delos control
-- plane, which applies its own migrations at startup (`delos serve`).
-- Nothing else needs to happen here.

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

DO $$
BEGIN
    RAISE NOTICE 'Delos database initialized successfully';
END $$;
