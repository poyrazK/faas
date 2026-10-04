-- ADR-375: reserve aggregate canonical read credits before first dispatch.
-- Credits are planned worst-case work, not measured usage or a billing allowance.
-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_verification_read_budgets (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 database_oid bigint NOT NULL CHECK(database_oid BETWEEN 1 AND 4294967295),
 original_verification_id uuid NOT NULL UNIQUE,
 account_id uuid NOT NULL REFERENCES accounts(id),
 project_id uuid NOT NULL REFERENCES projects(id),
 read_bytes bigint NOT NULL CHECK(read_bytes BETWEEN 1 AND 3298534883328),
 sort_memory_bytes bigint NOT NULL CHECK(sort_memory_bytes BETWEEN 32 AND 8388608),
 sort_disk_bytes bigint NOT NULL CHECK(sort_disk_bytes BETWEEN 32 AND 68719476736),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(operation_id,source_database_id,database_oid),
 UNIQUE(operation_id,source_database_id,database_oid,original_verification_id),
 FOREIGN KEY(operation_id,source_database_id,database_oid,original_verification_id)
  REFERENCES project_environment_clone_postgres_verifications(operation_id,source_database_id,database_oid,verification_id)
);
CREATE TABLE IF NOT EXISTS project_environment_clone_postgres_verification_read_debits (
 operation_id uuid NOT NULL,
 source_database_id uuid NOT NULL,
 database_oid bigint NOT NULL CHECK(database_oid BETWEEN 1 AND 4294967295),
 original_verification_id uuid NOT NULL,
 attempt smallint NOT NULL CHECK(attempt BETWEEN 1 AND 3),
 verification_id uuid NOT NULL UNIQUE,
 retry_attempt smallint,
 retry_verification_id uuid,
 read_bytes bigint NOT NULL CHECK(read_bytes BETWEEN 1 AND 1099511627776),
 sort_memory_bytes bigint NOT NULL CHECK(sort_memory_bytes BETWEEN 32 AND 8388608),
 sort_disk_bytes bigint NOT NULL CHECK(sort_disk_bytes BETWEEN 32 AND 68719476736),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(operation_id,source_database_id,database_oid,attempt),
 FOREIGN KEY(operation_id,source_database_id,database_oid,original_verification_id)
  REFERENCES project_environment_clone_postgres_verification_read_budgets(operation_id,source_database_id,database_oid,original_verification_id),
 FOREIGN KEY(operation_id,source_database_id,database_oid,retry_attempt,retry_verification_id)
  REFERENCES project_environment_clone_postgres_verification_attempts(operation_id,source_database_id,database_oid,attempt,verification_id),
 CHECK((attempt=1 AND verification_id=original_verification_id AND retry_attempt IS NULL AND retry_verification_id IS NULL)
  OR (attempt>1 AND verification_id<>original_verification_id AND retry_attempt IS NOT NULL AND retry_attempt=attempt AND retry_verification_id IS NOT NULL AND retry_verification_id=verification_id))
);

-- +goose Down
ALTER TABLE project_environment_clone_postgres_verification_read_budgets ADD CONSTRAINT postgres_verification_read_budgets_down_no_ownership CHECK(false);
ALTER TABLE project_environment_clone_postgres_verification_read_debits ADD CONSTRAINT postgres_verification_read_allocations_down_no_ownership CHECK(false);
DROP TABLE project_environment_clone_postgres_verification_read_debits;
DROP TABLE project_environment_clone_postgres_verification_read_budgets;
