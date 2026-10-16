-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_sync_triage (
 incident_id uuid PRIMARY KEY,
 link_id uuid NOT NULL REFERENCES issue_tracker_links(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision>0),
 acknowledged_by uuid,
 claimed_by uuid
);

CREATE INDEX IF NOT EXISTS issue_tracker_sync_triage_link_idx ON issue_tracker_sync_triage(link_id);

CREATE TABLE IF NOT EXISTS issue_tracker_sync_triage_activity (
 id uuid PRIMARY KEY,
 incident_id uuid NOT NULL REFERENCES issue_tracker_sync_triage(incident_id) ON DELETE CASCADE,
 actor_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('acknowledge','claim','release')),
 revision bigint NOT NULL CHECK(revision>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(incident_id,revision)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Tracker triage history must be preserved; rollback is not supported'; END $$;
-- +goose StatementEnd
