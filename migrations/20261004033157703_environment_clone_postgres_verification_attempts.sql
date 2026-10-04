-- ADR-375: retain a closed unmatched first attempt and at most two new owners.
-- +goose Up
-- +goose StatementBegin
DO $attempts$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_clone_postgres_verifications'::regclass AND conname='postgres_verification_attempt_identity') THEN
  ALTER TABLE project_environment_clone_postgres_verifications ADD CONSTRAINT postgres_verification_attempt_identity UNIQUE(operation_id,source_database_id,database_oid,verification_id);
 END IF;
END $attempts$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_verification_attempts (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 database_oid bigint NOT NULL CHECK (database_oid BETWEEN 1 AND 4294967295),
 original_verification_id uuid NOT NULL,
 attempt smallint NOT NULL CHECK (attempt BETWEEN 1 AND 3),
 verification_id uuid NOT NULL UNIQUE,
 previous_attempt smallint,
 previous_verification_id uuid,
 previous_opened_at timestamptz CHECK (isfinite(previous_opened_at)),
 previous_closed_at timestamptz CHECK (isfinite(previous_closed_at)),
 key_id text NOT NULL CHECK (key_id ~ '^age1[0-9a-z]{58}$'),
 reserved_bytes bigint NOT NULL CHECK (reserved_bytes BETWEEN 1 AND 16384),
 state text NOT NULL CHECK (state IN ('reserved','verifying','compared','verified','failed')),
 request_started_at timestamptz CHECK (isfinite(request_started_at)),
 window_opened_at timestamptz CHECK (isfinite(window_opened_at)),
 target_database_oid bigint CHECK (target_database_oid BETWEEN 1 AND 4294967295),
 fingerprint text CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 ciphertext bytea CHECK (octet_length(ciphertext)>0 AND octet_length(ciphertext)<=reserved_bytes),
 ciphertext_sha256 text CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 compared_at timestamptz CHECK (isfinite(compared_at)),
 native_closed_at timestamptz CHECK (isfinite(native_closed_at)),
 verified_at timestamptz CHECK (isfinite(verified_at)),
 failed_at timestamptz CHECK (isfinite(failed_at)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 PRIMARY KEY(operation_id,source_database_id,database_oid,attempt),
 UNIQUE(operation_id,source_database_id,database_oid,attempt,verification_id),
 FOREIGN KEY(operation_id,source_database_id,database_oid,original_verification_id)
  REFERENCES project_environment_clone_postgres_verifications(operation_id,source_database_id,database_oid,verification_id),
 FOREIGN KEY(operation_id,source_database_id,database_oid,previous_attempt,previous_verification_id)
  REFERENCES project_environment_clone_postgres_verification_attempts(operation_id,source_database_id,database_oid,attempt,verification_id),
 CHECK ((attempt=1 AND verification_id=original_verification_id AND state='failed' AND previous_attempt IS NULL AND previous_verification_id IS NULL AND previous_opened_at IS NULL AND previous_closed_at IS NULL)
  OR (attempt>1 AND verification_id<>original_verification_id AND previous_attempt IS NOT NULL AND previous_attempt=attempt-1 AND previous_verification_id IS NOT NULL AND previous_verification_id<>verification_id AND previous_opened_at IS NOT NULL AND previous_closed_at IS NOT NULL AND previous_closed_at>=previous_opened_at)),
 CHECK (request_started_at IS NULL OR request_started_at>=created_at),
 CHECK (compared_at IS NULL OR compared_at>=request_started_at),
 CHECK (failed_at IS NULL OR failed_at>=request_started_at),
 CHECK (verified_at IS NULL OR verified_at>=compared_at),
 CHECK (window_opened_at IS NULL OR previous_closed_at IS NULL OR window_opened_at>=previous_closed_at),
 CHECK (native_closed_at IS NULL OR native_closed_at>=window_opened_at),
 CHECK ((state='reserved' AND request_started_at IS NULL AND window_opened_at IS NULL AND target_database_oid IS NULL)
  OR (state='verifying' AND request_started_at IS NOT NULL AND window_opened_at IS NULL AND target_database_oid IS NULL)
  OR (state IN ('compared','verified','failed') AND request_started_at IS NOT NULL AND window_opened_at IS NOT NULL AND target_database_oid IS NOT NULL)),
 CHECK ((state IN ('reserved','verifying','failed') AND ciphertext IS NULL AND fingerprint IS NULL AND ciphertext_sha256 IS NULL AND compared_at IS NULL)
  OR (state IN ('compared','verified') AND ciphertext IS NOT NULL AND fingerprint IS NOT NULL AND ciphertext_sha256 IS NOT NULL AND compared_at IS NOT NULL)),
 CHECK ((state='failed' AND failed_at IS NOT NULL AND native_closed_at IS NOT NULL AND verified_at IS NULL)
  OR (state='verified' AND failed_at IS NULL AND native_closed_at IS NOT NULL AND verified_at IS NOT NULL)
  OR (state IN ('reserved','verifying','compared') AND failed_at IS NULL AND native_closed_at IS NULL AND verified_at IS NULL))
);

-- +goose Down
ALTER TABLE project_environment_clone_postgres_verification_attempts ADD CONSTRAINT postgres_verification_attempts_down_no_ownership CHECK(false);
DROP TABLE project_environment_clone_postgres_verification_attempts;
ALTER TABLE project_environment_clone_postgres_verifications DROP CONSTRAINT IF EXISTS postgres_verification_attempt_identity;
