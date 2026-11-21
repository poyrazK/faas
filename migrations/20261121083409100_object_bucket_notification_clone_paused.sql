-- filename: 20261121083409100_object_bucket_notification_clone_paused.sql
-- migration-compat: expand
-- ADR-20261104120000 (amendment 2026-11-16): a full environment clone copies
-- each captured bucket's notification configuration into its target bucket
-- paused. While clone_paused is set the rules are stored but match no event;
-- resuming the stage's automation or any explicit write of the rules clears
-- it (the writers do, so a write of identical rules clears it too).

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE object_bucket_notifications ADD COLUMN IF NOT EXISTS clone_paused boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE object_bucket_notifications DROP COLUMN IF EXISTS clone_paused;
-- +goose StatementEnd
