-- +goose Up
-- One short global lock serializes CI storage reservations, never blob I/O.
CREATE TABLE IF NOT EXISTS ci_storage_budget (lock_id boolean PRIMARY KEY CHECK(lock_id));
INSERT INTO ci_storage_budget VALUES(true) ON CONFLICT (lock_id) DO NOTHING;

-- Deliberately survives account/execution deletion so orphan payloads retain
-- recovery authority. Physical object IDs are immutable and never reused.
CREATE TABLE IF NOT EXISTS ci_managed_objects (
 object_key text PRIMARY KEY CHECK(octet_length(object_key) BETWEEN 1 AND 1024),
 id uuid NOT NULL UNIQUE,
 account_id uuid NOT NULL,
 execution_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 job_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('source','logs','artifact')),
 digest text NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 bytes bigint NOT NULL CHECK(bytes BETWEEN 0 AND 33554432),
 state text NOT NULL CHECK(state IN ('pending','ready','deleting','deleted')),
 op_token uuid,
 op_until timestamptz,
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 ready_at timestamptz,
 deleted_at timestamptz,
 CHECK((op_token IS NULL) = (op_until IS NULL)),
 CHECK(expires_at > created_at),
 CHECK(state!='ready' OR ready_at IS NOT NULL),
 CHECK((state='deleted') = (deleted_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS ci_managed_objects_account_idx ON ci_managed_objects(account_id,state);
CREATE INDEX IF NOT EXISTS ci_managed_objects_attempt_idx ON ci_managed_objects(attempt_id);
CREATE INDEX IF NOT EXISTS ci_managed_objects_retention_idx ON ci_managed_objects(state,expires_at,created_at);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_managed_objects) THEN
  RAISE EXCEPTION 'CI object recovery journal and remote tombstones must be reconciled before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_managed_objects;
DROP TABLE ci_storage_budget;
