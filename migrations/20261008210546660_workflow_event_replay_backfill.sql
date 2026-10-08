-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_replay_jobs
    ADD COLUMN IF NOT EXISTS consumer_kind text NOT NULL DEFAULT 'application'
        CHECK (consumer_kind IN ('application','workflow')),
    ADD COLUMN IF NOT EXISTS workflow_name text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_replay_jobs'::regclass AND conname='event_replay_jobs_target_check') THEN
        ALTER TABLE event_replay_jobs ADD CONSTRAINT event_replay_jobs_target_check CHECK (
        (consumer_kind='application' AND workflow_name='') OR
        (consumer_kind='workflow' AND char_length(workflow_name) BETWEEN 1 AND 256
            AND jsonb_typeof(recipient->'workflow')='object')
    );
    END IF;
END $$;

DROP INDEX IF EXISTS event_replay_jobs_active_target_idx;
CREATE UNIQUE INDEX IF NOT EXISTS event_replay_jobs_active_target_idx
    ON event_replay_jobs(account_id,app_id,consumer_kind,subscription_id)
    WHERE state='running';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM event_replay_jobs WHERE consumer_kind='workflow') THEN
        RAISE EXCEPTION 'workflow event replay jobs must be pruned before rollback';
    END IF;
END $$;

DROP INDEX event_replay_jobs_active_target_idx;
CREATE UNIQUE INDEX event_replay_jobs_active_target_idx
    ON event_replay_jobs(account_id,subscription_id)
    WHERE state='running';
ALTER TABLE event_replay_jobs
    DROP CONSTRAINT event_replay_jobs_target_check,
    DROP COLUMN workflow_name,
    DROP COLUMN consumer_kind;
-- +goose StatementEnd
