-- Deployments table
CREATE TABLE deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt_id TEXT NOT NULL,
    from_version INTEGER NOT NULL DEFAULT 0,
    to_version INTEGER NOT NULL,
    environment TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending_approval',
    status_message TEXT,
    gates_passed BOOLEAN NOT NULL DEFAULT false,
    created_by TEXT,
    approved_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_deployments_prompt_id ON deployments(prompt_id);
CREATE INDEX idx_deployments_environment ON deployments(environment);
CREATE INDEX idx_deployments_status ON deployments(status);
CREATE INDEX idx_deployments_created_at ON deployments(created_at);

-- Deployment strategy
CREATE TABLE deployment_strategies (
    deployment_id UUID PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    strategy_type TEXT NOT NULL DEFAULT 'immediate',
    initial_percentage INTEGER NOT NULL DEFAULT 100,
    increment INTEGER NOT NULL DEFAULT 10,
    interval_seconds INTEGER NOT NULL DEFAULT 300,
    auto_rollback BOOLEAN NOT NULL DEFAULT false,
    rollback_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.5
);

-- Rollout progress
CREATE TABLE deployment_rollouts (
    deployment_id UUID PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    current_percentage INTEGER NOT NULL DEFAULT 0,
    target_percentage INTEGER NOT NULL DEFAULT 100,
    last_increment_at TIMESTAMPTZ,
    next_increment_at TIMESTAMPTZ
);

-- Deployment metadata
CREATE TABLE deployment_metadata (
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY(deployment_id, key)
);

-- Quality gates
CREATE TABLE quality_gates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    prompt_id TEXT NOT NULL,
    required BOOLEAN NOT NULL DEFAULT true,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_quality_gates_prompt ON quality_gates(prompt_id);

-- Quality gate conditions
CREATE TABLE quality_gate_conditions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    gate_id UUID NOT NULL REFERENCES quality_gates(id) ON DELETE CASCADE,
    condition_type TEXT NOT NULL,
    operator TEXT NOT NULL DEFAULT 'gte',
    threshold DOUBLE PRECISION NOT NULL,
    eval_run_id TEXT,
    dataset_id TEXT
);

CREATE INDEX idx_quality_gate_conditions_gate ON quality_gate_conditions(gate_id);

-- Quality gate results (per deployment)
CREATE TABLE deployment_gate_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    gate_id UUID NOT NULL REFERENCES quality_gates(id) ON DELETE CASCADE,
    gate_name TEXT NOT NULL,
    passed BOOLEAN NOT NULL DEFAULT false,
    message TEXT,
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_deployment_gate_results_deployment ON deployment_gate_results(deployment_id);

-- Gate condition results
CREATE TABLE deployment_condition_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    gate_result_id UUID NOT NULL REFERENCES deployment_gate_results(id) ON DELETE CASCADE,
    condition_type TEXT NOT NULL,
    expected DOUBLE PRECISION NOT NULL,
    actual DOUBLE PRECISION NOT NULL,
    passed BOOLEAN NOT NULL DEFAULT false
);

CREATE INDEX idx_deployment_condition_results_gate ON deployment_condition_results(gate_result_id);

-- Deployment metrics snapshots
CREATE TABLE deployment_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    is_baseline BOOLEAN NOT NULL DEFAULT false,
    avg_latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    error_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    quality_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    request_count INTEGER NOT NULL DEFAULT 0,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_deployment_metrics_deployment ON deployment_metrics(deployment_id);
