-- filename: 20261001193719615_mirror_admission_outcomes.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE mirror_invocation_results
    ADD COLUMN admission_failure_reason text NOT NULL DEFAULT ''
        CHECK (admission_failure_reason IN (
            '',
            'scheduler_admission_timeout',
            'scheduler_admission_rejected',
            'scheduler_admission_error'
        ));

-- Older rows with no admitted instance and a synthetic crash were failed
-- before the mirror guest existed. Preserve that distinction in history.
UPDATE mirror_invocation_results
SET admission_failure_reason = 'scheduler_admission_error',
    crashed = false,
    comparison_incomplete = true,
    status_diff = false,
    schema_diff = false,
    body_diff = false
WHERE instance_id IS NULL
  AND crashed;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE mirror_invocation_results
    DROP COLUMN admission_failure_reason;
-- +goose StatementEnd
