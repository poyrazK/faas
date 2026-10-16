-- +goose Up
CREATE TABLE IF NOT EXISTS issue_tracker_sync_notification_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 failure_threshold integer NOT NULL CHECK(failure_threshold>0),
 updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS issue_tracker_sync_incidents (
 link_id uuid PRIMARY KEY REFERENCES issue_tracker_links(id) ON DELETE CASCADE,
 settings_revision bigint NOT NULL CHECK(settings_revision>0),
 failures integer NOT NULL DEFAULT 0 CHECK(failures>=0),
 incident_id uuid,
 announced_kind text NOT NULL DEFAULT '' CHECK(announced_kind IN ('','paused','closure','failure')),
 issue_id uuid,
 team_id uuid,
 recipient_account_ids uuid[] NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS issue_tracker_sync_notifications (
 id uuid PRIMARY KEY,
 link_id uuid NOT NULL REFERENCES issue_tracker_links(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL,
 issue_id uuid NOT NULL,
 incident_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('paused','closure','failure','recovered')),
 outcome text NOT NULL CHECK(outcome IN ('matched','pending','applied','error','conflict','blocked','disabled')),
 reason text NOT NULL DEFAULT '' CHECK(octet_length(reason)<=128),
 team_id uuid,
 recipient_account_ids uuid[] NOT NULL CHECK(cardinality(recipient_account_ids)>0),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(incident_id,kind)
);

CREATE INDEX IF NOT EXISTS issue_tracker_sync_notifications_page_idx ON issue_tracker_sync_notifications(app_id,created_at DESC,id DESC);

CREATE INDEX IF NOT EXISTS issue_tracker_sync_notifications_link_idx ON issue_tracker_sync_notifications(link_id,created_at DESC,id DESC);
-- +goose StatementBegin

DO $$ DECLARE old_check text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO old_check FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF old_check IS NULL THEN RAISE EXCEPTION 'webhook outbox event constraint missing'; END IF;
 IF position('issue.tracker_sync.attention' in old_check)>0 AND position('issue.tracker_sync.recovered' in old_check)>0 THEN RETURN; END IF;
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE 'ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((' || substring(old_check from 8 for length(old_check)-8) || ') OR event IN (''issue.tracker_sync.attention'',''issue.tracker_sync.recovered''))';
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Tracker notification incidents and delivery evidence must be preserved; rollback is not supported';
END $$;
-- +goose StatementEnd
