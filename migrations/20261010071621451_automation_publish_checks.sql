-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_automation_publish_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 mode text NOT NULL CHECK (mode IN ('optional','scenarios','coverage')),
 version bigint NOT NULL CHECK (version > 0)
);
CREATE TABLE IF NOT EXISTS workflow_automation_publish_receipts (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 api_key_id text NOT NULL DEFAULT '',
 token_hash text NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
 policy_version bigint NOT NULL CHECK (policy_version >= 0),
 expires_at timestamptz NOT NULL,
 evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence)='object'),
 PRIMARY KEY (app_id,name,account_id,api_key_id)
);

-- +goose Down
DROP TABLE IF EXISTS workflow_automation_publish_receipts;
DROP TABLE IF EXISTS workflow_automation_publish_policies;
