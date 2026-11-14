-- filename: 20261112175759744_event_subscription_drop_unscoped_identity.sql
-- migration-compat: contract
-- ADR-20261104120000: the app-wide subscription identity would refuse a
-- stage subscription with production's source, type and filter. The
-- per-environment unique index (20261112175755118) carries the identity now.
-- Contract: the previous release upserts ON CONFLICT on this index, so only a
-- full release drops it. Until then a stage subscription that repeats a
-- production one is refused as a conflict.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS event_subscriptions_identity_uniq;

-- +goose Down
SET lock_timeout = '5s';
DELETE FROM event_subscriptions WHERE deployment_scope<>'';
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS event_subscriptions_identity_uniq
    ON event_subscriptions (app_id, source, type, filter);
