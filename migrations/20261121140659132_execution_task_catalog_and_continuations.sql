-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS execution_task_versions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
 version integer NOT NULL CHECK (version BETWEEN 1 AND 1000000),
 digest text NOT NULL CHECK (digest ~ '^[a-f0-9]{64}$'),
 metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata)='object' AND octet_length(metadata::text)<=262144),
 environment_id uuid,
 runtime text NOT NULL CHECK (runtime IN ('node22','node24','python312','python313')),
 agent_endpoint_ids uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(agent_endpoint_ids)<=16 AND array_position(agent_endpoint_ids,NULL) IS NULL),
 status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','retired')),
 sealed_definition bytea NOT NULL CHECK (octet_length(sealed_definition)<=4194304),
 kid text NOT NULL CHECK (length(kid) BETWEEN 1 AND 200),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT execution_task_version_content CHECK ((status='active')=(octet_length(sealed_definition)>0)),
 CONSTRAINT execution_task_version_identity UNIQUE(account_id,name,version)
);
CREATE TABLE IF NOT EXISTS execution_task_invocations (
 execution_id uuid PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 version_id uuid NOT NULL REFERENCES execution_task_versions(id) ON DELETE CASCADE,
 request_digest bytea NOT NULL CHECK (octet_length(request_digest)=32),
 callback_key bytea CHECK (callback_key IS NULL OR octet_length(callback_key)=32),
 callback jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(callback)='object' AND octet_length(callback::text)<=4096),
 delivered_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((callback_key IS NULL)=(callback='{}'::jsonb)),
 UNIQUE(account_id,callback_key)
);
CREATE INDEX IF NOT EXISTS execution_task_pending_callbacks ON execution_task_invocations(created_at) WHERE callback_key IS NOT NULL AND delivered_at IS NULL;
-- +goose Down
SELECT 1;
