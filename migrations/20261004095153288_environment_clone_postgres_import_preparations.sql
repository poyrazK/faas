-- +goose Up
-- ADR-531: new imports retain the exact original encrypted child preparation.
-- Legacy owners stay owned and unbound; no backfill can prove their SQL intent.
ALTER TABLE project_environment_clone_postgres_database_sql_pins
 ADD CONSTRAINT postgres_database_sql_pins_import_identity
 UNIQUE (operation_id,source_database_id,database_oid,ciphertext_sha256);

ALTER TABLE project_environment_clone_postgres_imports
 ADD COLUMN database_sql_pins_ciphertext_sha256 text
  CHECK (database_sql_pins_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 ADD COLUMN database_plan_ciphertext_sha256 text
  CHECK (database_plan_ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
 ADD COLUMN archive_reservation_sha256 text
  CHECK (archive_reservation_sha256 ~ '^[0-9a-f]{64}$'),
 ADD CONSTRAINT postgres_imports_preparation_tuple CHECK (
  (database_sql_pins_ciphertext_sha256 IS NULL AND database_plan_ciphertext_sha256 IS NULL AND archive_reservation_sha256 IS NULL)
  OR (database_sql_pins_ciphertext_sha256 IS NOT NULL AND database_plan_ciphertext_sha256 IS NOT NULL AND archive_reservation_sha256 IS NOT NULL)),
 ADD CONSTRAINT postgres_imports_original_preparation
 FOREIGN KEY (operation_id,source_database_id,database_oid,database_sql_pins_ciphertext_sha256)
 REFERENCES project_environment_clone_postgres_database_sql_pins(operation_id,source_database_id,database_oid,ciphertext_sha256);

-- +goose Down
-- Removing occupied preparation lineage would permit an import to be adopted.
ALTER TABLE project_environment_clone_postgres_imports
 ADD CONSTRAINT postgres_imports_down_no_preparation CHECK (database_sql_pins_ciphertext_sha256 IS NULL);
ALTER TABLE project_environment_clone_postgres_imports
 DROP CONSTRAINT postgres_imports_original_preparation,
 DROP CONSTRAINT postgres_imports_preparation_tuple,
 DROP CONSTRAINT postgres_imports_down_no_preparation,
 DROP COLUMN database_sql_pins_ciphertext_sha256,
 DROP COLUMN database_plan_ciphertext_sha256,
 DROP COLUMN archive_reservation_sha256;
ALTER TABLE project_environment_clone_postgres_database_sql_pins
 DROP CONSTRAINT postgres_database_sql_pins_import_identity;
