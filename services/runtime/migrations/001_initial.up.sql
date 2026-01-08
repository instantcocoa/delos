-- Provider configurations
CREATE TABLE providers (
    name TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    available BOOLEAN NOT NULL DEFAULT true,
    priority INTEGER NOT NULL DEFAULT 0,
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Models available per provider
CREATE TABLE provider_models (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_name TEXT NOT NULL REFERENCES providers(name) ON DELETE CASCADE,
    model_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    context_window INTEGER NOT NULL DEFAULT 4096,
    cost_per_1k_input DOUBLE PRECISION NOT NULL DEFAULT 0,
    cost_per_1k_output DOUBLE PRECISION NOT NULL DEFAULT 0,
    supports_streaming BOOLEAN NOT NULL DEFAULT true,
    supports_functions BOOLEAN NOT NULL DEFAULT false,
    UNIQUE(provider_name, model_id)
);

CREATE INDEX idx_provider_models_provider ON provider_models(provider_name);

-- Completion cache (for semantic caching)
CREATE TABLE completion_cache (
    cache_key TEXT PRIMARY KEY,
    prompt_hash TEXT NOT NULL,
    response TEXT NOT NULL,
    model TEXT NOT NULL,
    provider TEXT NOT NULL,
    usage_tokens INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    hit_count INTEGER NOT NULL DEFAULT 0,
    last_hit_at TIMESTAMPTZ
);

CREATE INDEX idx_completion_cache_expires ON completion_cache(expires_at);
CREATE INDEX idx_completion_cache_prompt ON completion_cache(prompt_hash);

-- Usage logs (for tracking and billing)
CREATE TABLE usage_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id TEXT,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt_ref TEXT,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    cached BOOLEAN NOT NULL DEFAULT false,
    error TEXT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_usage_logs_trace ON usage_logs(trace_id);
CREATE INDEX idx_usage_logs_provider ON usage_logs(provider);
CREATE INDEX idx_usage_logs_model ON usage_logs(model);
CREATE INDEX idx_usage_logs_created_at ON usage_logs(created_at);
CREATE INDEX idx_usage_logs_prompt_ref ON usage_logs(prompt_ref);

-- Rate limiting state
CREATE TABLE rate_limits (
    key TEXT PRIMARY KEY,
    tokens INTEGER NOT NULL DEFAULT 0,
    last_reset TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    window_seconds INTEGER NOT NULL DEFAULT 60
);

-- Function to cleanup expired cache entries
CREATE OR REPLACE FUNCTION cleanup_expired_cache()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    DELETE FROM completion_cache WHERE expires_at < NOW();
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

-- Seed default providers
INSERT INTO providers (name, display_name, available, priority) VALUES
    ('openai', 'OpenAI', true, 1),
    ('anthropic', 'Anthropic', true, 2)
ON CONFLICT (name) DO NOTHING;

-- Seed some common models
INSERT INTO provider_models (provider_name, model_id, display_name, context_window, cost_per_1k_input, cost_per_1k_output) VALUES
    ('openai', 'gpt-4o', 'GPT-4o', 128000, 0.005, 0.015),
    ('openai', 'gpt-4o-mini', 'GPT-4o Mini', 128000, 0.00015, 0.0006),
    ('openai', 'gpt-4-turbo', 'GPT-4 Turbo', 128000, 0.01, 0.03),
    ('anthropic', 'claude-3-5-sonnet-20241022', 'Claude 3.5 Sonnet', 200000, 0.003, 0.015),
    ('anthropic', 'claude-3-5-haiku-20241022', 'Claude 3.5 Haiku', 200000, 0.001, 0.005),
    ('anthropic', 'claude-3-opus-20240229', 'Claude 3 Opus', 200000, 0.015, 0.075)
ON CONFLICT (provider_name, model_id) DO NOTHING;
