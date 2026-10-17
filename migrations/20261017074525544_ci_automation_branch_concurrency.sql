-- filename: 20261017074525544_ci_automation_branch_concurrency.sql

-- +goose Up
ALTER TABLE ci_automation_policies ADD COLUMN IF NOT EXISTS latest_only boolean NOT NULL DEFAULT false;
ALTER TABLE ci_automation_events ADD COLUMN IF NOT EXISTS superseded_by uuid,
 ADD COLUMN IF NOT EXISTS superseded_sha text,
 ADD COLUMN IF NOT EXISTS superseded_at timestamptz;
ALTER TABLE ci_automation_events DROP CONSTRAINT IF EXISTS ci_automation_events_last_error_check;
ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_events_last_error_check CHECK(last_error IN
 ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled','branch_wait','head_changed','branch_deleted'));
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_automation_events'::regclass AND conname='ci_automation_superseded_identity') THEN
  ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_superseded_identity CHECK(
   (superseded_sha IS NULL AND superseded_at IS NULL AND superseded_by IS NULL)
   OR (superseded_sha IS NOT NULL AND superseded_sha ~ '^[0-9a-f]{40}$' AND superseded_at IS NOT NULL AND (superseded_by IS NULL OR superseded_by<>id)));
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='ci_automation_events'::regclass AND conname='ci_automation_superseded_event') THEN
  ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_superseded_event
   FOREIGN KEY(account_id,superseded_by) REFERENCES ci_automation_events(account_id,id);
 END IF;
END $$;
-- +goose StatementEnd
-- Created only for claimed retained sources; at most 256 groups/account.
CREATE TABLE IF NOT EXISTS ci_automation_branches (
 account_id uuid NOT NULL,
 binding_id uuid NOT NULL,
 branch text NOT NULL CHECK(octet_length(branch) BETWEEN 1 AND 255),
 head_fence bigint NOT NULL CHECK(head_fence>0),
 PRIMARY KEY(account_id,binding_id,branch),
 FOREIGN KEY(account_id,binding_id) REFERENCES ci_github_bindings(account_id,id) ON DELETE CASCADE
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_automation_branches) OR EXISTS(SELECT 1 FROM ci_automation_events WHERE superseded_at IS NOT NULL OR last_error IN ('branch_wait','head_changed','branch_deleted'))
 OR EXISTS(SELECT 1 FROM ci_automation_policies WHERE latest_only) THEN
  RAISE EXCEPTION 'retain CI branch serialization and supersession evidence before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_automation_branches;
ALTER TABLE ci_automation_events DROP CONSTRAINT ci_automation_superseded_event;
ALTER TABLE ci_automation_events DROP CONSTRAINT ci_automation_superseded_identity;
ALTER TABLE ci_automation_events DROP CONSTRAINT ci_automation_events_last_error_check;
ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_events_last_error_check CHECK(last_error IN
 ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled'));
ALTER TABLE ci_automation_events DROP COLUMN superseded_by, DROP COLUMN superseded_sha, DROP COLUMN superseded_at;
ALTER TABLE ci_automation_policies DROP COLUMN latest_only;
