-- +goose Up
CREATE TABLE IF NOT EXISTS dev_bridge_sessions (
    id text PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    target_app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    scope jsonb NOT NULL,
    attachment_digest bytea NOT NULL CHECK (octet_length(attachment_digest) = 32),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dev_bridge_scope_shape CHECK (
      jsonb_typeof(scope) = 'object' AND
      scope ?& ARRAY['account_id','target_app_id','environment_id','developer_id','project_id'] AND
      scope->>'account_id' = account_id::text AND
      scope->>'target_app_id' = target_app_id::text AND
      scope->>'environment_id' = environment_id::text AND
      jsonb_typeof(scope->'dependency_app_ids') IN ('array', 'null') AND
      length(scope->>'developer_id') BETWEEN 1 AND 128
    )
);
CREATE INDEX IF NOT EXISTS dev_bridge_sessions_account_expiry ON dev_bridge_sessions(account_id, expires_at);

-- +goose Down
DROP TABLE dev_bridge_sessions;
