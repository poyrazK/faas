-- filename: 20261017085604854_ci_pull_request_automation.sql

-- +goose Up
ALTER TABLE ci_automation_policies ADD COLUMN IF NOT EXISTS pr_branches jsonb NOT NULL DEFAULT '[]'
 CHECK(jsonb_typeof(pr_branches)='array' AND jsonb_array_length(pr_branches) BETWEEN 0 AND 16);
ALTER TABLE ci_automation_policies DROP CONSTRAINT IF EXISTS ci_automation_policies_push_branches_check;
ALTER TABLE ci_automation_policies ADD CONSTRAINT ci_automation_policies_push_branches_check
 CHECK(jsonb_typeof(push_branches)='array' AND jsonb_array_length(push_branches) BETWEEN 0 AND 16);
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_automation_policies'::regclass AND conname='ci_automation_policy_event_filters') THEN
  ALTER TABLE ci_automation_policies ADD CONSTRAINT ci_automation_policy_event_filters
   CHECK(jsonb_array_length(push_branches)+jsonb_array_length(pr_branches)>0);
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ci_automation_events DROP CONSTRAINT IF EXISTS ci_automation_events_last_error_check;
ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_events_last_error_check CHECK(last_error IN
 ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled',
  'branch_wait','head_changed','branch_deleted','pr_wait','pr_closed','pr_draft','pr_changed'));
-- Claimed retained sources bound this journal to at most 256 PRs/account.
CREATE TABLE IF NOT EXISTS ci_automation_pull_requests (
 account_id uuid NOT NULL,
 binding_id uuid NOT NULL,
 pr_number bigint NOT NULL CHECK(pr_number>0),
 head_fence bigint NOT NULL CHECK(head_fence>0),
 PRIMARY KEY(account_id,binding_id,pr_number),
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_automation_pull_requests)
 OR EXISTS(SELECT 1 FROM ci_automation_events WHERE snapshot->'source'->>'kind'='pull_request' OR last_error IN ('pr_wait','pr_closed','pr_draft','pr_changed'))
 OR EXISTS(SELECT 1 FROM ci_automation_policies WHERE jsonb_array_length(pr_branches)>0) THEN
  RAISE EXCEPTION 'retain CI pull request policy, admission and observation evidence before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_automation_pull_requests;
ALTER TABLE ci_automation_policies DROP CONSTRAINT ci_automation_policy_event_filters;
ALTER TABLE ci_automation_policies DROP COLUMN pr_branches;
ALTER TABLE ci_automation_policies DROP CONSTRAINT ci_automation_policies_push_branches_check;
ALTER TABLE ci_automation_policies ADD CONSTRAINT ci_automation_policies_push_branches_check
 CHECK(jsonb_typeof(push_branches)='array' AND jsonb_array_length(push_branches) BETWEEN 1 AND 16);
ALTER TABLE ci_automation_events DROP CONSTRAINT ci_automation_events_last_error_check;
ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_events_last_error_check CHECK(last_error IN
 ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled','branch_wait','head_changed','branch_deleted'));
