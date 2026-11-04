-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployments ADD COLUMN rollback_release_review jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE deployments ADD CONSTRAINT deployments_rollback_release_review_check CHECK (
  coalesce(jsonb_typeof(rollback_release_review) = 'object' AND
    (rollback_release_review = '{}'::jsonb OR
      (jsonb_typeof(rollback_release_review->'prepared_epoch') = 'number' AND
       jsonb_typeof(rollback_release_review->'runs') = 'array')), false)
) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployments DROP COLUMN rollback_release_review;
