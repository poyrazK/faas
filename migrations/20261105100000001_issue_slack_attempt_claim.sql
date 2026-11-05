-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A claim may start preparing a request without ever reaching the transport.
-- Keep this separate from the count of completed transport attempts.
ALTER TABLE issue_team_slack_deliveries ADD COLUMN IF NOT EXISTS attempt_claim_id uuid;

-- +goose Down
ALTER TABLE issue_team_slack_deliveries DROP COLUMN IF EXISTS attempt_claim_id;
