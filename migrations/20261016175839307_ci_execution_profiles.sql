-- filename: 20261016175839307_ci_execution_profiles.sql

-- +goose Up
-- Saved internal intent only. No trigger, automatic admission or flag enablement.
CREATE TABLE IF NOT EXISTS ci_execution_profiles (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(name ~ '^[a-z][a-z0-9-]{0,39}$'),
 revision_id uuid NOT NULL,
 revision_digest text NOT NULL CHECK(revision_digest ~ '^sha256:[0-9a-f]{64}$'),
 binding_id uuid NOT NULL,
 binding_generation bigint NOT NULL CHECK(binding_generation > 0),
 templates jsonb NOT NULL CHECK(jsonb_typeof(templates)='object' AND octet_length(templates::text)<=65536),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=65536),
 state text NOT NULL DEFAULT 'active' CHECK(state IN ('active','disabled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,revision_id) REFERENCES ci_pipeline_revisions(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE,
 UNIQUE(account_id,name),
 UNIQUE(account_id,id)
);
CREATE INDEX IF NOT EXISTS ci_execution_profiles_account_created_idx ON ci_execution_profiles(account_id,created_at DESC,id);

-- +goose Down
DROP TABLE ci_execution_profiles;
