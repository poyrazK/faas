-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_links (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 provider text NOT NULL CHECK(provider IN ('github','linear')),
 external_key text NOT NULL CHECK(octet_length(external_key) BETWEEN 1 AND 256),
 url text NOT NULL CHECK(octet_length(url) BETWEEN 1 AND 512),
 title text NOT NULL DEFAULT '' CHECK(octet_length(title)<=256),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 created_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 updated_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
 UNIQUE(issue_id,provider,external_key)
);

CREATE INDEX IF NOT EXISTS issue_tracker_links_page_idx ON issue_tracker_links(issue_id,created_at DESC,id DESC);
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_activity'::regclass AND conname='issue_activity_action_check' AND position('tracker_linked' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_activity DROP CONSTRAINT IF EXISTS issue_activity_action_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_activity'::regclass AND conname='issue_activity_action_check') THEN
  ALTER TABLE issue_activity ADD CONSTRAINT issue_activity_action_check CHECK ((action = ANY (ARRAY['created'::text, 'assigned'::text, 'resolved'::text, 'reopened'::text, 'ignored'::text, 'regressed'::text, 'impact_threshold_reached'::text, 'merged'::text, 'split'::text, 'split_rule_created'::text, 'split_rule_updated'::text, 'tracker_linked'::text, 'tracker_link_updated'::text, 'tracker_unlinked'::text])));
 END IF;
END $issue_replay$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_links) OR EXISTS(SELECT 1 FROM issue_activity WHERE action IN ('tracker_linked','tracker_link_updated','tracker_unlinked')) THEN
  RAISE EXCEPTION 'tracker references or activity are in use; remove them explicitly before rollback';
 END IF;
END $$;
DROP TABLE issue_tracker_links;
ALTER TABLE issue_activity DROP CONSTRAINT issue_activity_action_check, ADD CONSTRAINT issue_activity_action_check CHECK ((action = ANY (ARRAY['created'::text, 'assigned'::text, 'resolved'::text, 'reopened'::text, 'ignored'::text, 'regressed'::text, 'impact_threshold_reached'::text, 'merged'::text, 'split'::text, 'split_rule_created'::text, 'split_rule_updated'::text])));
-- +goose StatementEnd
