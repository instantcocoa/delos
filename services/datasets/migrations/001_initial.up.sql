-- Datasets table
CREATE TABLE datasets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT,
    prompt_id TEXT,
    example_count INTEGER NOT NULL DEFAULT 0,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_datasets_prompt_id ON datasets(prompt_id);
CREATE INDEX idx_datasets_created_at ON datasets(created_at);
CREATE INDEX idx_datasets_deleted_at ON datasets(deleted_at) WHERE deleted_at IS NULL;

-- Dataset tags
CREATE TABLE dataset_tags (
    dataset_id UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY(dataset_id, tag)
);

CREATE INDEX idx_dataset_tags_tag ON dataset_tags(tag);

-- Dataset schema fields
CREATE TABLE dataset_schema_fields (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    field_type TEXT NOT NULL DEFAULT 'string',
    description TEXT,
    required BOOLEAN NOT NULL DEFAULT false,
    is_input BOOLEAN NOT NULL DEFAULT true
);

CREATE INDEX idx_dataset_schema_fields_dataset ON dataset_schema_fields(dataset_id);

-- Examples table
CREATE TABLE examples (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    input JSONB NOT NULL DEFAULT '{}',
    expected_output JSONB NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',
    source TEXT NOT NULL DEFAULT 'manual',
    source_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_examples_dataset_id ON examples(dataset_id);
CREATE INDEX idx_examples_source ON examples(source);
CREATE INDEX idx_examples_created_at ON examples(created_at);

-- Function to update example count
CREATE OR REPLACE FUNCTION update_dataset_example_count()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE datasets SET example_count = example_count + 1, last_updated = NOW() WHERE id = NEW.dataset_id;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE datasets SET example_count = example_count - 1, last_updated = NOW() WHERE id = OLD.dataset_id;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER examples_count_trigger
    AFTER INSERT OR DELETE ON examples
    FOR EACH ROW
    EXECUTE FUNCTION update_dataset_example_count();
