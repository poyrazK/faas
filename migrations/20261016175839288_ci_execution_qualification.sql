-- +goose Up
-- Separate from metadata receipts: no conversion, trigger, daemon or dispatch.
CREATE TABLE IF NOT EXISTS ci_executions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 revision_id uuid NOT NULL,
 commit_sha text NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
 idempotency_key text NOT NULL CHECK (octet_length(idempotency_key) BETWEEN 1 AND 128),
 mode text NOT NULL DEFAULT 'qualification' CHECK (mode = 'qualification'),
 status text NOT NULL CHECK (status IN ('queued', 'running', 'cancelling', 'succeeded', 'failed', 'cancelled')),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object' AND octet_length(snapshot::text) <= 4194304),
 version bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
 fence bigint NOT NULL DEFAULT 0 CHECK (fence >= 0),
 lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 FOREIGN KEY (account_id, revision_id) REFERENCES ci_pipeline_revisions(account_id, id) ON DELETE CASCADE,
 UNIQUE (account_id, idempotency_key),
 UNIQUE (account_id, id),
 CHECK (snapshot->>'status' IS NOT NULL AND status = snapshot->>'status'),
 CHECK (snapshot->>'mode' IS NOT NULL AND mode = snapshot->>'mode'),
 CHECK ((status IN ('queued', 'running', 'cancelling') AND finished_at IS NULL)
     OR (status IN ('succeeded', 'failed', 'cancelled') AND finished_at IS NOT NULL AND lease_until IS NULL))
);
CREATE INDEX IF NOT EXISTS ci_executions_account_created_idx ON ci_executions(account_id, created_at DESC, id);

-- +goose Down
DROP TABLE ci_executions;
