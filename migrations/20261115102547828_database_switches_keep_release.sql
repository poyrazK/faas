-- migration-compat: expand
-- filename: 20261115102547828_database_switches_keep_release.sql
--
-- ADR-20261023181931 amendment 3 (`gregale postgres restore --switch`): a
-- database switch can keep the live release. It restores the database to a
-- point the customer picks and moves the app to the copy without changing
-- its code, so it undoes no release and records no undone deployment. The
-- undone_deployment_ids CHECK widens from 1..64 to 0..64 in three steps, each
-- its own transaction: the wider constraint is added NOT VALID under a new
-- name, validated (every existing row passes), and then the narrower one is
-- dropped. The previous release writes only rows both constraints allow.
-- Every step is re-runnable.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $widen$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'release_database_switches'::regclass
      AND conname = 'release_database_switches_undone_deployment_ids_v2_check'
  ) THEN
    ALTER TABLE release_database_switches
      ADD CONSTRAINT release_database_switches_undone_deployment_ids_v2_check
      CHECK (cardinality(undone_deployment_ids) BETWEEN 0 AND 64) NOT VALID;
  END IF;
END
$widen$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE release_database_switches VALIDATE CONSTRAINT release_database_switches_undone_deployment_ids_v2_check;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE release_database_switches DROP CONSTRAINT IF EXISTS release_database_switches_undone_deployment_ids_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
DELETE FROM release_database_switches WHERE cardinality(undone_deployment_ids) = 0;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $narrow$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'release_database_switches'::regclass
      AND conname = 'release_database_switches_undone_deployment_ids_check'
  ) THEN
    ALTER TABLE release_database_switches
      ADD CONSTRAINT release_database_switches_undone_deployment_ids_check
      CHECK (cardinality(undone_deployment_ids) BETWEEN 1 AND 64) NOT VALID;
  END IF;
END
$narrow$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE release_database_switches VALIDATE CONSTRAINT release_database_switches_undone_deployment_ids_check;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE release_database_switches DROP CONSTRAINT IF EXISTS release_database_switches_undone_deployment_ids_v2_check;
-- +goose StatementEnd
