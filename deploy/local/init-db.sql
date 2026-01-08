-- Initialize Delos database schemas
-- This file is run on first PostgreSQL startup

-- Create extension for UUID generation
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Create schemas for each service
CREATE SCHEMA IF NOT EXISTS observe;
CREATE SCHEMA IF NOT EXISTS runtime;
CREATE SCHEMA IF NOT EXISTS prompt;
CREATE SCHEMA IF NOT EXISTS datasets;
CREATE SCHEMA IF NOT EXISTS eval;
CREATE SCHEMA IF NOT EXISTS deploy;

-- Grant permissions
GRANT ALL ON SCHEMA observe TO delos;
GRANT ALL ON SCHEMA runtime TO delos;
GRANT ALL ON SCHEMA prompt TO delos;
GRANT ALL ON SCHEMA datasets TO delos;
GRANT ALL ON SCHEMA eval TO delos;
GRANT ALL ON SCHEMA deploy TO delos;

-- Log successful initialization
DO $$
BEGIN
    RAISE NOTICE 'Delos database initialized successfully';
END $$;
