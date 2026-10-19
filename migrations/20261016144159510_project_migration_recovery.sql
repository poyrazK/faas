-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_migration_recoveries (
 account_id uuid NOT NULL,
 attempt_id text NOT NULL CHECK(attempt_id ~ '^[a-f0-9]{32}$'),
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 request_id text NOT NULL CHECK(request_id ~ '^[a-f0-9]{32}$'),
 request_hash text NOT NULL CHECK(request_hash ~ '^[a-f0-9]{64}$'),
 task_id uuid NOT NULL,
 generation bigint NOT NULL CHECK(generation>0),
 replacement_attempt_id text NOT NULL CHECK(replacement_attempt_id ~ '^[a-f0-9]{32}$' AND replacement_attempt_id<>attempt_id),
 replacement_release_id uuid NOT NULL,
 config_version bigint NOT NULL CHECK(config_version>=0),
 config_hash text NOT NULL CHECK(config_hash ~ '^[a-f0-9]{64}$'),
 runtime_fingerprints jsonb NOT NULL CHECK(jsonb_typeof(runtime_fingerprints)='object'),
 evidence_hash text NOT NULL CHECK(evidence_hash ~ '^[a-f0-9]{64}$'),
 resolved_at timestamptz NOT NULL CHECK(isfinite(resolved_at)),
 PRIMARY KEY(account_id,attempt_id),
 FOREIGN KEY(account_id,attempt_id) REFERENCES project_migration_contracts(account_id,attempt_id) ON DELETE CASCADE
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_migration_recoveries) THEN
  RAISE EXCEPTION 'cannot discard migration recovery audit records';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_migration_recoveries;
