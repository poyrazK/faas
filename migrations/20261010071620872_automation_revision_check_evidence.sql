-- +goose Up
-- Client-reported metadata, saved in the same transaction as publication.
-- Older and unchecked revisions deliberately have no evidence.
ALTER TABLE workflow_automation_revisions ADD COLUMN IF NOT EXISTS check_evidence jsonb;

-- +goose Down
ALTER TABLE workflow_automation_revisions DROP COLUMN IF EXISTS check_evidence;
