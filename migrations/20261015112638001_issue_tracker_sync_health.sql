-- +goose Up
ALTER TABLE issue_tracker_auto_sync ADD COLUMN IF NOT EXISTS last_successful_at timestamptz;

CREATE TABLE IF NOT EXISTS issue_tracker_sync_history (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 link_id uuid NOT NULL REFERENCES issue_tracker_links(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 source_issue_id uuid NOT NULL,
 policy_revision bigint NOT NULL CHECK(policy_revision>0),
 claim_id uuid,
 event_type text NOT NULL CHECK(event_type IN ('enabled','disabled','checked','pulled','inspected')),
 direction text NOT NULL CHECK(direction IN ('outbound','inbound','both')),
 outcome text NOT NULL CHECK(outcome IN ('matched','pending','applied','error','conflict','blocked','disabled')),
 reason text NOT NULL DEFAULT '' CHECK(octet_length(reason)<=128),
 source_state text NOT NULL CHECK(source_state IN ('open','resolved')),
 observed_state text NOT NULL DEFAULT '' CHECK(observed_state IN ('','open','resolved')),
 pending_state text NOT NULL DEFAULT '' CHECK(pending_state IN ('','open','resolved')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT issue_tracker_sync_history_claim_key UNIQUE(link_id,claim_id)
);

CREATE INDEX IF NOT EXISTS issue_tracker_sync_history_page_idx ON issue_tracker_sync_history(link_id,created_at DESC,id DESC);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_sync_history) OR EXISTS(SELECT 1 FROM issue_tracker_auto_sync WHERE last_successful_at IS NOT NULL) THEN
  RAISE EXCEPTION 'Sync health receipts are in use; preserve this history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE issue_tracker_sync_history;
ALTER TABLE issue_tracker_auto_sync DROP COLUMN last_successful_at;
