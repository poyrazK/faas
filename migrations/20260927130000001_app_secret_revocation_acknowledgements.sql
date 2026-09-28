-- +goose Up
-- +goose StatementBegin
-- Secret runtime observations are row-backed and cascade when app_secrets is
-- deleted. Keep deletion intent and its target roster independently so a
-- caller can prove which workloads acknowledged removal after that cascade.
CREATE TABLE IF NOT EXISTS app_secret_revocations (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    scope text NOT NULL,
    key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_secret_revocations_scope_shape
        CHECK (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'),
    CONSTRAINT app_secret_revocations_key_shape
        CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND length(key) <= 128)
);

CREATE INDEX IF NOT EXISTS app_secret_revocations_app_idx
    ON app_secret_revocations (account_id, app_id, created_at DESC);

CREATE TABLE IF NOT EXISTS app_secret_revocation_targets (
    revocation_id uuid NOT NULL REFERENCES app_secret_revocations(id) ON DELETE CASCADE,
    instance_id uuid NOT NULL,
    workload_name text NOT NULL DEFAULT '',
    runtime_state text NOT NULL,
    reload_support text NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    ack_revision text,
    ack_at timestamptz,
    error_code text,
    PRIMARY KEY (revocation_id, instance_id, workload_name),
    CONSTRAINT app_secret_revocation_targets_workload_name_chk
        CHECK (workload_name = '' OR workload_name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    CONSTRAINT app_secret_revocation_targets_reload_support_chk
        CHECK (reload_support IN ('enabled', 'disabled', 'unknown')),
    CONSTRAINT app_secret_revocation_targets_status_chk
        CHECK (status IN ('pending', 'applied', 'failed')),
    CONSTRAINT app_secret_revocation_targets_ack_shape_chk CHECK (
        (status = 'pending' AND ack_revision IS NULL AND ack_at IS NULL AND error_code IS NULL) OR
        (status = 'applied' AND ack_revision IS NOT NULL AND ack_revision ~ '^[0-9a-f]{64}$' AND ack_at IS NOT NULL AND error_code IS NULL) OR
        (status = 'failed' AND ack_revision IS NOT NULL AND ack_revision ~ '^[0-9a-f]{64}$' AND ack_at IS NOT NULL AND error_code = 'application_reload_failed')
    )
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_secret_revocation_targets;
DROP TABLE IF EXISTS app_secret_revocations;
-- +goose StatementEnd
