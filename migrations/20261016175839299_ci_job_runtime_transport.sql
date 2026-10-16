-- +goose Up
-- Runtime grants are issued only through a host lease proof; never through HTTP.
CREATE TABLE IF NOT EXISTS ci_job_runtime (
 attempt_id uuid PRIMARY KEY REFERENCES ci_job_attempts(attempt_id) ON DELETE CASCADE,
 instance_id uuid NOT NULL REFERENCES instances(id),
 capability_hash bytea NOT NULL CHECK (octet_length(capability_hash) = 32),
 expires_at timestamptz NOT NULL,
 cancelled_at timestamptz,
 result jsonb CHECK (result IS NULL OR (jsonb_typeof(result) = 'object' AND octet_length(result::text) <= 32768)),
 sealed_at timestamptz,
 transfer_token uuid,
 transfer_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((result IS NULL) = (sealed_at IS NULL)),
 CHECK ((transfer_token IS NULL) = (transfer_until IS NULL)),
 CHECK (expires_at > created_at)
);
CREATE TABLE IF NOT EXISTS ci_job_runtime_objects (
 attempt_id uuid NOT NULL REFERENCES ci_job_runtime(attempt_id) ON DELETE CASCADE,
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 512),
 digest text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
 bytes bigint NOT NULL CHECK (bytes BETWEEN 0 AND 33554432),
 retained boolean NOT NULL DEFAULT false,
 PRIMARY KEY (attempt_id, name)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM ci_job_runtime) THEN
  RAISE EXCEPTION 'CI runtime grants and their Jobs must be reconciled before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_job_runtime_objects;
DROP TABLE ci_job_runtime;
