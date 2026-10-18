-- +goose Up
ALTER TABLE issue_tracker_linear_connections ADD COLUMN IF NOT EXISTS webhook_enabled boolean NOT NULL DEFAULT false;
-- +goose StatementBegin
DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_tracker_linear_connections'::regclass AND conname='issue_tracker_linear_webhook_connected') THEN
  ALTER TABLE issue_tracker_linear_connections ADD CONSTRAINT issue_tracker_linear_webhook_connected CHECK(NOT webhook_enabled OR connected);
 END IF;
END $issue_replay$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS issue_tracker_callbacks (
 scope_key text NOT NULL CHECK(octet_length(scope_key)<=128),
 delivery_id text NOT NULL CHECK(octet_length(delivery_id) BETWEEN 1 AND 128),
 provider text NOT NULL CHECK(provider IN ('github','linear')),
 app_id uuid REFERENCES apps(id) ON DELETE CASCADE,
 connection_revision bigint NOT NULL DEFAULT 0,
 installation_id bigint NOT NULL DEFAULT 0,
 repository_id bigint NOT NULL DEFAULT 0,
 workspace_id uuid,
 team_id uuid,
 ticket_id text NOT NULL CHECK(octet_length(ticket_id) BETWEEN 1 AND 128),
 ticket_url text NOT NULL DEFAULT '' CHECK(octet_length(ticket_url)<=512),
 payload_hash text NOT NULL CHECK(payload_hash ~ '^[a-f0-9]{64}$'),
 received_at timestamptz NOT NULL DEFAULT now(),
 processed_at timestamptz,
 PRIMARY KEY(scope_key,delivery_id),
 UNIQUE(scope_key,payload_hash),
 CHECK((provider='github' AND app_id IS NULL AND installation_id>0 AND repository_id>0 AND connection_revision=0 AND workspace_id IS NULL AND team_id IS NULL AND ticket_url<>'') OR
 (provider='linear' AND app_id IS NOT NULL AND connection_revision>0 AND installation_id=0 AND repository_id=0 AND workspace_id IS NOT NULL AND team_id IS NOT NULL AND ticket_url=''))
);

CREATE INDEX IF NOT EXISTS issue_tracker_callbacks_pending_idx ON issue_tracker_callbacks(received_at) WHERE processed_at IS NULL;

CREATE INDEX IF NOT EXISTS issue_tracker_callbacks_retention_idx ON issue_tracker_callbacks(processed_at) WHERE processed_at IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM issue_tracker_callbacks) OR EXISTS(SELECT 1 FROM issue_tracker_linear_connections WHERE webhook_enabled) THEN
  RAISE EXCEPTION 'preserve callback receipts and disconnect webhook-enabled connections before rollback';
 END IF;
END $$;
DROP TABLE issue_tracker_callbacks;
ALTER TABLE issue_tracker_linear_connections DROP CONSTRAINT issue_tracker_linear_webhook_connected, DROP COLUMN webhook_enabled;
-- +goose StatementEnd
