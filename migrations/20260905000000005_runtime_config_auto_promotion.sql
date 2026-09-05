-- filename: 20260905000000005_runtime_config_auto_promotion.sql
-- ADR-132 follow-up: opt-in progressive promotion for daemon canaries.
-- Safety rollback remains enabled for all canaries; this flag only controls
-- whether a healthy canary advances through the controller's fixed ladder.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE runtime_config_entries
    ADD COLUMN IF NOT EXISTS rollout_auto_promote boolean NOT NULL DEFAULT false;

ALTER TABLE runtime_config_revisions
    ADD COLUMN IF NOT EXISTS rollout_auto_promote boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE runtime_config_revisions
    DROP COLUMN IF EXISTS rollout_auto_promote;
ALTER TABLE runtime_config_entries
    DROP COLUMN IF EXISTS rollout_auto_promote;
-- +goose StatementEnd
