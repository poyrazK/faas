-- +goose Up
-- +goose StatementBegin
CREATE TABLE event_replay_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    subscription_id uuid NOT NULL,
    subscription_revision text NOT NULL CHECK (length(subscription_revision) = 64),
    recipient jsonb NOT NULL CHECK (jsonb_typeof(recipient) = 'object'),
    from_at timestamptz NOT NULL,
    until_at timestamptz NOT NULL,
    cutoff_at timestamptz NOT NULL,
    earliest_retained_at timestamptz,
    cursor_at timestamptz NOT NULL,
    cursor_outbox_id bigint NOT NULL DEFAULT 0,
    duplicate_policy text NOT NULL DEFAULT 'skip_existing' CHECK (duplicate_policy = 'skip_existing'),
    state text NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'completed', 'completed_with_failures')),
    scan_complete boolean NOT NULL DEFAULT false,
    scanned_count bigint NOT NULL DEFAULT 0 CHECK (scanned_count >= 0),
    matched_count bigint NOT NULL DEFAULT 0 CHECK (matched_count >= 0),
    filtered_count bigint NOT NULL DEFAULT 0 CHECK (filtered_count >= 0),
    skipped_captured_count bigint NOT NULL DEFAULT 0 CHECK (skipped_captured_count >= 0),
    skipped_unknown_count bigint NOT NULL DEFAULT 0 CHECK (skipped_unknown_count >= 0),
    skipped_existing_count bigint NOT NULL DEFAULT 0 CHECK (skipped_existing_count >= 0),
    skipped_unsettled_count bigint NOT NULL DEFAULT 0 CHECK (skipped_unsettled_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (from_at < cutoff_at AND cutoff_at <= until_at),
    CHECK ((state = 'running' AND completed_at IS NULL) OR (state <> 'running' AND completed_at IS NOT NULL))
);
CREATE INDEX event_replay_jobs_running_idx ON event_replay_jobs (created_at, id) WHERE state = 'running';
CREATE INDEX event_replay_jobs_account_idx ON event_replay_jobs (account_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX event_replay_jobs_active_target_idx ON event_replay_jobs (account_id, subscription_id) WHERE state = 'running';

CREATE TABLE event_replay_job_items (
    job_id uuid NOT NULL REFERENCES event_replay_jobs(id) ON DELETE CASCADE,
    outbox_id bigint NOT NULL,
    accepted_at timestamptz NOT NULL,
    event_source text NOT NULL,
    event_id text NOT NULL,
    event_type text NOT NULL,
    schema_version text NOT NULL DEFAULT '',
    state text NOT NULL CHECK (state IN ('pending', 'processing', 'enqueued', 'filtered', 'failed', 'skipped_captured', 'skipped_unknown', 'skipped_existing', 'skipped_unsettled')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    failure_code text NOT NULL DEFAULT '',
    last_error text NOT NULL DEFAULT '',
    retryable boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, outbox_id)
);
CREATE INDEX event_replay_job_items_due_idx ON event_replay_job_items (job_id, accepted_at, outbox_id) WHERE state = 'pending';
CREATE INDEX event_replay_job_items_state_idx ON event_replay_job_items (job_id, state, accepted_at, outbox_id);
CREATE INDEX event_replay_job_items_page_idx ON event_replay_job_items (job_id, accepted_at, outbox_id);

ALTER TABLE event_fanout_recipients
    ADD COLUMN backfill_job_id uuid REFERENCES event_replay_jobs(id) ON DELETE SET NULL;
CREATE INDEX event_fanout_recipients_backfill_idx ON event_fanout_recipients (backfill_job_id, outbox_id) WHERE backfill_job_id IS NOT NULL;

ALTER TABLE event_fanout_attempt_history DROP CONSTRAINT event_fanout_attempt_history_action_check;
ALTER TABLE event_fanout_attempt_history ADD CONSTRAINT event_fanout_attempt_history_action_check
    CHECK (action IN ('fanout_attempt', 'operator_replay', 'backfill_attempt'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM event_replay_jobs WHERE state = 'running') THEN
        RAISE EXCEPTION 'running event replay jobs must finish before rollback';
    END IF;
    IF EXISTS (SELECT 1 FROM event_fanout_recipients WHERE backfill_job_id IS NOT NULL)
       OR EXISTS (SELECT 1 FROM event_fanout_attempt_history WHERE action = 'backfill_attempt') THEN
        RAISE EXCEPTION 'backfill recipient lineage and attempt history must expire before rollback';
    END IF;
END $$;
DROP INDEX event_fanout_recipients_backfill_idx;
ALTER TABLE event_fanout_recipients DROP COLUMN backfill_job_id;
ALTER TABLE event_fanout_attempt_history DROP CONSTRAINT event_fanout_attempt_history_action_check;
ALTER TABLE event_fanout_attempt_history ADD CONSTRAINT event_fanout_attempt_history_action_check
    CHECK (action IN ('fanout_attempt', 'operator_replay'));
DROP TABLE event_replay_job_items;
DROP TABLE event_replay_jobs;
-- +goose StatementEnd
