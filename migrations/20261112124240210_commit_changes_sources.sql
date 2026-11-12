-- filename: 20261112124240210_commit_changes_sources.sql
-- migration-compat: expand

-- +goose Up
-- commit_sources is small; the constraint checks lock it briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261112140000: a changes source is a Commit source whose customer
-- outbox is filled by row triggers and whose events are enqueued into the
-- app's named queue_name instead of its request invocations. changes_feeds
-- holds the declared feeds apid installs; managed_database_id names the
-- managed database Gregale installs into (NULL for BYO). The relay polls the
-- source at most every poll_interval_ms. Nullable additions and a constant
-- default, so older apid and schedd keep reading and writing the table.
ALTER TABLE commit_sources
    ADD COLUMN IF NOT EXISTS queue_name text,
    ADD COLUMN IF NOT EXISTS changes_feeds jsonb,
    ADD COLUMN IF NOT EXISTS managed_database_id uuid,
    ADD COLUMN IF NOT EXISTS poll_interval_ms integer NOT NULL DEFAULT 5000;
ALTER TABLE commit_sources DROP CONSTRAINT IF EXISTS commit_sources_poll_interval_check;
ALTER TABLE commit_sources ADD CONSTRAINT commit_sources_poll_interval_check
    CHECK (poll_interval_ms BETWEEN 1000 AND 600000);
ALTER TABLE commit_sources DROP CONSTRAINT IF EXISTS commit_sources_changes_check;
ALTER TABLE commit_sources ADD CONSTRAINT commit_sources_changes_check CHECK (
    (queue_name IS NULL AND changes_feeds IS NULL AND managed_database_id IS NULL AND poll_interval_ms = 5000)
    OR (queue_name ~ '^[a-z][a-z0-9-]{0,62}$'
        AND jsonb_typeof(changes_feeds) = 'array'
        AND jsonb_array_length(changes_feeds) BETWEEN 1 AND 20
        AND destination = 'operation' AND contract_version = 1
        AND operation_policy IS NULL AND NOT allow_tenant_selection));
-- One changes source per app (ADR-20261112140000 §8).
CREATE UNIQUE INDEX IF NOT EXISTS commit_sources_changes_app
    ON commit_sources (app_id) WHERE queue_name IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS commit_sources_changes_app;
ALTER TABLE commit_sources DROP CONSTRAINT IF EXISTS commit_sources_changes_check;
ALTER TABLE commit_sources DROP CONSTRAINT IF EXISTS commit_sources_poll_interval_check;
ALTER TABLE commit_sources
    DROP COLUMN IF EXISTS poll_interval_ms,
    DROP COLUMN IF EXISTS managed_database_id,
    DROP COLUMN IF EXISTS changes_feeds,
    DROP COLUMN IF EXISTS queue_name;
-- +goose StatementEnd
