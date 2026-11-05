-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS rollback_release_review jsonb NOT NULL DEFAULT '{}'::jsonb;
-- +goose StatementBegin
DO $$
BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_constraint
 WHERE conrelid='deployments'::regclass AND conname='deployments_rollback_release_review_check') THEN
ALTER TABLE deployments ADD CONSTRAINT deployments_rollback_release_review_check CHECK (
  coalesce(jsonb_typeof(rollback_release_review) = 'object' AND
    (rollback_release_review = '{}'::jsonb OR
      (jsonb_typeof(rollback_release_review->'prepared_epoch') = 'number' AND
       jsonb_typeof(rollback_release_review->'runs') = 'array')), false)
) NOT VALID;
END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployments DROP COLUMN rollback_release_review;
