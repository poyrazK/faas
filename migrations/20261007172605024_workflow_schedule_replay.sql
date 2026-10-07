-- +goose Up
ALTER TABLE workflow_schedule_occurrences
    ADD COLUMN definition_hash text NOT NULL DEFAULT '',
    ADD COLUMN replay_run_id uuid,
    ADD COLUMN replayed_at timestamptz,
    ADD CONSTRAINT workflow_schedule_occurrences_definition_hash_check
        CHECK (definition_hash = '' OR definition_hash ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT workflow_schedule_occurrences_replay_pair_check
        CHECK ((replay_run_id IS NULL) = (replayed_at IS NULL));

-- Keep replay identity after workflow-run retention so a retry of the replay
-- request cannot create a second run for the same schedule occurrence.
CREATE INDEX workflow_schedule_occurrences_replay_idx
    ON workflow_schedule_occurrences (app_id, scheduled_for, id)
    WHERE status IN ('skipped_overlap', 'skipped_quota') AND replay_run_id IS NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM workflow_schedule_occurrences WHERE replay_run_id IS NOT NULL) THEN
        RAISE EXCEPTION 'schedule replays exist; preserve occurrence replay identity before rollback';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX workflow_schedule_occurrences_replay_idx;
ALTER TABLE workflow_schedule_occurrences
    DROP CONSTRAINT workflow_schedule_occurrences_definition_hash_check,
    DROP CONSTRAINT workflow_schedule_occurrences_replay_pair_check,
    DROP COLUMN definition_hash,
    DROP COLUMN replay_run_id,
    DROP COLUMN replayed_at;
