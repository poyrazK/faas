-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_auto_sync (
 link_id uuid PRIMARY KEY REFERENCES issue_tracker_links(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 source_issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 binding_fingerprint text NOT NULL CHECK(binding_fingerprint ~ '^[a-f0-9]{64}$'),
 baseline jsonb NOT NULL CHECK(jsonb_typeof(baseline)='object'),
 source_state text NOT NULL CHECK(source_state IN ('open','resolved')),
 pending_state text NOT NULL DEFAULT '' CHECK(pending_state IN ('','open','resolved')),
 outcome text NOT NULL DEFAULT 'matched' CHECK(outcome IN ('disabled','matched','pending','applied','error','conflict','blocked')),
 reason text NOT NULL DEFAULT '' CHECK(octet_length(reason)<=128),
 claim_id uuid,
 claim_until timestamptz,
 last_checked_at timestamptz,
 next_check_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS issue_tracker_auto_sync_due_idx ON issue_tracker_auto_sync(next_check_at,link_id) WHERE enabled AND outcome IN ('matched','pending','applied','error');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_auto_sync) THEN
  RAISE EXCEPTION 'automatic tracker sync policies are in use; disable and preserve them before rollback';
 END IF;
END $$;
DROP TABLE issue_tracker_auto_sync;
-- +goose StatementEnd
