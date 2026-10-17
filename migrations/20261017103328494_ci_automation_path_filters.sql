-- filename: 20261017103328494_ci_automation_path_filters.sql

-- +goose Up
ALTER TABLE ci_automation_policies ADD COLUMN IF NOT EXISTS include_paths jsonb NOT NULL DEFAULT '[]'
 CHECK(jsonb_typeof(include_paths)='array' AND jsonb_array_length(include_paths) BETWEEN 0 AND 16);
ALTER TABLE ci_automation_policies ADD COLUMN IF NOT EXISTS exclude_paths jsonb NOT NULL DEFAULT '[]'
 CHECK(jsonb_typeof(exclude_paths)='array' AND jsonb_array_length(exclude_paths) BETWEEN 0 AND 16);
ALTER TABLE ci_automation_events ADD COLUMN IF NOT EXISTS path_decision jsonb
 CHECK(path_decision IS NULL OR (jsonb_typeof(path_decision)='object' AND octet_length(path_decision::text)<=2048
 AND path_decision->>'head_sha' IS NOT NULL AND path_decision->>'head_sha'=snapshot->'source'->>'commit_sha'
 AND path_decision->>'outcome' IS NOT NULL AND path_decision->>'reason' IS NOT NULL
 AND ((path_decision->>'outcome'='run' AND path_decision->>'reason' IN ('paths_matched','changes_unavailable','prior_source_unsettled'))
 OR (path_decision->>'outcome'='skip' AND path_decision->>'reason'='paths_filtered' AND status='ignored' AND last_error='paths_filtered'))));
ALTER TABLE ci_automation_events DROP CONSTRAINT IF EXISTS ci_automation_events_last_error_check;
ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_events_last_error_check CHECK(last_error IN
 ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled',
  'branch_wait','head_changed','branch_deleted','pr_wait','pr_closed','pr_draft','pr_changed','paths_filtered'));
-- One immutable projection per retained skipped event; no execution or native work.
CREATE TABLE IF NOT EXISTS ci_automation_path_checks (
 account_id uuid NOT NULL,
 event_id uuid PRIMARY KEY,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=2048),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','ignored','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 8),
 lease_token uuid,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 check_id bigint NOT NULL DEFAULT 0 CHECK(check_id>=0),
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','publish_failed','access_denied','unavailable','attempts_exhausted')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,event_id) REFERENCES ci_automation_events(account_id,id) ON DELETE CASCADE,
 CHECK((status='sending' AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
 OR (status<>'sending' AND lease_token IS NULL AND lease_until IS NULL)),
 CHECK(status<>'sent' OR check_id>0)
);
CREATE INDEX IF NOT EXISTS ci_automation_path_checks_due_idx ON ci_automation_path_checks(account_id,next_attempt_at,created_at,event_id)
 WHERE status IN ('pending','sending');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_automation_policies WHERE jsonb_array_length(include_paths)+jsonb_array_length(exclude_paths)>0)
 OR EXISTS(SELECT 1 FROM ci_automation_events WHERE path_decision IS NOT NULL OR last_error='paths_filtered'
 OR coalesce(jsonb_array_length(snapshot->'policy'->'include_paths'),0)+coalesce(jsonb_array_length(snapshot->'policy'->'exclude_paths'),0)>0)
 OR EXISTS(SELECT 1 FROM ci_automation_path_checks) THEN
  RAISE EXCEPTION 'retain CI path filter policies and decision evidence before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_automation_path_checks;
ALTER TABLE ci_automation_events DROP COLUMN path_decision;
ALTER TABLE ci_automation_policies DROP COLUMN include_paths;
ALTER TABLE ci_automation_policies DROP COLUMN exclude_paths;
ALTER TABLE ci_automation_events DROP CONSTRAINT ci_automation_events_last_error_check;
ALTER TABLE ci_automation_events ADD CONSTRAINT ci_automation_events_last_error_check CHECK(last_error IN
 ('','unavailable','capacity','authority_revoked','policy_disabled','profile_disabled','binding_disabled',
  'branch_wait','head_changed','branch_deleted','pr_wait','pr_closed','pr_draft','pr_changed'));
