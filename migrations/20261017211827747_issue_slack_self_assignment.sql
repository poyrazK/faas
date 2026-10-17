-- filename: 20261017211827747_issue_slack_self_assignment.sql

-- +goose Up
-- Existing messages did not contain Assign to me, and existing identity links
-- delegated only acknowledgment/snoozing. Neither acquires new authority on upgrade.
ALTER TABLE issue_slack_action_messages ADD COLUMN IF NOT EXISTS can_assign boolean NOT NULL DEFAULT false;
ALTER TABLE issue_slack_identities ADD COLUMN IF NOT EXISTS can_assign boolean NOT NULL DEFAULT false;

-- +goose Down
-- Assignment activity and its ordinary webhook receipts remain intact.
ALTER TABLE issue_slack_identities DROP COLUMN IF EXISTS can_assign;
ALTER TABLE issue_slack_action_messages DROP COLUMN IF EXISTS can_assign;
