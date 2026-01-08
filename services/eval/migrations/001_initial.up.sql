-- Eval runs table
CREATE TABLE eval_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    prompt_id TEXT NOT NULL,
    prompt_version INTEGER NOT NULL,
    dataset_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    total_examples INTEGER NOT NULL DEFAULT 0,
    completed_examples INTEGER NOT NULL DEFAULT 0,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error_message TEXT
);

CREATE INDEX idx_eval_runs_prompt_id ON eval_runs(prompt_id);
CREATE INDEX idx_eval_runs_dataset_id ON eval_runs(dataset_id);
CREATE INDEX idx_eval_runs_status ON eval_runs(status);
CREATE INDEX idx_eval_runs_created_at ON eval_runs(created_at);

-- Evaluator configs for each run
CREATE TABLE eval_run_evaluators (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    eval_run_id UUID NOT NULL REFERENCES eval_runs(id) ON DELETE CASCADE,
    evaluator_type TEXT NOT NULL,
    weight DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    config JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_eval_run_evaluators_run ON eval_run_evaluators(eval_run_id);

-- Eval run summary
CREATE TABLE eval_run_summaries (
    eval_run_id UUID PRIMARY KEY REFERENCES eval_runs(id) ON DELETE CASCADE,
    overall_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    scores_by_evaluator JSONB NOT NULL DEFAULT '{}',
    pass_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    total_cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0
);

-- Individual eval results
CREATE TABLE eval_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    eval_run_id UUID NOT NULL REFERENCES eval_runs(id) ON DELETE CASCADE,
    example_id TEXT NOT NULL,
    input JSONB NOT NULL DEFAULT '{}',
    expected_output JSONB NOT NULL DEFAULT '{}',
    actual_output TEXT,
    passed BOOLEAN NOT NULL DEFAULT false,
    overall_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_eval_results_run ON eval_results(eval_run_id);
CREATE INDEX idx_eval_results_passed ON eval_results(passed);
CREATE INDEX idx_eval_results_example ON eval_results(example_id);

-- Individual evaluator scores for each result
CREATE TABLE eval_result_scores (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    eval_result_id UUID NOT NULL REFERENCES eval_results(id) ON DELETE CASCADE,
    evaluator_type TEXT NOT NULL,
    score DOUBLE PRECISION NOT NULL DEFAULT 0,
    passed BOOLEAN NOT NULL DEFAULT false,
    reason TEXT
);

CREATE INDEX idx_eval_result_scores_result ON eval_result_scores(eval_result_id);
