-- filename: 20261112175755118_event_subscription_scope_indexes.sql
-- migration-compat: expand
-- ADR-20261104120000: a stage may subscribe to the same source, type and
-- filter as production, so a subscription's identity is unique within one
-- environment of an app. Environment deletion finds its subscriptions by
-- environment_id. Both indexes are built concurrently so subscription writes
-- never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'event_subscriptions_environment_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX event_subscriptions_environment_idx';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'event_subscriptions_scope_identity_uniq' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX event_subscriptions_scope_identity_uniq';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS event_subscriptions_environment_idx
    ON event_subscriptions (environment_id) WHERE environment_id IS NOT NULL;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS event_subscriptions_scope_identity_uniq
    ON event_subscriptions (app_id, deployment_scope, source, type, filter);

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS event_subscriptions_scope_identity_uniq;
DROP INDEX CONCURRENTLY IF EXISTS event_subscriptions_environment_idx;
