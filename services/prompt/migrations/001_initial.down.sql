DROP TRIGGER IF EXISTS prompts_updated_at ON prompts;
DROP FUNCTION IF EXISTS update_updated_at();
DROP TABLE IF EXISTS prompt_metadata;
DROP TABLE IF EXISTS prompt_tags;
DROP TABLE IF EXISTS prompt_generation_configs;
DROP TABLE IF EXISTS prompt_variables;
DROP TABLE IF EXISTS prompt_messages;
DROP TABLE IF EXISTS prompt_versions;
DROP TABLE IF EXISTS prompts;
