-- filename: 20261126095039200_alert_rule_environment_index.sql
-- migration-compat: expand
-- ADR-20261104120000 amendment 2026-11-22: lists a stage's alert rules, and
-- makes an app rule's name unique within one environment, so a stage can
-- hold a copy of a production rule under the same name once a later
-- contract migration drops alert_rules_app_name_uniq. Production is the zero
-- UUID in the identity; (app_id, name) leads so the preset lookup's name
-- prefix scan keeps an index. Built concurrently so rule writes never wait
-- on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'alert_rules_environment_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX alert_rules_environment_idx';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'alert_rules_app_environment_name_uniq' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX alert_rules_app_environment_name_uniq';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS alert_rules_environment_idx
    ON alert_rules (environment_id) WHERE environment_id IS NOT NULL;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS alert_rules_app_environment_name_uniq
    ON alert_rules (app_id, name, (COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'::uuid)))
    WHERE app_id IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_app_environment_name_uniq;
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_environment_idx;
