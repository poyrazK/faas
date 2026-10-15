-- Operator actions historically use an account subject and put the affected
-- resource in data. Index that explicit reference so incident evidence can
-- include existing cancellations/retries/outcomes without account-wide scans.
-- Like the other event indexes, this runs inside db.MigrateUp's transaction.
-- The partial predicate keeps ordinary request/wake traffic out of the index.

-- +goose Up
CREATE INDEX IF NOT EXISTS events_operator_resource_at_id_idx
    ON events ((coalesce(nullif(data->>'target_id', ''), nullif(data->>'run_id', ''), nullif(data->>'deployment_id', ''))), at DESC, id DESC)
    WHERE kind LIKE 'operator.action.%';

-- +goose Down
DROP INDEX IF EXISTS events_operator_resource_at_id_idx;
