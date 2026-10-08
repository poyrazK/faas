-- filename: 20261007170000001_customer_operation_workflow_states.sql
-- ADR-719: app-declared workflow state and monotonic current snapshots.
-- +goose Up
CREATE TABLE customer_operation_workflow_state_reports (
    operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
    id uuid NOT NULL,
    workflow text NOT NULL CHECK (workflow ~ '^[a-z][a-z0-9-]{0,62}$'),
    instance_id text NOT NULL CHECK (octet_length(instance_id) BETWEEN 1 AND 256 AND instance_id !~ '[\x00-\x1f\x7f]'),
    state text NOT NULL CHECK (state ~ '^[a-z][a-z0-9-]{0,63}$'),
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    occurred_at timestamptz NOT NULL CHECK (isfinite(occurred_at)),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (operation_id, id)
);

CREATE TABLE customer_operation_workflow_states (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    platform_tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    scope text NOT NULL,
    subject_type text NOT NULL,
    subject_id text NOT NULL CHECK (octet_length(subject_id) BETWEEN 1 AND 256 AND subject_id !~ '[\x00-\x1f\x7f]'),
    workflow text NOT NULL CHECK (workflow ~ '^[a-z][a-z0-9-]{0,62}$'),
    instance_id text NOT NULL CHECK (octet_length(instance_id) BETWEEN 1 AND 256 AND instance_id !~ '[\x00-\x1f\x7f]'),
    state text NOT NULL CHECK (state ~ '^[a-z][a-z0-9-]{0,63}$'),
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    operation_id uuid NOT NULL,
    report_id uuid NOT NULL,
    updated_at timestamptz NOT NULL CHECK (isfinite(updated_at)),
    PRIMARY KEY (account_id, app_id, platform_tenant_id, scope, subject_type, subject_id, workflow, instance_id)
);

CREATE INDEX customer_operation_workflow_states_subject_idx ON customer_operation_workflow_states
    (account_id, app_id, platform_tenant_id, scope, subject_type, subject_id, updated_at DESC);

-- +goose Down
-- Current state is customer business data; rollback requires an empty projection.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM customer_operation_workflow_state_reports)
       OR EXISTS (SELECT 1 FROM customer_operation_workflow_states) THEN
        RAISE EXCEPTION 'retained customer workflow states prevent rollback';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE customer_operation_workflow_states;
DROP TABLE customer_operation_workflow_state_reports;
