-- Per-app deploy tokens (report item: replace account-wide CI keys).
-- The token plaintext is never persisted; only SHA-256 is stored.
-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS deploy_tokens (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  token_sha256 bytea UNIQUE NOT NULL,
  label text NOT NULL DEFAULT '',
  scopes text[] NOT NULL DEFAULT ARRAY['deploy:write'::text],
  status text NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  last_used_at timestamptz,
  revoked_at timestamptz,
  rotated_from_id uuid REFERENCES deploy_tokens(id) ON DELETE SET NULL,
  CONSTRAINT deploy_tokens_hash_len_chk CHECK (octet_length(token_sha256) = 32),
  CONSTRAINT deploy_tokens_status_chk CHECK (status IN ('active', 'grace', 'revoked')),
  CONSTRAINT deploy_tokens_scopes_chk CHECK (
    scopes <@ ARRAY['deploy:write'::text]::text[] AND cardinality(scopes) > 0
  ),
  CONSTRAINT deploy_tokens_expiry_chk CHECK (expires_at > created_at),
  CONSTRAINT deploy_tokens_label_len_chk CHECK (length(label) <= 100)
);

CREATE INDEX IF NOT EXISTS deploy_tokens_app_idx
  ON deploy_tokens (account_id, app_id, created_at DESC);
CREATE INDEX IF NOT EXISTS deploy_tokens_active_idx
  ON deploy_tokens (token_sha256)
  WHERE status IN ('active', 'grace');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS deploy_tokens;
-- +goose StatementEnd
