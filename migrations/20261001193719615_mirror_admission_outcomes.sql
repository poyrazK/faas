-- filename: 20261001193719615_mirror_admission_outcomes.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE mirror_invocation_results
    ADD COLUMN IF NOT EXISTS admission_failure_reason text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'mirror_invocation_results_admission_failure_reason_check'
          AND conrelid = 'mirror_invocation_results'::regclass
    ) THEN
        ALTER TABLE mirror_invocation_results
            ADD CONSTRAINT mirror_invocation_results_admission_failure_reason_check
                CHECK (admission_failure_reason IN (
                    '',
                    'scheduler_admission_timeout',
                    'scheduler_admission_rejected',
                    'scheduler_admission_error'
                ));
    END IF;
END$$;

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
    DROP CONSTRAINT IF EXISTS mirror_invocation_results_admission_failure_reason_check;
ALTER TABLE mirror_invocation_results
    DROP COLUMN IF EXISTS admission_failure_reason;
-- +goose StatementEnd
