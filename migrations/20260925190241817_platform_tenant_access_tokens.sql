-- filename: 20260925190241817_platform_tenant_access_tokens.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS platform_tenant_access_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL,
    platform_tenant_id uuid NOT NULL,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    prefix text NOT NULL CHECK (prefix ~ '^fp_tenant_[0-9a-f]{8}$'),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    scopes text[] NOT NULL CHECK (
      cardinality(scopes) > 0 AND
      scopes <@ ARRAY['platform_tenant:usage:read', 'platform_tenant:statements:read']::text[]
    ),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at timestamptz,
    FOREIGN KEY (account_id, platform_tenant_id)
      REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS platform_tenant_access_tokens_active_name_idx
  ON platform_tenant_access_tokens(account_id, platform_tenant_id, lower(name))
  WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS platform_tenant_access_tokens_tenant_idx
  ON platform_tenant_access_tokens(account_id, platform_tenant_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE platform_tenant_access_tokens;
-- +goose StatementEnd
