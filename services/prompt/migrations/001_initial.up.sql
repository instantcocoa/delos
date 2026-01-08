-- Prompts table
CREATE TABLE prompts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'draft',
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_prompts_slug ON prompts(slug);
CREATE INDEX idx_prompts_status ON prompts(status);
CREATE INDEX idx_prompts_created_at ON prompts(created_at);
CREATE INDEX idx_prompts_deleted_at ON prompts(deleted_at) WHERE deleted_at IS NULL;

-- Prompt versions table (stores each version of a prompt)
CREATE TABLE prompt_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt_id UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    change_description TEXT,
    updated_by TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(prompt_id, version)
);

CREATE INDEX idx_prompt_versions_prompt_id ON prompt_versions(prompt_id);

-- Prompt messages table (stores messages for each version)
CREATE TABLE prompt_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt_version_id UUID NOT NULL REFERENCES prompt_versions(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_prompt_messages_version ON prompt_messages(prompt_version_id);

-- Prompt variables table
CREATE TABLE prompt_variables (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt_version_id UUID NOT NULL REFERENCES prompt_versions(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    var_type TEXT NOT NULL DEFAULT 'string',
    required BOOLEAN NOT NULL DEFAULT false,
    default_value TEXT
);

CREATE INDEX idx_prompt_variables_version ON prompt_variables(prompt_version_id);

-- Generation config table
CREATE TABLE prompt_generation_configs (
    prompt_version_id UUID PRIMARY KEY REFERENCES prompt_versions(id) ON DELETE CASCADE,
    temperature DOUBLE PRECISION,
    max_tokens INTEGER,
    top_p DOUBLE PRECISION,
    stop_sequences TEXT[],
    output_schema TEXT
);

-- Prompt tags table
CREATE TABLE prompt_tags (
    prompt_id UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY(prompt_id, tag)
);

CREATE INDEX idx_prompt_tags_tag ON prompt_tags(tag);

-- Prompt metadata table (key-value pairs)
CREATE TABLE prompt_metadata (
    prompt_id UUID NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY(prompt_id, key)
);

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER prompts_updated_at
    BEFORE UPDATE ON prompts
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at();
