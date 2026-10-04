-- +goose Up
-- ADR-531: these are operational ownership records, never copied queued work.
CREATE TABLE IF NOT EXISTS invocation_work_environment_domains (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id),
    policy_name text NOT NULL CHECK (policy_name ~ '^[a-z][a-z0-9-]{0,62}$'),
    kind text NOT NULL CHECK (kind IN ('key','fairness')),
    digest bytea NOT NULL CHECK (length(digest)=32),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id,policy_name,kind,digest)
);
CREATE INDEX IF NOT EXISTS invocation_work_environment_domains_environment_idx
    ON invocation_work_environment_domains(environment_id);

CREATE TABLE IF NOT EXISTS invocation_work_environment_admissions (
    invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id),
    workload_spec_id uuid NOT NULL REFERENCES project_environment_workload_specs(id),
    settings_hash text NOT NULL CHECK (settings_hash ~ '^[a-f0-9]{64}$'),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    policy_name text NOT NULL CHECK (policy_name ~ '^[a-z][a-z0-9-]{0,62}$'),
    policy_revision bigint NOT NULL CHECK (policy_revision>=1),
    key_digest bytea NOT NULL CHECK (length(key_digest)=32),
    fairness_digest bytea,
    fairness_limit integer NOT NULL CHECK (fairness_limit BETWEEN 0 AND 1000),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((fairness_limit=0 AND fairness_digest IS NULL) OR
        (fairness_limit>0 AND fairness_digest IS NOT NULL AND length(fairness_digest)=32))
);
CREATE INDEX IF NOT EXISTS invocation_work_environment_admissions_environment_idx
    ON invocation_work_environment_admissions(environment_id);

-- +goose Down
DROP TABLE invocation_work_environment_admissions;
DROP TABLE invocation_work_environment_domains;
