-- migration-compat: expand
-- filename: 20261104212333498_event_replay_retry_fairness_index.sql
-- +goose NO TRANSACTION

-- +goose Up
-- The partial index keeps fair retry selection bounded as histories grow.
SET lock_timeout = '5s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS event_replay_job_items_retry_fair_idx
    ON event_replay_job_items (job_id, attempts, accepted_at, outbox_id)
    WHERE state = 'failed' AND retryable;
-- Do not silently accept an invalid index left by a cancelled build.
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid=to_regclass('event_replay_job_items_retry_fair_idx') AND indisvalid AND indisready) THEN
        RAISE EXCEPTION 'event replay retry fairness index is invalid; repair the index before retrying this migration';
    END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS event_replay_job_items_retry_fair_idx;
