-- +goose Up
-- Internal metadata only; no Jobs, dispatch triggers or VM allocation.
CREATE TABLE IF NOT EXISTS ci_pipeline_revisions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,39}$'),
 digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
 definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object' AND octet_length(definition::text) <= 131072),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (account_id, name, digest),
 UNIQUE (account_id, id)
);

CREATE TABLE IF NOT EXISTS ci_runs (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 revision_id uuid NOT NULL,
 pipeline text NOT NULL CHECK (pipeline ~ '^[a-z][a-z0-9-]{0,39}$'),
 commit_sha text NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
 idempotency_key text NOT NULL CHECK (octet_length(idempotency_key) BETWEEN 1 AND 128),
 plan jsonb NOT NULL CHECK (jsonb_typeof(plan) = 'object' AND octet_length(plan::text) <= 131072),
 status text NOT NULL CHECK (status IN ('blocked', 'cancelled')),
 blocked_reason text NOT NULL CHECK (blocked_reason IN ('compute_unavailable', '')),
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 FOREIGN KEY (account_id, revision_id) REFERENCES ci_pipeline_revisions(account_id, id) ON DELETE CASCADE,
 UNIQUE (account_id, idempotency_key),
 UNIQUE (account_id, id),
 CHECK ((status = 'blocked' AND blocked_reason = 'compute_unavailable' AND finished_at IS NULL)
     OR (status = 'cancelled' AND blocked_reason = '' AND finished_at IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS ci_run_events (
 account_id uuid NOT NULL,
 run_id uuid NOT NULL,
 sequence integer NOT NULL CHECK (sequence BETWEEN 1 AND 2),
 event_type text NOT NULL CHECK (event_type IN ('blocked', 'cancelled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (run_id, sequence),
 FOREIGN KEY (account_id, run_id) REFERENCES ci_runs(account_id, id) ON DELETE CASCADE,
 CHECK ((sequence = 1 AND event_type = 'blocked') OR (sequence = 2 AND event_type = 'cancelled'))
);

CREATE INDEX IF NOT EXISTS ci_runs_account_created_idx ON ci_runs(account_id, created_at DESC, id);
CREATE INDEX IF NOT EXISTS ci_revisions_account_created_idx ON ci_pipeline_revisions(account_id, created_at DESC, id);

-- +goose Down
DROP TABLE ci_run_events;
DROP TABLE ci_runs;
DROP TABLE ci_pipeline_revisions;
